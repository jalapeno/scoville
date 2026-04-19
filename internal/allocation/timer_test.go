package allocation

import (
	"testing"
	"time"

	"github.com/jalapeno/syd/internal/graph"
)

// allocateWithLease is a test helper that registers a path, creates a workload
// with the given lease duration, and calls AllocatePaths.
func allocateWithLease(t *testing.T, tbl *Table, workloadID string, lease time.Duration) {
	t.Helper()
	tbl.RegisterPath(makePath("p-lease"))
	wl := &WorkloadAllocation{
		WorkloadID:    workloadID,
		Sharing:       graph.SharingExclusive,
		LeaseDuration: lease,
		LeaseExpires:  time.Now().Add(lease),
	}
	if err := tbl.AllocatePaths(wl, []string{"p-lease"}); err != nil {
		t.Fatalf("allocate: %v", err)
	}
}

// --- ExtendLease -------------------------------------------------------------

func TestExtendLease_ResetsExpiry(t *testing.T) {
	tbl := makeTable()
	allocateWithLease(t, tbl, "wl-1", time.Hour)

	wl, _ := tbl.GetWorkload("wl-1")
	before := wl.LeaseExpires

	if err := tbl.ExtendLease("wl-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wl, _ = tbl.GetWorkload("wl-1")
	after := wl.LeaseExpires

	if !after.After(before) {
		t.Errorf("extended expiry should be later than original: before=%s after=%s", before, after)
	}
}

func TestExtendLease_NotFound(t *testing.T) {
	tbl := makeTable()
	if err := tbl.ExtendLease("no-such-workload"); err == nil {
		t.Error("expected error for unknown workload, got nil")
	}
}

func TestExtendLease_NotActive(t *testing.T) {
	// Draining workload cannot have its lease extended.
	tbl := makeTable()
	allocateWithLease(t, tbl, "wl-1", time.Hour)
	_ = tbl.DrainWorkload("wl-1")

	if err := tbl.ExtendLease("wl-1"); err == nil {
		t.Error("expected error when extending lease of draining workload, got nil")
	}
}

func TestExtendLease_NoLease(t *testing.T) {
	// Workload without a lease cannot be extended.
	tbl := makeTable()
	tbl.RegisterPath(makePath("p-nolease"))
	wl := makeWorkload("wl-nolease", graph.SharingExclusive)
	// LeaseDuration is zero — no lease.
	_ = tbl.AllocatePaths(wl, []string{"p-nolease"})

	if err := tbl.ExtendLease("wl-nolease"); err == nil {
		t.Error("expected error when extending lease of workload without a lease, got nil")
	}
}

// --- StartDrainTimer ---------------------------------------------------------

func TestStartDrainTimer_RequiresDrainingState(t *testing.T) {
	// StartDrainTimer must fail if the workload is still active.
	tbl := makeTable()
	tbl.RegisterPath(makePath("p-drain"))
	wl := makeWorkload("wl-1", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wl, []string{"p-drain"})

	if err := tbl.StartDrainTimer("wl-1"); err == nil {
		t.Error("expected error when starting drain timer on active workload, got nil")
	}
}

func TestStartDrainTimer_NotFound(t *testing.T) {
	tbl := makeTable()
	if err := tbl.StartDrainTimer("no-such-workload"); err == nil {
		t.Error("expected error for unknown workload, got nil")
	}
}

func TestStartDrainTimer_Succeeds(t *testing.T) {
	// StartDrainTimer must succeed when the workload is in DRAINING state.
	tbl := makeTable()
	tbl.RegisterPath(makePath("p-drain"))
	wl := makeWorkload("wl-1", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wl, []string{"p-drain"})
	_ = tbl.DrainWorkload("wl-1")

	if err := tbl.StartDrainTimer("wl-1"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// Clean up: stop the drain timer so it doesn't fire after the test ends.
	tbl.mu.Lock()
	if stored := tbl.workloads["wl-1"]; stored != nil && stored.drainTimer != nil {
		stored.drainTimer.Stop()
	}
	tbl.mu.Unlock()
}

// --- Lease expiry (immediate fire) -------------------------------------------

func TestLeaseExpiry_ImmediateFire_TransitionsToDraining(t *testing.T) {
	// Setting LeaseExpires in the past causes scheduleLeaseTimer to fire
	// expireLease in a goroutine immediately (without waiting for a real timer).
	// The workload should transition to DRAINING within a short wait.
	tbl := makeTable()
	tbl.RegisterPath(makePath("p-exp"))

	wl := &WorkloadAllocation{
		WorkloadID:    "wl-exp",
		Sharing:       graph.SharingExclusive,
		LeaseDuration: time.Second,
		LeaseExpires:  time.Now().Add(-time.Millisecond), // already expired
	}
	if err := tbl.AllocatePaths(wl, []string{"p-exp"}); err != nil {
		t.Fatalf("allocate: %v", err)
	}

	// Give the goroutine time to acquire the lock and run expireLease.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		stored, _ := tbl.GetWorkload("wl-exp")
		if stored != nil && stored.State == WorkloadDraining {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	stored, _ := tbl.GetWorkload("wl-exp")
	if stored == nil || stored.State != WorkloadDraining {
		t.Fatalf("want workload DRAINING after immediate expiry, got %v", stored)
	}

	s, _ := tbl.PathStateOf("p-exp")
	if s != StateDraining {
		t.Errorf("want path DRAINING after lease expiry, got %s", s)
	}

	// Stop the drain timer that expireLease started so it doesn't fire 30s later.
	tbl.mu.Lock()
	if w := tbl.workloads["wl-exp"]; w != nil && w.drainTimer != nil {
		w.drainTimer.Stop()
	}
	tbl.mu.Unlock()
}

func TestLeaseExpiry_ShortTimer_TransitionsToDraining(t *testing.T) {
	// Allocate with a real but very short lease (50 ms) and wait for the timer
	// to fire naturally via time.AfterFunc.
	tbl := makeTable()
	tbl.RegisterPath(makePath("p-short"))

	const leaseDur = 50 * time.Millisecond
	wl := &WorkloadAllocation{
		WorkloadID:    "wl-short",
		Sharing:       graph.SharingExclusive,
		LeaseDuration: leaseDur,
		LeaseExpires:  time.Now().Add(leaseDur),
	}
	if err := tbl.AllocatePaths(wl, []string{"p-short"}); err != nil {
		t.Fatalf("allocate: %v", err)
	}

	// Poll until DRAINING or timeout.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		stored, _ := tbl.GetWorkload("wl-short")
		if stored != nil && stored.State == WorkloadDraining {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	stored, _ := tbl.GetWorkload("wl-short")
	if stored == nil || stored.State != WorkloadDraining {
		t.Fatalf("want workload DRAINING after 50ms lease, got %v", stored)
	}

	// Stop the 30-second drain timer before the test exits.
	tbl.mu.Lock()
	if w := tbl.workloads["wl-short"]; w != nil && w.drainTimer != nil {
		w.drainTimer.Stop()
	}
	tbl.mu.Unlock()
}
