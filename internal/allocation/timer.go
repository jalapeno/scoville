package allocation

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jalapeno/syd/internal/graph"
)

// DrainGracePeriod is the time a workload spends in DRAINING state before its
// paths are returned to FREE. This gives in-flight packets time to clear before
// the capacity is reused by a subsequent allocation.
const DrainGracePeriod = 30 * time.Second

// StartDrainTimer starts the drain-to-release timer for a workload that is
// already in the DRAINING state. Called by the API handler after a graceful
// workload-complete request (Immediate=false).
func (t *Table) StartDrainTimer(workloadID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	wl, ok := t.workloads[workloadID]
	if !ok {
		return fmt.Errorf("workload %q not found", workloadID)
	}
	if wl.State != WorkloadDraining {
		return fmt.Errorf("workload %q is in state %q, not draining", workloadID, wl.State)
	}
	t.scheduleDrainTimer(wl)
	return nil
}

// ExtendLease resets a workload's lease timer, extending the deadline by the
// workload's original LeaseDuration from now. Returns an error if the workload
// is not active or was created without a lease.
func (t *Table) ExtendLease(workloadID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	wl, ok := t.workloads[workloadID]
	if !ok {
		return fmt.Errorf("workload %q not found", workloadID)
	}
	if wl.State != WorkloadActive {
		return fmt.Errorf("workload %q is not active (state: %s)", workloadID, wl.State)
	}
	if wl.LeaseDuration == 0 {
		return fmt.Errorf("workload %q has no lease", workloadID)
	}
	if wl.leaseTimer != nil {
		wl.leaseTimer.Stop()
	}
	wl.LeaseExpires = time.Now().Add(wl.LeaseDuration)
	wl.leaseTimer = time.AfterFunc(wl.LeaseDuration, func() {
		t.expireLease(workloadID)
	})
	return nil
}

// --- internal helpers --------------------------------------------------------

// scheduleLeaseTimer starts the lease expiry timer for a workload. It is a
// no-op when LeaseExpires is zero. Must be called with t.mu held.
func (t *Table) scheduleLeaseTimer(wl *WorkloadAllocation) {
	if wl.LeaseExpires.IsZero() {
		return
	}
	ttl := time.Until(wl.LeaseExpires)
	if ttl <= 0 {
		// Already expired at the moment of allocation; drain immediately in a
		// goroutine so we don't block AllocatePaths while holding the lock.
		go t.expireLease(wl.WorkloadID)
		return
	}
	id := wl.WorkloadID
	wl.leaseTimer = time.AfterFunc(ttl, func() {
		t.expireLease(id)
	})
}

// expireLease is the lease timer callback. It drains the workload and starts
// the drain grace timer so paths are freed after DrainGracePeriod.
func (t *Table) expireLease(workloadID string) {
	t.mu.Lock()
	wl, ok := t.workloads[workloadID]
	if !ok || wl.State != WorkloadActive {
		t.mu.Unlock()
		return
	}
	t.drain(wl, DrainReasonLeaseExpired)
	wl.leaseTimer = nil
	t.scheduleDrainTimer(wl)
	t.mu.Unlock()

	slog.Default().Info("workload lease expired; draining",
		"workload_id", workloadID,
		"drain_grace", DrainGracePeriod,
	)
}

// scheduleDrainTimer starts (or restarts) the drain-to-release timer.
// Must be called with t.mu held.
func (t *Table) scheduleDrainTimer(wl *WorkloadAllocation) {
	if wl.drainTimer != nil {
		wl.drainTimer.Stop()
	}
	id := wl.WorkloadID
	wl.drainTimer = time.AfterFunc(DrainGracePeriod, func() {
		t.releaseDrained(id)
	})
}

// releaseDrained is the drain-timer callback. It transitions all of the
// workload's paths back to FREE and marks the workload complete.
func (t *Table) releaseDrained(workloadID string) {
	t.mu.Lock()
	wl, ok := t.workloads[workloadID]
	if !ok || wl.State != WorkloadDraining {
		t.mu.Unlock()
		return
	}

	released := make([]*graph.Path, 0, len(wl.PathIDs))
	for _, id := range wl.PathIDs {
		if ap, ok := t.paths[id]; ok {
			released = append(released, ap.Path)
			ap.State = StateFree
			ap.WorkloadID = ""
		}
	}
	wl.State = WorkloadComplete
	wl.drainTimer = nil
	wl.notifyStateChanged()
	fn := t.onRelease
	t.mu.Unlock()

	slog.Default().Info("workload paths released after drain grace period",
		"workload_id", workloadID,
	)
	if fn != nil {
		fn(workloadID, released)
	}
}
