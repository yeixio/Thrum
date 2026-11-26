// Package server publishes cluster state over a small local HTTP API.
//
//	GET /healthz    liveness of the collector itself
//	GET /v1/state   the latest state as JSON
//	GET /v1/stream  server-sent events: "state" on every update, "event" for cluster events
package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yeixio/thrum/collector/internal/collect"
	"github.com/yeixio/thrum/collector/internal/health"
)

// Payload is one published update.
type Payload struct {
	Cluster string          `json:"cluster"`
	Time    time.Time       `json:"time"`
	State   health.State    `json:"state"`
	Detail  health.Snapshot `json:"detail"`
}

type message struct {
	kind string
	data []byte
}

// Hub keeps the latest payload and fans updates out to stream subscribers.
type Hub struct {
	// Token, when set, must be presented as a bearer token or ?token= value.
	Token string
	// AllowOrigins lists extra browser origins allowed to read the API.
	// Loopback origins and the desktop app are always allowed.
	AllowOrigins []string

	mu     sync.RWMutex
	latest []byte
	subs   map[chan message]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[chan message]struct{}{}} }

func (h *Hub) broadcast(m message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default: // A stalled client misses an update rather than blocking everyone.
		}
	}
}

// PublishState stores and broadcasts a new state.
func (h *Hub) PublishState(p Payload) {
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.latest = data
	h.mu.Unlock()
	h.broadcast(message{"state", data})
}

// PublishEvent broadcasts a cluster event.
func (h *Hub) PublishEvent(e collect.Event) {
	if data, err := json.Marshal(e); err == nil {
		h.broadcast(message{"event", data})
	}
}

// originAllowed decides which web pages may read the API. Without this,
// any site open in the user's browser could read cluster health from the
// collector on localhost.
func (h *Hub) originAllowed(origin string) bool {
	for _, o := range h.AllowOrigins {
		if o == "*" || o == origin {
			return true
		}
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Scheme == "tauri" {
		return true
	}
	host := u.Hostname()
	if host == "localhost" || host == "tauri.localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (h *Hub) authorized(r *http.Request) bool {
	if h.Token == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		// EventSource cannot set headers, so the stream also accepts a query value.
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.Token)) == 1
}

func (h *Hub) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if !h.originAllowed(origin) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Headers", "Authorization")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "read only", http.StatusMethodNotAllowed)
			return
		}
		if !h.authorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Handler returns the HTTP API.
func (h *Hub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/v1/state", h.guard(h.serveState))
	mux.HandleFunc("/v1/stream", h.guard(h.serveStream))
	return mux
}

func (h *Hub) serveState(w http.ResponseWriter, _ *http.Request) {
	h.mu.RLock()
	data := h.latest
	h.mu.RUnlock()
	if data == nil {
		http.Error(w, "no state yet", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (h *Hub) serveStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := make(chan message, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	latest := h.latest
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}()

	send := func(m message) {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", m.kind, m.data)
		flusher.Flush()
	}
	if latest != nil {
		send(message{"state", latest})
	}
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-ch:
			send(m)
		case <-keepAlive.C:
			fmt.Fprint(w, ": keep-alive\n\n")
			flusher.Flush()
		}
	}
}
