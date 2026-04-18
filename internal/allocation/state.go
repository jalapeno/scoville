// Package allocation manages the lifecycle of allocated paths.
//
// Each path computed by the path engine is tracked as an AllocatedPath with
// one of four states:
//
//	FREE       — not allocated; eligible for any request
//	EXCLUSIVE  — allocated; removed from all subsequent path computation
//	SHARED     — allocated; eligible as fallback for sharing-tolerant requests
//	DRAINING   — workload signalled completion; paths are being released
//
// The state transitions are:
//
//	FREE → EXCLUSIVE  (request with SharingExclusive)
//	FREE → SHARED     (request with SharingAllowed)
//	EXCLUSIVE → DRAINING → FREE  (workload complete or lease expired)
//	SHARED    → DRAINING → FREE
package allocation

import (
	"fmt"
	"sync"
	"time"

	"github.com/jalapeno/scoville/internal/graph"
)

// PathState is the allocation lifecycle state of a single path.
type PathState string

const (
	StateFree      PathState = "free"
	StateExclusive PathState = "exclusive"
	StateShared    PathState = "shared"
	StateDraining  PathState = "draining"
)

// AllocatedPath associates a computed path with its current allocation state
// and the workload that owns it.
type AllocatedPath struct {
	Path       *graph.Path
	State      PathState
	WorkloadID string
	AllocatedAt time.Time
}

// WorkloadState tracks the lifecycle of a workload allocation request.
type WorkloadState string

const (
	WorkloadActive   WorkloadState = "active"
	WorkloadDraining WorkloadState = "draining"
	WorkloadComplete WorkloadState = "complete"
)

// DrainReason identifies why a workload entered the DRAINING state.
type DrainReason string

const (
	DrainReasonWorkloadComplete DrainReason = "workload_complete" // scheduler called /complete
	DrainReasonLeaseExpired     DrainReason = "lease_expired"      // heartbeat timeout
	DrainReasonTopologyChange   DrainReason = "topology_change"    // BMP withdrew a node/link on the path
	DrainReasonTopologyReplaced DrainReason = "topology_replaced"  // topology push replaced the graph
)

// WorkloadAllocation groups all paths allocated to a single workload request.
type WorkloadAllocation struct {
	WorkloadID   string
	TopologyID   string
	PathIDs      []string          // IDs of AllocatedPath entries
	Sharing      graph.SharingPolicy
	Disjointness graph.DisjointnessLevel
	State        WorkloadState
	CreatedAt    time.Time
	// LeaseExpires, if non-zero, triggers automatic release at that time.
	// The workload can extend the lease via a heartbeat call.
	LeaseExpires time.Time
	// LeaseDuration is the original requested lease length. Zero means no
	// lease. Stored so heartbeats can extend by the same interval each time.
	LeaseDuration time.Duration `json:"-"`

	// DrainReason is set when the workload enters DRAINING, explaining why.
	// Empty while the workload is active.
	DrainReason DrainReason

	// stateChanged is closed each time the workload's state transitions, then
	// replaced with a new channel for the next transition. Listeners receive
	// a broadcast wake-up by selecting on the channel value captured while
	// holding the table's read lock via Subscribe.
	stateChanged chan struct{}

	// leaseTimer fires when the lease expires; drainTimer fires after the
	// drain grace period. Both are managed exclusively by timer.go.
	leaseTimer *time.Timer
	drainTimer *time.Timer
}

// notifyStateChanged broadcasts a state-change wake-up to all current
// subscribers by closing the existing stateChanged channel and creating a
// fresh one for the next transition. Must be called with t.mu held.
//
// The nil guard handles workloads created via struct literals (e.g. in tests
// that call DrainWorkload directly rather than through AllocatePaths).
func (wl *WorkloadAllocation) notifyStateChanged() {
	if wl.stateChanged == nil {
		return
	}
	old := wl.stateChanged
	wl.stateChanged = make(chan struct{})
	close(old)
}

// ReleaseCallback is invoked after a workload transitions to the COMPLETE state
// and its paths have been returned to FREE. The callback receives the workload
// ID and the path objects that were allocated, allowing the southbound driver
// to clean up forwarding state.
//
// Implementations must not call back into the Table (the lock is not held when
// the callback fires, but re-entry would still be risky).
type ReleaseCallback func(workloadID string, paths []*graph.Path)

// Table is the allocation state table for a single topology. It is safe for
// concurrent use.
type Table struct {
	mu         sync.RWMutex
	topologyID string
	paths      map[string]*AllocatedPath
	workloads  map[string]*WorkloadAllocation

	// onRelease, if non-nil, is called after each workload reaches the COMPLETE
	// state. Set via SetOnRelease; protected by mu.
	onRelease ReleaseCallback
}

// NewTable creates an empty allocation table for the given topology.
func NewTable(topologyID string) *Table {
	return &Table{
		topologyID: topologyID,
		paths:      make(map[string]*AllocatedPath),
		workloads:  make(map[string]*WorkloadAllocation),
	}
}

// SetOnRelease registers fn as the release callback. It replaces any
// previously registered callback. Pass nil to disable.
func (t *Table) SetOnRelease(fn ReleaseCallback) {
	t.mu.Lock()
	t.onRelease = fn
	t.mu.Unlock()
}

// RegisterPath adds a path to the table in the FREE state. This is called by
// the path engine when a new topology is loaded.
func (t *Table) RegisterPath(p *graph.Path) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.paths[p.ID]; !exists {
		t.paths[p.ID] = &AllocatedPath{Path: p, State: StateFree}
	}
}

// AllocatePaths transitions a set of paths to EXCLUSIVE or SHARED on behalf
// of a workload. It returns an error if any path is not in the FREE state
// (or SHARED state when sharing is allowed).
func (t *Table) AllocatePaths(workload *WorkloadAllocation, pathIDs []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	targetState := StateExclusive
	if workload.Sharing == graph.SharingAllowed {
		targetState = StateShared
	}

	// Validate all paths before modifying any.
	for _, id := range pathIDs {
		ap, ok := t.paths[id]
		if !ok {
			return fmt.Errorf("path %q not found in allocation table", id)
		}
		switch ap.State {
		case StateFree:
			// always ok
		case StateShared:
			if workload.Sharing != graph.SharingAllowed {
				return fmt.Errorf("path %q is SHARED but request requires exclusive allocation", id)
			}
		default:
			return fmt.Errorf("path %q is in state %q and cannot be allocated", id, ap.State)
		}
	}

	now := time.Now()
	for _, id := range pathIDs {
		ap := t.paths[id]
		ap.State = targetState
		ap.WorkloadID = workload.WorkloadID
		ap.AllocatedAt = now
	}

	workload.PathIDs = pathIDs
	workload.State = WorkloadActive
	workload.CreatedAt = now
	workload.stateChanged = make(chan struct{})
	t.workloads[workload.WorkloadID] = workload
	t.scheduleLeaseTimer(workload)
	return nil
}

// DrainWorkload transitions a workload and its paths to the DRAINING state.
// Called when the workload scheduler signals completion via the /complete
// endpoint. Use drain() directly for internal callers that supply a reason.
func (t *Table) DrainWorkload(workloadID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	wl, ok := t.workloads[workloadID]
	if !ok {
		return fmt.Errorf("workload %q not found", workloadID)
	}
	t.drain(wl, DrainReasonWorkloadComplete)
	return nil
}

// drain transitions wl to DRAINING with the supplied reason and notifies
// subscribers. It does NOT start the drain timer — callers that want the
// automatic free-after-grace-period must call scheduleDrainTimer separately
// (or use StartDrainTimer from the API layer).
// Must be called with t.mu held. wl must be in WorkloadActive state.
func (t *Table) drain(wl *WorkloadAllocation, reason DrainReason) {
	wl.State = WorkloadDraining
	wl.DrainReason = reason
	for _, id := range wl.PathIDs {
		if ap, ok := t.paths[id]; ok {
			ap.State = StateDraining
		}
	}
	wl.notifyStateChanged()
}

// ReleaseWorkload transitions a workload's paths back to FREE and marks the
// workload as complete. It is typically called after the DRAINING grace period.
func (t *Table) ReleaseWorkload(workloadID string) error {
	t.mu.Lock()

	wl, ok := t.workloads[workloadID]
	if !ok {
		t.mu.Unlock()
		return fmt.Errorf("workload %q not found", workloadID)
	}

	// Collect path objects before clearing WorkloadID so the release callback
	// can pass them to the southbound driver for cleanup.
	released := make([]*graph.Path, 0, len(wl.PathIDs))
	for _, id := range wl.PathIDs {
		if ap, ok := t.paths[id]; ok {
			released = append(released, ap.Path)
			ap.State = StateFree
			ap.WorkloadID = ""
		}
	}
	wl.State = WorkloadComplete
	wl.notifyStateChanged()
	fn := t.onRelease
	t.mu.Unlock()

	if fn != nil {
		fn(workloadID, released)
	}
	return nil
}

// Subscribe returns a channel that will be closed the next time the workload's
// state changes. The caller should capture the channel value while the table
// lock is not held, then select on it outside any lock. If the workload is not
// found, Subscribe returns (nil, false).
//
// Pattern:
//
//	ch, ok := table.Subscribe(workloadID)
//	if !ok { return }
//	select {
//	case <-ch:        // state changed — re-read with GetWorkload
//	case <-ctx.Done(): // client disconnected
//	}
func (t *Table) Subscribe(workloadID string) (<-chan struct{}, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	wl, ok := t.workloads[workloadID]
	if !ok || wl.stateChanged == nil {
		return nil, false
	}
	return wl.stateChanged, true
}

// FreePaths returns all paths in the FREE state.
func (t *Table) FreePaths() []*AllocatedPath {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []*AllocatedPath
	for _, ap := range t.paths {
		if ap.State == StateFree {
			out = append(out, ap)
		}
	}
	return out
}

// SharedPaths returns all paths in the SHARED state (fallback candidates for
// sharing-tolerant requests).
func (t *Table) SharedPaths() []*AllocatedPath {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []*AllocatedPath
	for _, ap := range t.paths {
		if ap.State == StateShared {
			out = append(out, ap)
		}
	}
	return out
}

// PathStateOf returns the current state of a path by ID.
func (t *Table) PathStateOf(pathID string) (PathState, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ap, ok := t.paths[pathID]
	if !ok {
		return "", false
	}
	return ap.State, true
}

// GetWorkload returns the WorkloadAllocation for the given ID.
func (t *Table) GetWorkload(workloadID string) (*WorkloadAllocation, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	wl, ok := t.workloads[workloadID]
	return wl, ok
}

// WorkloadPaths returns the graph.Path objects for every path allocated to the
// given workload. Paths whose IDs are no longer present in the table (e.g.
// because the topology was replaced) are silently skipped.
func (t *Table) WorkloadPaths(workloadID string) []*graph.Path {
	t.mu.RLock()
	defer t.mu.RUnlock()
	wl, ok := t.workloads[workloadID]
	if !ok {
		return nil
	}
	out := make([]*graph.Path, 0, len(wl.PathIDs))
	for _, id := range wl.PathIDs {
		if ap, ok := t.paths[id]; ok && ap.Path != nil {
			out = append(out, ap.Path)
		}
	}
	return out
}

// Snapshot returns a summary of all path states for observability.
func (t *Table) Snapshot() TableSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	s := TableSnapshot{TopologyID: t.topologyID}
	for _, ap := range t.paths {
		s.Total++
		switch ap.State {
		case StateFree:
			s.Free++
		case StateExclusive:
			s.Exclusive++
		case StateShared:
			s.Shared++
		case StateDraining:
			s.Draining++
		}
	}
	s.ActiveWorkloads = 0
	for _, wl := range t.workloads {
		if wl.State == WorkloadActive {
			s.ActiveWorkloads++
		}
	}
	return s
}

// TableSnapshot is a point-in-time summary of an allocation table.
type TableSnapshot struct {
	TopologyID      string `json:"topology_id"`
	Total           int    `json:"total"`
	Free            int    `json:"free"`
	Exclusive       int    `json:"exclusive"`
	Shared          int    `json:"shared"`
	Draining        int    `json:"draining"`
	ActiveWorkloads int    `json:"active_workloads"`
}
