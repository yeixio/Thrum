// thrum-collector watches a Kubernetes cluster with read-only access and
// publishes a normalized health state for the Thrum sound engine.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
	"sigs.k8s.io/yaml"

	"github.com/yeixio/thrum/collector/internal/collect"
	"github.com/yeixio/thrum/collector/internal/health"
	"github.com/yeixio/thrum/collector/internal/server"
)

// fileConfig is the optional YAML configuration. Flags override it.
type fileConfig struct {
	Listen            string         `json:"listen"`
	Interval          string         `json:"interval"`
	Namespace         string         `json:"namespace"`
	ExcludeNamespaces []string       `json:"excludeNamespaces"`
	PendingGrace      string         `json:"pendingGrace"`
	AllowOrigins      []string       `json:"allowOrigins"`
	Health            *health.Config `json:"health"`
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("thrum-collector: %v", err)
	}
}

func run() error {
	var (
		kubeconfig = flag.String("kubeconfig", "", "path to a kubeconfig file (default: $KUBECONFIG, then ~/.kube/config)")
		kubeCtx    = flag.String("context", "", "kubeconfig context to use (default: the current context)")
		configPath = flag.String("config", "", "path to a YAML config file")
		listen     = flag.String("listen", "", "address to serve on (default 127.0.0.1:7878)")
		interval   = flag.Duration("interval", 0, "how often to publish state (default 2s)")
		namespace  = flag.String("namespace", "", "watch pods and deployments in this namespace only")
		exclude    = flag.String("exclude-namespaces", "", "comma-separated namespaces to ignore")
		once       = flag.Bool("once", false, "print the current state as JSON and exit")
	)
	flag.Parse()

	cfg := fileConfig{}
	hcfg := health.DefaultConfig()
	if *configPath != "" {
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			return err
		}
		cfg.Health = &hcfg // unmarshal on top of the defaults
		if err := yaml.UnmarshalStrict(raw, &cfg); err != nil {
			return fmt.Errorf("%s: %w", *configPath, err)
		}
	}
	addr := firstNonEmpty(*listen, cfg.Listen, "127.0.0.1:7878")
	every, err := duration(*interval, cfg.Interval, 2*time.Second)
	if err != nil {
		return fmt.Errorf("interval: %w", err)
	}
	grace, err := duration(0, cfg.PendingGrace, 90*time.Second)
	if err != nil {
		return fmt.Errorf("pendingGrace: %w", err)
	}
	ns := firstNonEmpty(*namespace, cfg.Namespace)
	excluded := cfg.ExcludeNamespaces
	if *exclude != "" {
		excluded = strings.Split(*exclude, ",")
	}

	token := os.Getenv("THRUM_TOKEN")
	if !*once && token == "" && !isLoopback(addr) {
		return errors.New("refusing to serve on a non-loopback address without THRUM_TOKEN set")
	}

	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = *kubeconfig
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: *kubeCtx})
	restCfg, err := loader.ClientConfig()
	if err != nil {
		return fmt.Errorf("loading kubeconfig: %w", err)
	}
	restCfg.UserAgent = "thrum-collector"
	clusterName := *kubeCtx
	if clusterName == "" {
		if raw, err := loader.RawConfig(); err == nil {
			clusterName = raw.CurrentContext
		}
	}

	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return err
	}
	metrics, err := metricsclient.NewForConfig(restCfg)
	if err != nil {
		return err
	}

	probe := func(ctx context.Context) (time.Duration, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		start := time.Now()
		_, err := client.Discovery().RESTClient().Get().AbsPath("/readyz").DoRaw(ctx)
		return time.Since(start), err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	col := collect.New(client, metrics, collect.Options{
		Namespace:         ns,
		ExcludeNamespaces: excluded,
		PendingGrace:      grace,
		Probe:             probe,
	})
	log.Printf("connecting to %q (read-only)", clusterName)
	if !col.Start(ctx) {
		return errors.New("could not sync with the cluster")
	}

	observe := func() (server.Payload, error) {
		snap, err := col.Snapshot(ctx)
		if err != nil {
			return server.Payload{}, err
		}
		return server.Payload{Cluster: clusterName, Time: time.Now().UTC(), State: health.Evaluate(snap, hcfg), Detail: snap}, nil
	}

	if *once {
		p, err := observe()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	}

	hub := server.NewHub()
	hub.Token = token
	hub.AllowOrigins = cfg.AllowOrigins
	srv := &http.Server{Addr: addr, Handler: hub.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		lastHealth := -1.0
		for {
			if p, err := observe(); err != nil {
				log.Printf("observe: %v", err)
			} else {
				hub.PublishState(p)
				if p.State.Health != lastHealth {
					log.Printf("health %.0f (nodes %d/%d ready, %d pods, %d crash looping, %d pending)",
						p.State.Health, p.Detail.NodesTotal-p.Detail.NodesNotReady, p.Detail.NodesTotal,
						p.Detail.PodsTotal, p.Detail.PodsCrashLooping, p.Detail.PodsPending)
					lastHealth = p.State.Health
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-col.Events():
				hub.PublishEvent(e)
			}
		}
	}()

	log.Printf("serving on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func duration(flagVal time.Duration, fileVal string, def time.Duration) (time.Duration, error) {
	if flagVal > 0 {
		return flagVal, nil
	}
	if fileVal != "" {
		return time.ParseDuration(fileVal)
	}
	return def, nil
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
