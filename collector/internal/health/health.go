// Package health turns raw cluster facts into a normalized health state.
//
// It knows nothing about Kubernetes clients or about sound. The collector
// feeds it a Snapshot, and anything (the sound engine, a dashboard, a
// script) can consume the State it returns.
package health

import (
	"math"
	"time"
)

// Load describes how busy the cluster is. Each value is 0..1.
// Busy is not unhealthy: load never lowers the health score.
type Load struct {
	CPU     float64 `json:"cpu"`
	Memory  float64 `json:"memory"`
	Network float64 `json:"network"`
	Disk    float64 `json:"disk"`
}

// Conditions describes how unwell the cluster is. Each value is a 0..1 severity.
type Conditions struct {
	PendingPods     float64 `json:"pendingPods"`
	CrashLoop       float64 `json:"crashLoop"`
	NodeNotReady    float64 `json:"nodeNotReady"`
	APILatency      float64 `json:"apiLatency"`
	FailedWorkloads float64 `json:"failedWorkloads"`
}

// State is the contract with every consumer. Health is 0 (critical) to 100.
type State struct {
	Health     float64    `json:"health"`
	Load       Load       `json:"load"`
	Conditions Conditions `json:"conditions"`
}

// Snapshot is a point-in-time count of what the collector observed.
type Snapshot struct {
	NodesTotal         int `json:"nodesTotal"`
	NodesNotReady      int `json:"nodesNotReady"`
	NodesUnderPressure int `json:"nodesUnderPressure"`

	PodsTotal        int `json:"podsTotal"`
	PodsPending      int `json:"podsPending"`
	PodsCrashLooping int `json:"podsCrashLooping"`
	PodsFailing      int `json:"podsFailing"`

	DeploymentsTotal       int `json:"deploymentsTotal"`
	DeploymentsUnavailable int `json:"deploymentsUnavailable"`

	// Usage and capacity in cores and bytes. Only meaningful when
	// MetricsAvailable is true (the cluster runs metrics-server).
	MetricsAvailable bool    `json:"metricsAvailable"`
	CPUUsage         float64 `json:"cpuUsageCores"`
	CPUCapacity      float64 `json:"cpuCapacityCores"`
	MemoryUsage      float64 `json:"memoryUsageBytes"`
	MemoryCapacity   float64 `json:"memoryCapacityBytes"`

	APIReachable bool          `json:"apiReachable"`
	APILatency   time.Duration `json:"-"`
	APILatencyMS float64       `json:"apiLatencyMs"`
}

// Weights say what fraction of the remaining health each fully severe
// condition takes away. A weight of 0 ignores the condition. A weight of 1
// means that condition alone brings health to 0.
type Weights struct {
	PendingPods     float64 `json:"pendingPods"`
	CrashLoop       float64 `json:"crashLoop"`
	NodeNotReady    float64 `json:"nodeNotReady"`
	APILatency      float64 `json:"apiLatency"`
	FailedWorkloads float64 `json:"failedWorkloads"`
}

// Config holds the user-tunable health rules.
type Config struct {
	Weights Weights `json:"weights"`
	// API latency at or below OK is fine. At or above Bad it is fully severe.
	APILatencyOKMS  float64 `json:"apiLatencyOkMs"`
	APILatencyBadMS float64 `json:"apiLatencyBadMs"`
}

// DefaultConfig returns the built-in health rules.
func DefaultConfig() Config {
	return Config{
		Weights: Weights{
			PendingPods:     0.35,
			CrashLoop:       0.5,
			NodeNotReady:    0.6,
			APILatency:      0.3,
			FailedWorkloads: 0.5,
		},
		APILatencyOKMS:  250,
		APILatencyBadMS: 2500,
	}
}

func clamp01(x float64) float64 {
	if math.IsNaN(x) || x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// severity scores "count bad out of total" so that it works at any cluster
// size. One broken thing in a three-node homelab is a big deal by
// proportion. One broken thing among a thousand still registers by count,
// because count/(count+half) rises quickly and then levels off.
func severity(count, total int, half, fullFraction float64) float64 {
	if count <= 0 {
		return 0
	}
	c := float64(count)
	byCount := c / (c + half)
	byFraction := 0.0
	if total > 0 {
		byFraction = (c / float64(total)) / fullFraction
	}
	return clamp01(math.Max(byCount, byFraction))
}

// Evaluate computes the normalized state for a snapshot.
func Evaluate(s Snapshot, cfg Config) State {
	var st State

	if s.MetricsAvailable {
		if s.CPUCapacity > 0 {
			st.Load.CPU = clamp01(s.CPUUsage / s.CPUCapacity)
		}
		if s.MemoryCapacity > 0 {
			st.Load.Memory = clamp01(s.MemoryUsage / s.MemoryCapacity)
		}
	}
	// Network and disk need Prometheus, which arrives in a later phase.

	c := &st.Conditions
	c.PendingPods = severity(s.PodsPending, s.PodsTotal, 4, 0.3)
	c.CrashLoop = severity(s.PodsCrashLooping, s.PodsTotal, 3, 0.25)
	c.FailedWorkloads = math.Max(
		severity(s.DeploymentsUnavailable, s.DeploymentsTotal, 2, 0.34),
		severity(s.PodsFailing, s.PodsTotal, 4, 0.3),
	)
	// A node under memory, disk or PID pressure is still serving, so it
	// counts for half as much as one that is NotReady.
	c.NodeNotReady = math.Max(
		severity(s.NodesNotReady, s.NodesTotal, 2, 0.5),
		0.5*severity(s.NodesUnderPressure, s.NodesTotal, 2, 0.5),
	)

	ms := s.APILatencyMS
	if ms == 0 {
		ms = float64(s.APILatency) / float64(time.Millisecond)
	}
	if !s.APIReachable {
		c.APILatency = 1
	} else if span := cfg.APILatencyBadMS - cfg.APILatencyOKMS; span > 0 {
		c.APILatency = clamp01((ms - cfg.APILatencyOKMS) / span)
	}

	// Penalties combine like independent risks, not by simple addition.
	// One outage often shows up as several symptoms at once (a dead node
	// also means crash loops and unavailable deployments), and adding them
	// would hit zero too early and leave no room to hear things get worse.
	w := cfg.Weights
	remaining := 1.0
	for _, p := range []float64{
		w.PendingPods * c.PendingPods,
		w.CrashLoop * c.CrashLoop,
		w.NodeNotReady * c.NodeNotReady,
		w.APILatency * c.APILatency,
		w.FailedWorkloads * c.FailedWorkloads,
	} {
		remaining *= 1 - clamp01(p)
	}
	st.Health = math.Round(100 * remaining)

	// If the API server cannot be reached, everything else is stale. Do
	// not let old good news keep the score high.
	if !s.APIReachable && st.Health > 25 {
		st.Health = 25
	}
	return st
}
