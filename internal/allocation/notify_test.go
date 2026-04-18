package allocation

import (
	"testing"
	"time"

	"github.com/jalapeno/scoville/internal/graph"
)

// --- DrainReason propagation -------------------------------------------------

func TestDrainReason_WorkloadComplete(t *testing.T) {
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = table.AllocatePaths(wl, []string{"p1"})

	_ = table.DrainWorkload("wl-1")

	got, _ := table.GetWorkload("wl-1")
	if got.DrainReason != DrainReasonWorkloadComplete {
		t.Errorf("DrainReason = %q, want %q", got.DrainReason, DrainReasonWorkloadComplete)
	}
}

func TestDrainReason_TopologyChange(t *testing.T) {
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1", VertexIDs: []string{"r1"}})
	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = table.AllocatePaths(wl, []string{"p1"})

	table.InvalidateElement("r1")

	got, _ := table.GetWorkload("wl-1")
	if got.DrainReason != DrainReasonTopologyChange {
		t.Errorf("DrainReason = %q, want %q", got.DrainReason, DrainReasonTopologyChange)
	}
}

func TestDrainReason_TopologyReplaced(t *testing.T) {
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = table.AllocatePaths(wl, []string{"p1"})

	table.DrainAll()

	got, _ := table.GetWorkload("wl-1")
	if got.DrainReason != DrainReasonTopologyReplaced {
		t.Errorf("DrainReason = %q, want %q", got.DrainReason, DrainReasonTopologyReplaced)
	}
}

func TestDrainReason_LeaseExpired(t *testing.T) {
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	wl := &WorkloadAllocation{
		WorkloadID:    "wl-1",
		Sharing:       graph.SharingExclusive,
		LeaseDuration: 1 * time.Millisecond,
		LeaseExpires:  time.Now().Add(-time.Millisecond), // already expired
	}
	_ = table.AllocatePaths(wl, []string{"p1"})

	// scheduleLeaseTimer fires expireLease immediately (LeaseExpires in past).
	// Poll until state changes.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		got, _ := table.GetWorkload("wl-1")
		if got.State == WorkloadDraining {
			if got.DrainReason != DrainReasonLeaseExpired {
				t.Errorf("DrainReason = %q, want %q", got.DrainReason, DrainReasonLeaseExpired)
			}
			// Stop drain timer to avoid goroutine leak.
			table.mu.Lock()
			if got.drainTimer != nil {
				got.drainTimer.Stop()
			}
			table.mu.Unlock()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("workload did not drain within 200ms after expired lease")
}

// --- Subscribe / stateChanged channel ----------------------------------------

func TestSubscribe_NotFound(t *testing.T) {
	table := NewTable("topo")
	_, ok := table.Subscribe("nonexistent")
	if ok {
		t.Error("Subscribe should return ok=false for unknown workload")
	}
}

func TestSubscribe_FiresOnDrain(t *testing.T) {
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = table.AllocatePaths(wl, []string{"p1"})

	ch, ok := table.Subscribe("wl-1")
	if !ok {
		t.Fatal("Subscribe returned ok=false for active workload")
	}

	// Drain in a goroutine; the channel should close.
	go func() { _ = table.DrainWorkload("wl-1") }()

	select {
	case <-ch:
		// Good — channel closed as expected.
	case <-time.After(200 * time.Millisecond):
		t.Error("Subscribe channel not closed within 200ms after DrainWorkload")
	}

	got, _ := table.GetWorkload("wl-1")
	if got.State != WorkloadDraining {
		t.Errorf("state = %q, want draining", got.State)
	}
}

func TestSubscribe_FiresOnRelease(t *testing.T) {
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = table.AllocatePaths(wl, []string{"p1"})
	_ = table.DrainWorkload("wl-1")

	// Subscribe after drain — should fire on the next transition (release).
	ch, ok := table.Subscribe("wl-1")
	if !ok {
		t.Fatal("Subscribe returned ok=false for draining workload")
	}

	go func() { _ = table.ReleaseWorkload("wl-1") }()

	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
		t.Error("Subscribe channel not closed within 200ms after ReleaseWorkload")
	}

	got, _ := table.GetWorkload("wl-1")
	if got.State != WorkloadComplete {
		t.Errorf("state = %q, want complete", got.State)
	}
}

func TestSubscribe_MultipleListeners(t *testing.T) {
	// Closing the channel broadcasts to all waiters simultaneously.
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = table.AllocatePaths(wl, []string{"p1"})

	// Three separate subscribers each capture the same channel.
	const n = 3
	channels := make([]<-chan struct{}, n)
	for i := range channels {
		ch, ok := table.Subscribe("wl-1")
		if !ok {
			t.Fatalf("Subscribe[%d] returned ok=false", i)
		}
		channels[i] = ch
	}

	go func() { _ = table.DrainWorkload("wl-1") }()

	for i, ch := range channels {
		select {
		case <-ch:
		case <-time.After(200 * time.Millisecond):
			t.Errorf("listener %d not woken within 200ms", i)
		}
	}
}

func TestSubscribe_SequentialTransitions(t *testing.T) {
	// Verify we get two distinct wakeups: active→draining and draining→complete.
	table := NewTable("topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = table.AllocatePaths(wl, []string{"p1"})

	// --- First transition: active → draining ---
	ch1, _ := table.Subscribe("wl-1")
	_ = table.DrainWorkload("wl-1")
	select {
	case <-ch1:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("first transition (drain) not signalled")
	}

	// --- Second transition: draining → complete ---
	ch2, _ := table.Subscribe("wl-1")
	if ch2 == ch1 {
		t.Error("Subscribe after first transition returned the same channel (expected a fresh one)")
	}
	_ = table.ReleaseWorkload("wl-1")
	select {
	case <-ch2:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("second transition (release) not signalled")
	}
}
