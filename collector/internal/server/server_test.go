package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeixio/thrum/collector/internal/collect"
	"github.com/yeixio/thrum/collector/internal/health"
)

func get(t *testing.T, srv *httptest.Server, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestStateEndpointMatchesTheEngineContract(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	if res := get(t, srv, "/v1/state", nil); res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("before first publish: status %d, want 503", res.StatusCode)
	}
	hub.PublishState(Payload{Cluster: "homelab", State: health.State{Health: 87}})

	var body map[string]any
	if err := json.NewDecoder(get(t, srv, "/v1/state", nil).Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	state := body["state"].(map[string]any)
	if state["health"].(float64) != 87 {
		t.Fatalf("health = %v", state["health"])
	}
	// These are the exact field names the TypeScript engine reads.
	for _, k := range []string{"cpu", "memory", "network", "disk"} {
		if _, ok := state["load"].(map[string]any)[k]; !ok {
			t.Errorf("load.%s missing", k)
		}
	}
	for _, k := range []string{"pendingPods", "crashLoop", "nodeNotReady", "apiLatency", "failedWorkloads"} {
		if _, ok := state["conditions"].(map[string]any)[k]; !ok {
			t.Errorf("conditions.%s missing", k)
		}
	}
}

func TestOnlyLocalOriginsMayReadTheAPI(t *testing.T) {
	hub := NewHub()
	hub.AllowOrigins = []string{"https://dash.example.com"}
	hub.PublishState(Payload{})
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	cases := map[string]int{
		"":                            200, // curl and other non-browser clients
		"http://localhost:5173":       200,
		"http://127.0.0.1:4173":       200,
		"tauri://localhost":           200,
		"https://dash.example.com":    200,
		"https://evil.example.com":    403,
		"http://localhost.evil.com":   403,
		"http://127.0.0.1.evil.com:8": 403,
	}
	for origin, want := range cases {
		hdr := map[string]string{}
		if origin != "" {
			hdr["Origin"] = origin
		}
		res := get(t, srv, "/v1/state", hdr)
		if res.StatusCode != want {
			t.Errorf("origin %q: status %d, want %d", origin, res.StatusCode, want)
		}
		if want == 200 && origin != "" && res.Header.Get("Access-Control-Allow-Origin") != origin {
			t.Errorf("origin %q: missing CORS header", origin)
		}
	}
}

func TestTokenAndReadOnly(t *testing.T) {
	hub := NewHub()
	hub.Token = "s3cret"
	hub.PublishState(Payload{})
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	if res := get(t, srv, "/v1/state", nil); res.StatusCode != 401 {
		t.Errorf("no token: %d", res.StatusCode)
	}
	if res := get(t, srv, "/v1/state", map[string]string{"Authorization": "Bearer wrong"}); res.StatusCode != 401 {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	if res := get(t, srv, "/v1/state", map[string]string{"Authorization": "Bearer s3cret"}); res.StatusCode != 200 {
		t.Errorf("header token: %d", res.StatusCode)
	}
	if res := get(t, srv, "/v1/state?token=s3cret", nil); res.StatusCode != 200 {
		t.Errorf("query token: %d", res.StatusCode)
	}
	if res := get(t, srv, "/healthz", nil); res.StatusCode != 200 {
		t.Errorf("healthz should not need a token: %d", res.StatusCode)
	}
	res, err := srv.Client().Post(srv.URL+"/v1/state?token=s3cret", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", res.StatusCode)
	}
}

func TestStreamSendsLatestStateThenUpdatesAndEvents(t *testing.T) {
	hub := NewHub()
	hub.PublishState(Payload{Cluster: "first"})
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/v1/stream", nil)
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	lines := bufio.NewScanner(res.Body)
	read := func() (kind, data string) {
		for lines.Scan() {
			l := lines.Text()
			switch {
			case strings.HasPrefix(l, "event: "):
				kind = strings.TrimPrefix(l, "event: ")
			case strings.HasPrefix(l, "data: "):
				data = strings.TrimPrefix(l, "data: ")
			case l == "" && kind != "":
				return kind, data
			}
		}
		t.Fatal("stream ended early")
		return "", ""
	}

	if kind, data := read(); kind != "state" || !strings.Contains(data, `"first"`) {
		t.Fatalf("first message: %s %s", kind, data)
	}
	hub.PublishState(Payload{Cluster: "second"})
	if kind, data := read(); kind != "state" || !strings.Contains(data, `"second"`) {
		t.Fatalf("update: %s %s", kind, data)
	}
	hub.PublishEvent(collect.Event{Type: collect.PodRestart, Namespace: "app", Name: "web-1"})
	if kind, data := read(); kind != "event" || !strings.Contains(data, `"podRestart"`) {
		t.Fatalf("event: %s %s", kind, data)
	}
}
