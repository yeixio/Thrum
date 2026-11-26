package collect

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func node(name string, ready bool, pressure ...corev1.NodeConditionType) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("8Gi"),
			},
		},
	}
	for _, p := range pressure {
		n.Status.Conditions = append(n.Status.Conditions, corev1.NodeCondition{Type: p, Status: corev1.ConditionTrue})
	}
	return n
}

func pod(ns, name string, phase corev1.PodPhase, age time.Duration, waiting string, restarts int32) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, CreationTimestamp: metav1.NewTime(now.Add(-age))},
		Status:     corev1.PodStatus{Phase: phase},
	}
	cs := corev1.ContainerStatus{Name: "main", RestartCount: restarts}
	if waiting != "" {
		cs.State.Waiting = &corev1.ContainerStateWaiting{Reason: waiting}
	}
	p.Status.ContainerStatuses = []corev1.ContainerStatus{cs}
	return p
}

func deployment(ns, name string, replicas int32, available bool, stalled bool) *appsv1.Deployment {
	d := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
	}
	st := corev1.ConditionTrue
	if !available {
		st = corev1.ConditionFalse
	}
	d.Status.Conditions = []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: st}}
	if stalled {
		d.Status.Conditions = append(d.Status.Conditions, appsv1.DeploymentCondition{
			Type: appsv1.DeploymentProgressing, Status: corev1.ConditionFalse, Reason: "ProgressDeadlineExceeded",
		})
	}
	return d
}

func start(t *testing.T, opts Options, objs ...runtime.Object) (*Collector, *fake.Clientset, context.Context) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := fake.NewSimpleClientset(objs...)
	opts.Now = func() time.Time { return now }
	c := New(client, nil, opts)
	if !c.Start(ctx) {
		t.Fatal("cache did not sync")
	}
	return c, client, ctx
}

func TestSnapshotClassifiesWorkloads(t *testing.T) {
	c, _, ctx := start(t, Options{},
		node("a", true), node("b", true, corev1.NodeMemoryPressure), node("c", false),
		pod("app", "ok", corev1.PodRunning, time.Hour, "", 0),
		pod("app", "starting", corev1.PodPending, 10*time.Second, "ContainerCreating", 0),
		pod("app", "stuck", corev1.PodPending, 10*time.Minute, "", 0),
		pod("app", "crash", corev1.PodRunning, time.Hour, "CrashLoopBackOff", 14),
		pod("app", "badimage", corev1.PodPending, 10*time.Minute, "ImagePullBackOff", 0),
		pod("app", "done", corev1.PodSucceeded, time.Hour, "", 0),
		pod("app", "evicted-long-ago", corev1.PodFailed, 48*time.Hour, "", 0),
		pod("app", "just-failed", corev1.PodFailed, time.Minute, "", 0),
		deployment("app", "web", 3, true, false),
		deployment("app", "down", 2, false, false),
		deployment("app", "stalled", 2, true, true),
		deployment("app", "scaled-to-zero", 0, false, false),
	)
	s, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	check("NodesTotal", s.NodesTotal, 3)
	check("NodesNotReady", s.NodesNotReady, 1)
	check("NodesUnderPressure", s.NodesUnderPressure, 1)
	check("PodsTotal", s.PodsTotal, 6) // succeeded and long-evicted pods are ignored
	check("PodsPending", s.PodsPending, 1)
	check("PodsCrashLooping", s.PodsCrashLooping, 1)
	check("PodsFailing", s.PodsFailing, 2)
	check("DeploymentsTotal", s.DeploymentsTotal, 4)
	check("DeploymentsUnavailable", s.DeploymentsUnavailable, 2)
	if s.CPUCapacity != 8 {
		t.Errorf("CPUCapacity = %v, want 8 (NotReady nodes do not count)", s.CPUCapacity)
	}
	if s.MetricsAvailable {
		t.Error("MetricsAvailable should be false without a metrics client")
	}
	if !s.APIReachable {
		t.Error("APIReachable should default to true without a probe")
	}
}

func TestExcludedNamespacesAreIgnored(t *testing.T) {
	c, _, ctx := start(t, Options{ExcludeNamespaces: []string{"sandbox"}},
		node("a", true),
		pod("app", "ok", corev1.PodRunning, time.Hour, "", 0),
		pod("sandbox", "crash", corev1.PodRunning, time.Hour, "CrashLoopBackOff", 5),
		deployment("sandbox", "down", 1, false, false),
	)
	s, _ := c.Snapshot(ctx)
	if s.PodsTotal != 1 || s.PodsCrashLooping != 0 || s.DeploymentsTotal != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestNamespaceScope(t *testing.T) {
	c, _, ctx := start(t, Options{Namespace: "app"},
		node("a", true),
		pod("app", "ok", corev1.PodRunning, time.Hour, "", 0),
		pod("other", "crash", corev1.PodRunning, time.Hour, "CrashLoopBackOff", 5),
	)
	s, _ := c.Snapshot(ctx)
	if s.PodsTotal != 1 || s.PodsCrashLooping != 0 || s.NodesTotal != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestMetricsAndProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := fake.NewSimpleClientset(node("a", true), node("b", true))
	// The generated fake's tracker guesses the wrong resource name for
	// NodeMetrics, so serve the list through a reactor instead.
	metrics := metricsfake.NewSimpleClientset()
	metrics.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{
			{ObjectMeta: metav1.ObjectMeta{Name: "a"}, Usage: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("1500m"), corev1.ResourceMemory: resource.MustParse("2Gi")}},
			{ObjectMeta: metav1.ObjectMeta{Name: "b"}, Usage: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("2Gi")}},
		}}, nil
	})
	c := New(client, metrics, Options{
		Now:   func() time.Time { return now },
		Probe: func(context.Context) (time.Duration, error) { return 42 * time.Millisecond, nil },
	})
	if !c.Start(ctx) {
		t.Fatal("cache did not sync")
	}
	s, err := c.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !s.MetricsAvailable || s.CPUUsage != 2 || s.CPUCapacity != 8 {
		t.Fatalf("metrics: %+v", s)
	}
	if s.APILatencyMS != 42 || !s.APIReachable {
		t.Fatalf("probe: %+v", s)
	}
}

func next(t *testing.T, c *Collector) Event {
	t.Helper()
	select {
	case e := <-c.Events():
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for an event")
		return Event{}
	}
}

func TestEvents(t *testing.T) {
	c, client, ctx := start(t, Options{},
		node("a", true),
		pod("app", "web-1", corev1.PodRunning, time.Hour, "", 0),
		deployment("app", "web", 2, true, false),
	)
	// Nothing that existed before start is reported.
	select {
	case e := <-c.Events():
		t.Fatalf("unexpected startup event: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}

	p := pod("app", "web-1", corev1.PodRunning, time.Hour, "", 1)
	if _, err := client.CoreV1().Pods("app").UpdateStatus(ctx, p, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, c); e.Type != PodRestart || e.Name != "web-1" {
		t.Fatalf("got %+v, want podRestart", e)
	}

	d := deployment("app", "web", 5, true, false)
	if _, err := client.AppsV1().Deployments("app").Update(ctx, d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, c); e.Type != ScaleUp {
		t.Fatalf("got %+v, want scaleUp", e)
	}

	d = deployment("app", "web", 1, true, false)
	_, _ = client.AppsV1().Deployments("app").Update(ctx, d, metav1.UpdateOptions{})
	if e := next(t, c); e.Type != ScaleDown {
		t.Fatalf("got %+v, want scaleDown", e)
	}

	d = deployment("app", "web", 1, true, false)
	d.Generation = 2
	_, _ = client.AppsV1().Deployments("app").Update(ctx, d, metav1.UpdateOptions{})
	if e := next(t, c); e.Type != Deployment {
		t.Fatalf("got %+v, want deployment", e)
	}

	d = deployment("app", "web", 1, true, true)
	d.Generation = 2
	_, _ = client.AppsV1().Deployments("app").Update(ctx, d, metav1.UpdateOptions{})
	if e := next(t, c); e.Type != DeploymentFailed {
		t.Fatalf("got %+v, want deploymentFailed", e)
	}

	if _, err := client.CoreV1().Nodes().Update(ctx, node("a", false), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, c); e.Type != NodeFailure || e.Name != "a" {
		t.Fatalf("got %+v, want nodeFailure", e)
	}

	if _, err := client.AppsV1().Deployments("app").Create(ctx, deployment("app", "api", 1, true, false), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	if e := next(t, c); e.Type != Deployment || e.Name != "api" {
		t.Fatalf("got %+v, want deployment for api", e)
	}
}
