// Package fakeapi is a tiny stand-in for a Kubernetes API server. It
// serves just enough of the read-only API for the collector to run, so the
// whole pipeline can be tested and demonstrated without a cluster.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
)

// Cluster is the content the fake server reports.
type Cluster struct {
	Nodes       []corev1.Node
	Pods        []corev1.Pod
	Deployments []appsv1.Deployment
	NodeMetrics []metricsv1beta1.NodeMetrics
}

func node(name string, ready bool) corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, ResourceVersion: "1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
}

func pod(ns, name string, phase corev1.PodPhase, waiting string) corev1.Pod {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns, Name: name, ResourceVersion: "1",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
		},
		Status: corev1.PodStatus{Phase: phase},
	}
	cs := corev1.ContainerStatus{Name: "main"}
	if waiting != "" {
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: waiting}
		cs.RestartCount = 12
	}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{cs}
	return p
}

func deployment(ns, name string, replicas int32, available bool) appsv1.Deployment {
	st := corev1.ConditionTrue
	if !available {
		st = corev1.ConditionFalse
	}
	return appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, ResourceVersion: "1", Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: st}},
		},
	}
}

func usage(name, cpu, mem string) metricsv1beta1.NodeMetrics {
	return metricsv1beta1.NodeMetrics{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Usage: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpu),
			corev1.ResourceMemory: resource.MustParse(mem),
		},
	}
}

// Scenario returns a named example cluster: "healthy", "busy" or "failing".
func Scenario(name string) (Cluster, error) {
	c := Cluster{
		Nodes: []corev1.Node{node("node-a", true), node("node-b", true), node("node-c", true)},
		Deployments: []appsv1.Deployment{
			deployment("shop", "web", 3, true), deployment("shop", "api", 3, true), deployment("shop", "worker", 2, true),
		},
		NodeMetrics: []metricsv1beta1.NodeMetrics{
			usage("node-a", "600m", "5Gi"), usage("node-b", "500m", "4Gi"), usage("node-c", "700m", "5Gi"),
		},
	}
	for i := 0; i < 8; i++ {
		c.Pods = append(c.Pods, pod("shop", fmt.Sprintf("web-%d", i), corev1.PodRunning, ""))
	}
	switch name {
	case "healthy":
	case "busy":
		c.NodeMetrics = []metricsv1beta1.NodeMetrics{
			usage("node-a", "3700m", "12Gi"), usage("node-b", "3600m", "11Gi"), usage("node-c", "3800m", "12Gi"),
		}
	case "failing":
		c.Nodes[2] = node("node-c", false)
		c.NodeMetrics = c.NodeMetrics[:2]
		c.Pods = append(c.Pods,
			pod("shop", "api-0", corev1.PodRunning, "CrashLoopBackOff"),
			pod("shop", "api-1", corev1.PodRunning, "CrashLoopBackOff"),
			pod("shop", "api-2", corev1.PodRunning, "CrashLoopBackOff"),
			pod("shop", "worker-0", corev1.PodPending, ""),
		)
		c.Deployments[1] = deployment("shop", "api", 3, false)
	default:
		return c, fmt.Errorf("unknown scenario %q (want healthy, busy or failing)", name)
	}
	return c, nil
}

// collection describes one resource type the fake server can list and watch.
type collection struct {
	kind, apiVersion string
	// items returns the objects in a namespace ("" means all), each with
	// its kind and apiVersion filled in.
	items func(ns string) []any
	// list wraps items in the matching List type.
	list func(items []any) any
}

func typeMeta(kind, apiVersion string) metav1.TypeMeta {
	return metav1.TypeMeta{Kind: kind, APIVersion: apiVersion}
}

// Handler serves list and watch requests for the cluster. Nothing ever
// changes, so a watch stays quiet after any initial events it was asked for.
func Handler(c Cluster) http.Handler {
	writeJSON := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	meta := metav1.ListMeta{ResourceVersion: "1"}

	serve := func(res collection) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				// The collector must never write. Make it loud if it tries.
				http.Error(w, "fakeapi is read-only", http.StatusMethodNotAllowed)
				return
			}
			ns := ""
			if parts := strings.Split(r.URL.Path, "/namespaces/"); len(parts) == 2 {
				ns = strings.Split(parts[1], "/")[0]
			}
			q := r.URL.Query()
			if q.Get("watch") != "true" {
				writeJSON(w, res.list(res.items(ns)))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			enc := json.NewEncoder(w)
			if q.Get("sendInitialEvents") == "true" {
				// Newer clients list through the watch: send every object,
				// then a bookmark that marks the end of the initial state.
				for _, item := range res.items(ns) {
					_ = enc.Encode(map[string]any{"type": "ADDED", "object": item})
				}
				_ = enc.Encode(map[string]any{"type": "BOOKMARK", "object": map[string]any{
					"kind": res.kind, "apiVersion": res.apiVersion,
					"metadata": map[string]any{
						"resourceVersion": "1",
						"annotations":     map[string]string{"k8s.io/initial-events-end": "true"},
					},
				}})
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		}
	}

	nodes := serve(collection{
		kind: "Node", apiVersion: "v1",
		items: func(string) []any {
			out := []any{}
			for _, n := range c.Nodes {
				n.TypeMeta = typeMeta("Node", "v1")
				out = append(out, n)
			}
			return out
		},
		list: func(items []any) any {
			return map[string]any{"kind": "NodeList", "apiVersion": "v1", "metadata": meta, "items": items}
		},
	})
	pods := serve(collection{
		kind: "Pod", apiVersion: "v1",
		items: func(ns string) []any {
			out := []any{}
			for _, p := range c.Pods {
				if ns == "" || p.Namespace == ns {
					p.TypeMeta = typeMeta("Pod", "v1")
					out = append(out, p)
				}
			}
			return out
		},
		list: func(items []any) any {
			return map[string]any{"kind": "PodList", "apiVersion": "v1", "metadata": meta, "items": items}
		},
	})
	deployments := serve(collection{
		kind: "Deployment", apiVersion: "apps/v1",
		items: func(ns string) []any {
			out := []any{}
			for _, d := range c.Deployments {
				if ns == "" || d.Namespace == ns {
					d.TypeMeta = typeMeta("Deployment", "apps/v1")
					out = append(out, d)
				}
			}
			return out
		},
		list: func(items []any) any {
			return map[string]any{"kind": "DeploymentList", "apiVersion": "apps/v1", "metadata": meta, "items": items}
		},
	})
	nodeMetrics := serve(collection{
		kind: "NodeMetrics", apiVersion: "metrics.k8s.io/v1beta1",
		items: func(string) []any {
			out := []any{}
			for _, m := range c.NodeMetrics {
				m.TypeMeta = typeMeta("NodeMetrics", "metrics.k8s.io/v1beta1")
				out = append(out, m)
			}
			return out
		},
		list: func(items []any) any {
			return map[string]any{"kind": "NodeMetricsList", "apiVersion": "metrics.k8s.io/v1beta1", "metadata": meta, "items": items}
		},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("/api/v1/nodes", nodes)
	mux.HandleFunc("/api/v1/pods", pods)
	mux.HandleFunc("/api/v1/namespaces/", pods)
	mux.HandleFunc("/apis/apps/v1/deployments", deployments)
	mux.HandleFunc("/apis/apps/v1/namespaces/", deployments)
	mux.HandleFunc("/apis/metrics.k8s.io/v1beta1/nodes", nodeMetrics)
	return mux
}

// Kubeconfig returns a kubeconfig that points at a fake server.
func Kubeconfig(serverURL, name string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: %[2]s
clusters:
- name: %[2]s
  cluster:
    server: %[1]s
contexts:
- name: %[2]s
  context:
    cluster: %[2]s
    user: %[2]s
users:
- name: %[2]s
  user: {}
`, serverURL, name)
}
