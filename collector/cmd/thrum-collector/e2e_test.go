package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeixio/thrum/collector/internal/fakeapi"
	"github.com/yeixio/thrum/collector/internal/server"
)

// Builds the real binary and runs it against a fake API server over HTTP,
// which exercises kubeconfig loading, client-go, the Metrics API and the
// health model together.
func TestCollectorEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "thrum-collector")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	observe := func(scenario string) server.Payload {
		t.Helper()
		cluster, err := fakeapi.Scenario(scenario)
		if err != nil {
			t.Fatal(err)
		}
		api := httptest.NewServer(fakeapi.Handler(cluster))
		defer api.Close()
		kubeconfig := filepath.Join(dir, scenario+".kubeconfig")
		if err := os.WriteFile(kubeconfig, []byte(fakeapi.Kubeconfig(api.URL, scenario)), 0o600); err != nil {
			t.Fatal(err)
		}
		// A collector that cannot sync would otherwise hang the whole suite.
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "--kubeconfig", kubeconfig, "--once")
		cmd.Env = append(os.Environ(), "KUBECONFIG=")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", scenario, err)
		}
		var p server.Payload
		if err := json.Unmarshal(out, &p); err != nil {
			t.Fatalf("%s: %v\n%s", scenario, err, out)
		}
		return p
	}

	// Listing contexts must work without contacting a cluster.
	offline := filepath.Join(dir, "offline.kubeconfig")
	if err := os.WriteFile(offline, []byte(fakeapi.Kubeconfig("http://127.0.0.1:1", "homelab")), 0o600); err != nil {
		t.Fatal(err)
	}
	listCmd := exec.Command(bin, "--kubeconfig", offline, "--list-contexts")
	listCmd.Env = append(os.Environ(), "KUBECONFIG=")
	listOut, err := listCmd.Output()
	if err != nil {
		t.Fatalf("list-contexts: %v", err)
	}
	var contexts struct {
		Current  string
		Contexts []string
	}
	if err := json.Unmarshal(listOut, &contexts); err != nil || contexts.Current != "homelab" || len(contexts.Contexts) != 1 {
		t.Fatalf("list-contexts: %v %s", err, listOut)
	}

	healthy := observe("healthy")
	if healthy.Cluster != "healthy" || healthy.State.Health != 100 {
		t.Errorf("healthy: %+v", healthy.State)
	}
	if !healthy.Detail.MetricsAvailable || healthy.State.Load.CPU < 0.1 || healthy.State.Load.CPU > 0.2 {
		t.Errorf("healthy load: %+v", healthy.State.Load)
	}

	busy := observe("busy")
	if busy.State.Health != 100 || busy.State.Load.CPU < 0.9 {
		t.Errorf("busy should be loud but healthy: %+v", busy.State)
	}

	failing := observe("failing")
	if failing.State.Health > 40 {
		t.Errorf("failing: health %v, want <= 40", failing.State.Health)
	}
	c := failing.State.Conditions
	if c.CrashLoop < 0.5 || c.NodeNotReady < 0.5 || c.FailedWorkloads < 0.3 || c.PendingPods <= 0 {
		t.Errorf("failing conditions: %+v", c)
	}
	if failing.Detail.NodesNotReady != 1 || failing.Detail.PodsCrashLooping != 3 {
		t.Errorf("failing detail: %+v", failing.Detail)
	}
}
