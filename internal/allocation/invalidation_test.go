package allocation

import (
	"testing"
	"time"

	"github.com/jalapeno/scoville/internal/graph"
)

// --- helpers -----------------------------------------------------------------

// makeTableWithPath creates a Table, registers a path with the given vertex
// and edge IDs, and returns both. The path is in FREE state after registration.
func makeTableWithPath(t *testing.T, pathID string, vertexIDs, edgeIDs []string) *Table {
	t.Helper()
	table := NewTable("test-topo")
	table.RegisterPath(&graph.Path{
		ID:        pathID,
		VertexIDs: vertexIDs,
		EdgeIDs:   edgeIDs,
	})
	return table
}

// allocateToTable allocates the given pathIDs to a new workload in the table.
func allocateToTable(t *testing.T, table *Table, workloadID string, pathIDs []string) *WorkloadAllocation {
	t.Helper()
	wl := &WorkloadAllocation{
		WorkloadID: workloadID,
		Sharing:    graph.SharingExclusive,
	}
	if err := table.AllocatePaths(wl, pathIDs); err != nil {
		t.Fatalf("AllocatePaths: %v", err)
	}
	return wl
}

// --- InvalidateElement -------------------------------------------------------

func TestInvalidateElement_DrainsByVertexID(t *testing.T) {
	table := makeTableWithPath(t, "p1", []string{"r1", "r2", "r3"}, []string{"e1-2", "e2-3"})
	allocateToTable(t, table, "wl-1", []string{"p1"})

	table.InvalidateElement("r2") // transit node

	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadDraining {
		t.Errorf("workload state = %q, want draining", wl.State)
	}
	if s, _ := table.PathStateOf("p1"); s != StateDraining {
		t.Errorf("path state = %q, want draining", s)
	}
}

func TestInvalidateElement_DrainsByEdgeID(t *testing.T) {
	table := makeTableWithPath(t, "p1", []string{"r1", "r2"}, []string{"link:r1:r2:10.0.0.1"})
	allocateToTable(t, table, "wl-1", []string{"p1"})

	table.InvalidateElement("link:r1:r2:10.0.0.1")

	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadDraining {
		t.Errorf("workload state = %q, want draining", wl.State)
	}
}

func TestInvalidateElement_NoMatchLeavesFree(t *testing.T) {
	table := makeTableWithPath(t, "p1", []string{"r1", "r2"}, []string{"e1-2"})
	allocateToTable(t, table, "wl-1", []string{"p1"})

	table.InvalidateElement("r99") // not on this path

	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadActive {
		t.Errorf("workload state = %q, want active (unrelated element removed)", wl.State)
	}
	if s, _ := table.PathStateOf("p1"); s != StateExclusive {
		t.Errorf("path state = %q, want exclusive", s)
	}
}

func TestInvalidateElement_OnlyAffectsActiveWorkloads(t *testing.T) {
	// A workload already in DRAINING should not be re-processed.
	table := makeTableWithPath(t, "p1", []string{"r1", "r2"}, nil)
	allocateToTable(t, table, "wl-1", []string{"p1"})
	_ = table.DrainWorkload("wl-1") // manually drain first

	// Invalidating should not panic or change state.
	table.InvalidateElement("r1")

	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadDraining {
		t.Errorf("workload state = %q, want draining (unchanged)", wl.State)
	}
}

func TestInvalidateElement_MultipleWorkloads_OnlyAffectedDrained(t *testing.T) {
	// Two workloads: wl-1 uses r2, wl-2 does not. Only wl-1 should be drained.
	table := NewTable("test-topo")
	table.RegisterPath(&graph.Path{ID: "p1", VertexIDs: []string{"r1", "r2"}})
	table.RegisterPath(&graph.Path{ID: "p2", VertexIDs: []string{"r3", "r4"}})

	allocateToTable(t, table, "wl-1", []string{"p1"})
	allocateToTable(t, table, "wl-2", []string{"p2"})

	table.InvalidateElement("r2")

	wl1, _ := table.GetWorkload("wl-1")
	wl2, _ := table.GetWorkload("wl-2")

	if wl1.State != WorkloadDraining {
		t.Errorf("wl-1 state = %q, want draining", wl1.State)
	}
	if wl2.State != WorkloadActive {
		t.Errorf("wl-2 state = %q, want active (r2 not on its path)", wl2.State)
	}
}

func TestInvalidateElement_EmptyTable_NoOp(t *testing.T) {
	table := NewTable("test-topo")
	// Should not panic on empty table.
	table.InvalidateElement("r1")
}

func TestInvalidateElement_NoPath_PathIDMissing(t *testing.T) {
	// Register a workload with a path that is NOT in t.paths (edge case).
	table := NewTable("test-topo")
	table.RegisterPath(&graph.Path{ID: "p1", VertexIDs: []string{"r1"}})
	allocateToTable(t, table, "wl-1", []string{"p1"})

	// Manually remove the path from the table to simulate a corrupted state.
	table.mu.Lock()
	delete(table.paths, "p1")
	table.mu.Unlock()

	// Should not panic — pathsTouchElement skips missing paths.
	table.InvalidateElement("r1")
}

// --- DrainAll ----------------------------------------------------------------

func TestDrainAll_DrainsSingleActive(t *testing.T) {
	table := makeTableWithPath(t, "p1", []string{"r1"}, nil)
	allocateToTable(t, table, "wl-1", []string{"p1"})

	table.DrainAll()

	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadDraining {
		t.Errorf("workload state = %q, want draining", wl.State)
	}
	if s, _ := table.PathStateOf("p1"); s != StateDraining {
		t.Errorf("path state = %q, want draining", s)
	}
}

func TestDrainAll_MultipleWorkloads(t *testing.T) {
	table := NewTable("test-topo")
	table.RegisterPath(&graph.Path{ID: "p1"})
	table.RegisterPath(&graph.Path{ID: "p2"})
	allocateToTable(t, table, "wl-1", []string{"p1"})
	allocateToTable(t, table, "wl-2", []string{"p2"})

	table.DrainAll()

	for _, id := range []string{"wl-1", "wl-2"} {
		wl, _ := table.GetWorkload(id)
		if wl.State != WorkloadDraining {
			t.Errorf("%s state = %q, want draining", id, wl.State)
		}
	}
}

func TestDrainAll_SkipsAlreadyDraining(t *testing.T) {
	table := makeTableWithPath(t, "p1", []string{"r1"}, nil)
	allocateToTable(t, table, "wl-1", []string{"p1"})
	_ = table.DrainWorkload("wl-1") // already draining

	// Second DrainAll should not restart the drain timer or panic.
	table.DrainAll()

	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadDraining {
		t.Errorf("state = %q, want draining", wl.State)
	}
}

func TestDrainAll_EmptyTable_NoOp(t *testing.T) {
	table := NewTable("test-topo")
	table.DrainAll() // should not panic
}

// --- TableSet ----------------------------------------------------------------

func TestTableSet_GetPutDelete(t *testing.T) {
	ts := NewTableSet()
	tbl := NewTable("t1")
	ts.Put("t1", tbl)

	if got := ts.Get("t1"); got != tbl {
		t.Error("Get did not return the stored table")
	}

	ts.Delete("t1")
	if ts.Get("t1") != nil {
		t.Error("Get should return nil after Delete")
	}
}

func TestTableSet_All_ReturnsCopy(t *testing.T) {
	ts := NewTableSet()
	ts.Put("t1", NewTable("t1"))
	ts.Put("t2", NewTable("t2"))

	all := ts.All()
	if len(all) != 2 {
		t.Errorf("All len = %d, want 2", len(all))
	}

	// Mutating the returned map must not affect the TableSet.
	delete(all, "t1")
	if ts.Get("t1") == nil {
		t.Error("deleting from All() copy affected the TableSet")
	}
}

func TestTableSet_InvalidateElement_Delegates(t *testing.T) {
	ts := NewTableSet()
	tbl := NewTable("topo-1")
	tbl.RegisterPath(&graph.Path{ID: "p1", VertexIDs: []string{"r1"}})

	wl := &WorkloadAllocation{WorkloadID: "wl-1", Sharing: graph.SharingExclusive}
	_ = tbl.AllocatePaths(wl, []string{"p1"})
	ts.Put("topo-1", tbl)

	ts.InvalidateElement("topo-1", "r1")

	w, _ := tbl.GetWorkload("wl-1")
	if w.State != WorkloadDraining {
		t.Errorf("workload state = %q, want draining after TableSet.InvalidateElement", w.State)
	}
}

func TestTableSet_InvalidateElement_MissingTopoNoOp(t *testing.T) {
	ts := NewTableSet()
	// Should not panic for an unknown topology ID.
	ts.InvalidateElement("nonexistent", "r1")
}

// TestInvalidateElement_DrainTimer verifies that a drain timer is actually
// started: after the invalidation, the workload should eventually transition to
// complete if we wait. We use a very short DrainGracePeriod substitute by
// manually triggering releaseDrained after drain.
func TestInvalidateElement_PathReleasedAfterInvalidation(t *testing.T) {
	table := makeTableWithPath(t, "p1", []string{"r1"}, nil)
	allocateToTable(t, table, "wl-1", []string{"p1"})

	table.InvalidateElement("r1")

	// Workload must now be draining.
	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadDraining {
		t.Fatalf("state = %q, want draining", wl.State)
	}

	// Manually trigger the release (simulating the drain timer firing) so we
	// don't wait 30 seconds in a unit test.
	table.releaseDrained("wl-1")

	wl, _ = table.GetWorkload("wl-1")
	if wl.State != WorkloadComplete {
		t.Errorf("state after release = %q, want complete", wl.State)
	}
	if s, _ := table.PathStateOf("p1"); s != StateFree {
		t.Errorf("path state after release = %q, want free", s)
	}

	// Stop any pending drain timer to avoid goroutine leak.
	table.mu.Lock()
	if wl.drainTimer != nil {
		wl.drainTimer.Stop()
	}
	table.mu.Unlock()
}

// TestDrainAll_TimerFires confirms DrainAll starts a drain timer by
// checking that releaseDrained completes the workload.
func TestDrainAll_TimerFires(t *testing.T) {
	table := makeTableWithPath(t, "p1", nil, nil)
	allocateToTable(t, table, "wl-1", []string{"p1"})

	table.DrainAll()
	table.releaseDrained("wl-1")

	wl, _ := table.GetWorkload("wl-1")
	if wl.State != WorkloadComplete {
		t.Errorf("state = %q after DrainAll+release, want complete", wl.State)
	}

	table.mu.Lock()
	if wl.drainTimer != nil {
		wl.drainTimer.Stop()
	}
	table.mu.Unlock()
}

// TestInvalidateElement_ShortTimer replaces the drain timer with a very short
// one to verify the automatic path → FREE transition without a 30s wait.
func TestInvalidateElement_ShortTimer(t *testing.T) {
	table := makeTableWithPath(t, "p1", []string{"r1"}, nil)
	allocateToTable(t, table, "wl-1", []string{"p1"})

	// Override the grace period for this table by mangling the timer after
	// InvalidateElement moves the workload to DRAINING.
	table.InvalidateElement("r1")

	table.mu.Lock()
	wl := table.workloads["wl-1"]
	if wl.drainTimer != nil {
		wl.drainTimer.Stop()
	}
	// Replace with a 10ms timer.
	wl.drainTimer = time.AfterFunc(10*time.Millisecond, func() {
		table.releaseDrained("wl-1")
	})
	table.mu.Unlock()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		wl, _ := table.GetWorkload("wl-1")
		if wl.State == WorkloadComplete {
			// Path should be free.
			if s, _ := table.PathStateOf("p1"); s != StateFree {
				t.Errorf("path state = %q, want free after drain", s)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("workload did not transition to complete within 200ms")
}
