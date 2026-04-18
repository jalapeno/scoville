package allocation

import "log/slog"

// InvalidateElement immediately drains all active workloads whose computed
// paths traverse the given vertex or edge ID. This is called when a topology
// element is withdrawn — for example, when a BMP del message removes a link
// or node — so that the workload scheduler can detect the topology change and
// request replacement paths.
//
// Affected workloads move to DRAINING state and their paths are released after
// DrainGracePeriod, giving in-flight packets time to clear before capacity is
// reused by a subsequent allocation.
func (t *Table) InvalidateElement(elementID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, wl := range t.workloads {
		if wl.State != WorkloadActive {
			continue
		}
		if !t.pathsTouchElement(wl, elementID) {
			continue
		}
		t.drain(wl, DrainReasonTopologyChange)
		t.scheduleDrainTimer(wl)
		slog.Default().Info("workload invalidated by topology change",
			"workload_id", wl.WorkloadID,
			"topology_id", t.topologyID,
			"element_id", elementID,
		)
	}
}

// DrainAll immediately drains all active workloads. Called when a topology
// is replaced by a push so that existing allocations do not remain active
// against a graph that no longer exists. The drain grace period still applies,
// giving in-flight traffic time to clear before paths are released.
func (t *Table) DrainAll() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, wl := range t.workloads {
		if wl.State != WorkloadActive {
			continue
		}
		t.drain(wl, DrainReasonTopologyReplaced)
		t.scheduleDrainTimer(wl)
	}
	if len(t.workloads) > 0 {
		slog.Default().Info("all workloads drained due to topology replacement",
			"topology_id", t.topologyID,
		)
	}
}

// pathsTouchElement reports whether any path in wl's allocation traverses
// elementID (matched against VertexIDs and EdgeIDs). Must be called with
// t.mu held.
func (t *Table) pathsTouchElement(wl *WorkloadAllocation, elementID string) bool {
	for _, pid := range wl.PathIDs {
		ap, ok := t.paths[pid]
		if !ok {
			continue
		}
		for _, vid := range ap.Path.VertexIDs {
			if vid == elementID {
				return true
			}
		}
		for _, eid := range ap.Path.EdgeIDs {
			if eid == elementID {
				return true
			}
		}
	}
	return false
}
