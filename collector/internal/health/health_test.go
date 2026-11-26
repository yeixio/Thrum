package health

import (
	"testing"
	"time"
)

func healthy() Snapshot {
	return Snapshot{
		NodesTotal: 3, PodsTotal: 40, DeploymentsTotal: 12,
		MetricsAvailable: true, CPUUsage: 1.2, CPUCapacity: 12, MemoryUsage: 8e9, MemoryCapacity: 48e9,
		APIReachable: true, APILatency: 20 * time.Millisecond,
	}
}

func TestHealthyClusterScores100(t *testing.T) {
	st := Evaluate(healthy(), DefaultConfig())
	if st.Health != 100 {
		t.Fatalf("health = %v, want 100", st.Health)
	}
	if st.Conditions != (Conditions{}) {
		t.Fatalf("conditions = %+v, want all zero", st.Conditions)
	}
	if st.Load.CPU < 0.099 || st.Load.CPU > 0.101 {
		t.Fatalf("cpu = %v, want 0.1", st.Load.CPU)
	}
}

func TestBusyIsNotUnhealthy(t *testing.T) {
	s := healthy()
	s.CPUUsage = 11.4
	s.MemoryUsage = 44e9
	st := Evaluate(s, DefaultConfig())
	if st.Health != 100 {
		t.Fatalf("95%% CPU with nothing broken: health = %v, want 100", st.Health)
	}
	if st.Load.CPU < 0.9 {
		t.Fatalf("cpu = %v, want >= 0.9", st.Load.CPU)
	}
}

func TestQuietButFailingIsUnhealthy(t *testing.T) {
	s := healthy()
	s.CPUUsage = 0.5
	s.PodsCrashLooping = 6
	s.DeploymentsUnavailable = 2
	st := Evaluate(s, DefaultConfig())
	if st.Health > 60 {
		t.Fatalf("health = %v, want <= 60", st.Health)
	}
	if st.Conditions.CrashLoop < 0.5 || st.Conditions.FailedWorkloads < 0.4 {
		t.Fatalf("conditions = %+v", st.Conditions)
	}
}

func TestSeverityWorksAtAnyClusterSize(t *testing.T) {
	// One of three nodes down is serious by proportion.
	small := healthy()
	small.NodesNotReady = 1
	if got := Evaluate(small, DefaultConfig()).Conditions.NodeNotReady; got < 0.6 {
		t.Fatalf("1 of 3 nodes: severity = %v, want >= 0.6", got)
	}
	// One of 500 still registers, but mildly.
	big := healthy()
	big.NodesTotal = 500
	big.NodesNotReady = 1
	got := Evaluate(big, DefaultConfig()).Conditions.NodeNotReady
	if got < 0.2 || got > 0.5 {
		t.Fatalf("1 of 500 nodes: severity = %v, want between 0.2 and 0.5", got)
	}
}

func TestSeverityIsMonotonic(t *testing.T) {
	prev := -1.0
	for n := 0; n <= 40; n++ {
		s := healthy()
		s.PodsCrashLooping = n
		got := Evaluate(s, DefaultConfig()).Conditions.CrashLoop
		if got < prev || got > 1 {
			t.Fatalf("crashLoop severity not monotonic or out of range at n=%d: %v after %v", n, got, prev)
		}
		prev = got
	}
}

func TestPressureCountsHalfAsMuchAsNotReady(t *testing.T) {
	a, b := healthy(), healthy()
	a.NodesNotReady = 1
	b.NodesUnderPressure = 1
	na := Evaluate(a, DefaultConfig()).Conditions.NodeNotReady
	nb := Evaluate(b, DefaultConfig()).Conditions.NodeNotReady
	if nb != na/2 {
		t.Fatalf("pressure = %v, notReady = %v, want half", nb, na)
	}
}

func TestAPILatency(t *testing.T) {
	cases := []struct {
		ms   int
		want float64
	}{{50, 0}, {250, 0}, {1375, 0.5}, {2500, 1}, {9000, 1}}
	for _, c := range cases {
		s := healthy()
		s.APILatency = time.Duration(c.ms) * time.Millisecond
		if got := Evaluate(s, DefaultConfig()).Conditions.APILatency; got != c.want {
			t.Errorf("%dms: severity = %v, want %v", c.ms, got, c.want)
		}
	}
}

func TestUnreachableAPICapsHealth(t *testing.T) {
	s := healthy()
	s.APIReachable = false
	st := Evaluate(s, DefaultConfig())
	if st.Health > 25 || st.Conditions.APILatency != 1 {
		t.Fatalf("unreachable: %+v", st)
	}
}

func TestEverythingBrokenIsCriticalAndStaysInRange(t *testing.T) {
	s := Snapshot{
		NodesTotal: 3, NodesNotReady: 3, PodsTotal: 10, PodsPending: 10, PodsCrashLooping: 10, PodsFailing: 10,
		DeploymentsTotal: 4, DeploymentsUnavailable: 4, APIReachable: true, APILatency: 10 * time.Second,
	}
	st := Evaluate(s, DefaultConfig())
	if st.Health < 0 || st.Health > 10 {
		t.Fatalf("health = %v, want between 0 and 10", st.Health)
	}
}

func TestNoMetricsServerMeansZeroLoadNotAnError(t *testing.T) {
	s := healthy()
	s.MetricsAvailable = false
	st := Evaluate(s, DefaultConfig())
	if st.Load.CPU != 0 || st.Load.Memory != 0 || st.Health != 100 {
		t.Fatalf("%+v", st)
	}
}

func TestCustomWeights(t *testing.T) {
	s := healthy()
	s.PodsPending = 40
	cfg := DefaultConfig()
	cfg.Weights.PendingPods = 0
	if got := Evaluate(s, cfg).Health; got != 100 {
		t.Fatalf("with pending weight 0: health = %v, want 100", got)
	}
}

func TestOneOutageWithSeveralSymptomsLeavesHeadroom(t *testing.T) {
	// A dead node that also takes a service down is bad, but it should
	// still score better than the whole cluster being broken.
	s := healthy()
	s.NodesNotReady = 1
	s.PodsCrashLooping = 3
	s.DeploymentsUnavailable = 1
	st := Evaluate(s, DefaultConfig())
	if st.Health < 10 || st.Health > 40 {
		t.Fatalf("health = %v, want between 10 and 40", st.Health)
	}
}
