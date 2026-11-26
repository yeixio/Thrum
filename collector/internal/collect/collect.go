// Package collect watches a cluster through read-only list and watch
// calls and summarizes what it sees. It never writes to the cluster.
package collect

import (
	"context"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/yeixio/thrum/collector/internal/health"
)

// EventType names match the sound engine's event signatures.
type EventType string

const (
	PodRestart       EventType = "podRestart"
	Deployment       EventType = "deployment"
	ScaleUp          EventType = "scaleUp"
	ScaleDown        EventType = "scaleDown"
	NodeFailure      EventType = "nodeFailure"
	DeploymentFailed EventType = "deploymentFailed"
)

// Event is something notable that happened in the cluster.
type Event struct {
	Type      EventType `json:"type"`
	Namespace string    `json:"namespace,omitempty"`
	Name      string    `json:"name"`
	Time      time.Time `json:"time"`
}

// Options configure a Collector.
type Options struct {
	// Namespace limits pods and deployments to one namespace. Empty means all.
	Namespace string
	// ExcludeNamespaces are ignored when watching all namespaces.
	ExcludeNamespaces []string
	// PendingGrace is how long a pod may be Pending before it counts as a
	// problem. Scheduling and image pulls take a moment on a healthy cluster.
	PendingGrace time.Duration
	// FailedWindow is how long a Failed pod keeps counting after it failed.
	FailedWindow time.Duration
	// Probe measures an API server round trip. Nil disables the measurement.
	Probe func(ctx context.Context) (time.Duration, error)
	// Now is injectable for tests.
	Now func() time.Time
}

// Collector holds informer caches for nodes, pods and deployments.
type Collector struct {
	opts     Options
	metrics  metricsclient.Interface
	factory  informers.SharedInformerFactory
	nsFactor informers.SharedInformerFactory
	nodes    corelisters.NodeLister
	pods     corelisters.PodLister
	deploys  appslisters.DeploymentLister
	events   chan Event
	exclude  map[string]bool

	mu          sync.Mutex
	synced      bool
	metricsNext time.Time
	metricsOK   bool
	cpuUse      float64
	memUse      float64
}

// New builds a collector. metrics may be nil when the Metrics API is not wanted.
func New(client kubernetes.Interface, metrics metricsclient.Interface, opts Options) *Collector {
	if opts.PendingGrace == 0 {
		opts.PendingGrace = 90 * time.Second
	}
	if opts.FailedWindow == 0 {
		opts.FailedWindow = 10 * time.Minute
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	c := &Collector{
		opts:    opts,
		metrics: metrics,
		events:  make(chan Event, 256),
		exclude: map[string]bool{},
	}
	for _, ns := range opts.ExcludeNamespaces {
		c.exclude[ns] = true
	}
	// Nodes are cluster scoped. Pods and deployments may be limited to a namespace.
	c.factory = informers.NewSharedInformerFactory(client, 10*time.Minute)
	c.nsFactor = informers.NewSharedInformerFactoryWithOptions(client, 10*time.Minute, informers.WithNamespace(opts.Namespace))

	nodeInf := c.factory.Core().V1().Nodes()
	podInf := c.nsFactor.Core().V1().Pods()
	depInf := c.nsFactor.Apps().V1().Deployments()
	c.nodes, c.pods, c.deploys = nodeInf.Lister(), podInf.Lister(), depInf.Lister()

	_, _ = nodeInf.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{UpdateFunc: c.onNodeUpdate})
	_, _ = podInf.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{UpdateFunc: c.onPodUpdate})
	_, _ = depInf.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    c.onDeploymentAdd,
		UpdateFunc: c.onDeploymentUpdate,
	})
	return c
}

// Start begins watching and blocks until the caches have synced once.
func (c *Collector) Start(ctx context.Context) bool {
	c.factory.Start(ctx.Done())
	c.nsFactor.Start(ctx.Done())
	for _, ok := range c.factory.WaitForCacheSync(ctx.Done()) {
		if !ok {
			return false
		}
	}
	for _, ok := range c.nsFactor.WaitForCacheSync(ctx.Done()) {
		if !ok {
			return false
		}
	}
	c.mu.Lock()
	c.synced = true
	c.mu.Unlock()
	return true
}

// Events returns notable cluster events. Events that happened before
// Start returned are not reported.
func (c *Collector) Events() <-chan Event { return c.events }

func (c *Collector) emit(t EventType, namespace, name string) {
	c.mu.Lock()
	synced := c.synced
	c.mu.Unlock()
	if !synced || c.exclude[namespace] {
		return
	}
	select {
	case c.events <- Event{Type: t, Namespace: namespace, Name: name, Time: c.opts.Now()}:
	default: // A slow consumer loses events rather than blocking the watch.
	}
}

func nodeReady(n *corev1.Node) bool {
	for _, cond := range n.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func nodeUnderPressure(n *corev1.Node) bool {
	for _, cond := range n.Status.Conditions {
		switch cond.Type {
		case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure:
			if cond.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

func restarts(p *corev1.Pod) int32 {
	var n int32
	for _, s := range p.Status.InitContainerStatuses {
		n += s.RestartCount
	}
	for _, s := range p.Status.ContainerStatuses {
		n += s.RestartCount
	}
	return n
}

func deploymentCondition(d *appsv1.Deployment, t appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range d.Status.Conditions {
		if d.Status.Conditions[i].Type == t {
			return &d.Status.Conditions[i]
		}
	}
	return nil
}

func deploymentStalled(d *appsv1.Deployment) bool {
	p := deploymentCondition(d, appsv1.DeploymentProgressing)
	return p != nil && p.Status == corev1.ConditionFalse && p.Reason == "ProgressDeadlineExceeded"
}

func desiredReplicas(d *appsv1.Deployment) int32 {
	if d.Spec.Replicas == nil {
		return 1
	}
	return *d.Spec.Replicas
}

// deploymentUnavailable is true when a deployment that should be running
// is below its minimum availability or its rollout has stalled. A rolling
// update that is still inside its availability budget does not count.
func deploymentUnavailable(d *appsv1.Deployment) bool {
	if desiredReplicas(d) == 0 {
		return false
	}
	if deploymentStalled(d) {
		return true
	}
	a := deploymentCondition(d, appsv1.DeploymentAvailable)
	return a != nil && a.Status == corev1.ConditionFalse
}

func (c *Collector) onNodeUpdate(oldObj, newObj any) {
	o, ok1 := oldObj.(*corev1.Node)
	n, ok2 := newObj.(*corev1.Node)
	if ok1 && ok2 && nodeReady(o) && !nodeReady(n) {
		c.emit(NodeFailure, "", n.Name)
	}
}

func (c *Collector) onPodUpdate(oldObj, newObj any) {
	o, ok1 := oldObj.(*corev1.Pod)
	n, ok2 := newObj.(*corev1.Pod)
	if ok1 && ok2 && restarts(n) > restarts(o) {
		c.emit(PodRestart, n.Namespace, n.Name)
	}
}

func (c *Collector) onDeploymentAdd(obj any) {
	if d, ok := obj.(*appsv1.Deployment); ok {
		c.emit(Deployment, d.Namespace, d.Name)
	}
}

func (c *Collector) onDeploymentUpdate(oldObj, newObj any) {
	o, ok1 := oldObj.(*appsv1.Deployment)
	n, ok2 := newObj.(*appsv1.Deployment)
	if !ok1 || !ok2 {
		return
	}
	switch {
	case !deploymentStalled(o) && deploymentStalled(n):
		c.emit(DeploymentFailed, n.Namespace, n.Name)
	case desiredReplicas(n) > desiredReplicas(o):
		c.emit(ScaleUp, n.Namespace, n.Name)
	case desiredReplicas(n) < desiredReplicas(o):
		c.emit(ScaleDown, n.Namespace, n.Name)
	case n.Generation != o.Generation:
		c.emit(Deployment, n.Namespace, n.Name)
	}
}

// Waiting reasons that mean a container cannot start without someone fixing something.
var failingReasons = map[string]bool{
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"InvalidImageName":           true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
}

type podClass int

const (
	podFine podClass = iota
	podPending
	podCrashLooping
	podFailing
	podIgnored
)

func (c *Collector) classify(p *corev1.Pod, now time.Time) podClass {
	if p.DeletionTimestamp != nil || p.Status.Phase == corev1.PodSucceeded {
		return podIgnored
	}
	if p.Status.Phase == corev1.PodFailed {
		// Failed pods (evictions, finished jobs) can linger for days.
		// Only recent failures say anything about current health.
		at := p.CreationTimestamp.Time
		for _, s := range p.Status.ContainerStatuses {
			if t := s.State.Terminated; t != nil && t.FinishedAt.After(at) {
				at = t.FinishedAt.Time
			}
		}
		if now.Sub(at) <= c.opts.FailedWindow {
			return podFailing
		}
		return podIgnored
	}
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	failing := false
	for _, s := range statuses {
		if w := s.State.Waiting; w != nil {
			if w.Reason == "CrashLoopBackOff" {
				return podCrashLooping
			}
			if failingReasons[w.Reason] {
				failing = true
			}
		}
	}
	if failing {
		return podFailing
	}
	if p.Status.Phase == corev1.PodPending && now.Sub(p.CreationTimestamp.Time) > c.opts.PendingGrace {
		return podPending
	}
	return podFine
}

// Snapshot summarizes the current cache contents.
func (c *Collector) Snapshot(ctx context.Context) (health.Snapshot, error) {
	var s health.Snapshot
	now := c.opts.Now()

	nodes, err := c.nodes.List(labels.Everything())
	if err != nil {
		return s, err
	}
	for _, n := range nodes {
		s.NodesTotal++
		if !nodeReady(n) {
			s.NodesNotReady++
			continue
		}
		if nodeUnderPressure(n) {
			s.NodesUnderPressure++
		}
		// Capacity only counts nodes that can actually run work.
		s.CPUCapacity += n.Status.Allocatable.Cpu().AsApproximateFloat64()
		s.MemoryCapacity += n.Status.Allocatable.Memory().AsApproximateFloat64()
	}

	pods, err := c.pods.List(labels.Everything())
	if err != nil {
		return s, err
	}
	for _, p := range pods {
		if c.exclude[p.Namespace] {
			continue
		}
		switch c.classify(p, now) {
		case podIgnored:
			continue
		case podPending:
			s.PodsPending++
		case podCrashLooping:
			s.PodsCrashLooping++
		case podFailing:
			s.PodsFailing++
		}
		s.PodsTotal++
	}

	deploys, err := c.deploys.List(labels.Everything())
	if err != nil {
		return s, err
	}
	for _, d := range deploys {
		if c.exclude[d.Namespace] {
			continue
		}
		s.DeploymentsTotal++
		if deploymentUnavailable(d) {
			s.DeploymentsUnavailable++
		}
	}

	c.readMetrics(ctx, now, &s)

	s.APIReachable = true
	if c.opts.Probe != nil {
		d, err := c.opts.Probe(ctx)
		s.APIReachable = err == nil
		s.APILatency = d
		s.APILatencyMS = float64(d) / float64(time.Millisecond)
	}
	return s, nil
}

// readMetrics asks the Metrics API for node usage. Clusters without
// metrics-server are supported: the call is retried only occasionally and
// load is reported as unavailable in the meantime.
func (c *Collector) readMetrics(ctx context.Context, now time.Time, s *health.Snapshot) {
	if c.metrics == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.metricsNext) {
		s.MetricsAvailable, s.CPUUsage, s.MemoryUsage = c.metricsOK, c.cpuUse, c.memUse
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	list, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		c.metricsOK = false
		c.metricsNext = now.Add(time.Minute)
		return
	}
	c.cpuUse, c.memUse = 0, 0
	for _, m := range list.Items {
		c.cpuUse += m.Usage.Cpu().AsApproximateFloat64()
		c.memUse += m.Usage.Memory().AsApproximateFloat64()
	}
	c.metricsOK = len(list.Items) > 0
	// metrics-server itself only refreshes every 15 seconds or so.
	c.metricsNext = now.Add(10 * time.Second)
	s.MetricsAvailable, s.CPUUsage, s.MemoryUsage = c.metricsOK, c.cpuUse, c.memUse
}
