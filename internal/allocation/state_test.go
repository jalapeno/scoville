package allocation

import (
	"testing"

	"github.com/jalapeno/syd/internal/graph"
)

// --- helpers -----------------------------------------------------------------

func makePath(id string) *graph.Path {
	return &graph.Path{ID: id}
}

func makeWorkload(id string, sharing graph.SharingPolicy) *WorkloadAllocation {
	return &WorkloadAllocation{WorkloadID: id, Sharing: sharing}
}

func makeTable() *Table {
	return NewTable("test-topo")
}

// registerPaths adds paths to the table in the FREE state.
func registerPaths(t *testing.T, tbl *Table, ids ...string) {
	t.Helper()
	for _, id := range ids {
		tbl.RegisterPath(makePath(id))
	}
}

// --- RegisterPath ------------------------------------------------------------

func TestRegisterPath_Idempotent(t *testing.T) {
	tbl := makeTable()
	tbl.RegisterPath(makePath("p-1"))
	// Allocate p-1 so it's no longer FREE.
	wl := makeWorkload("wl-1", graph.SharingExclusive)
	if err := tbl.AllocatePaths(wl, []string{"p-1"}); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	// Registering the same path again must not overwrite the existing state.
	tbl.RegisterPath(makePath("p-1"))
	state, _ := tbl.PathStateOf("p-1")
	if state != StateExclusive {
		t.Errorf("re-register should not overwrite existing state: got %s", state)
	}
}

// --- AllocatePaths -----------------------------------------------------------

func TestAllocatePaths_ExclusiveTransition(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-1", "p-2")

	wl := makeWorkload("wl-1", graph.SharingExclusive)
	if err := tbl.AllocatePaths(wl, []string{"p-1", "p-2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, id := range []string{"p-1", "p-2"} {
		s, ok := tbl.PathStateOf(id)
		if !ok {
			t.Fatalf("path %s not found", id)
		}
		if s != StateExclusive {
			t.Errorf("path %s: want EXCLUSIVE, got %s", id, s)
		}
	}

	wlOut, ok := tbl.GetWorkload("wl-1")
	if !ok {
		t.Fatal("workload not stored in table")
	}
	if wlOut.State != WorkloadActive {
		t.Errorf("want workload ACTIVE, got %s", wlOut.State)
	}
}

func TestAllocatePaths_SharedTransition(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-1", "p-2")

	wl := makeWorkload("wl-1", graph.SharingAllowed)
	if err := tbl.AllocatePaths(wl, []string{"p-1", "p-2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, id := range []string{"p-1", "p-2"} {
		s, _ := tbl.PathStateOf(id)
		if s != StateShared {
			t.Errorf("path %s: want SHARED, got %s", id, s)
		}
	}
}

func TestAllocatePaths_SharedPathAllowedForSharedRequest(t *testing.T) {
	// A SHARED path may be allocated again by a sharing-tolerant workload.
	tbl := makeTable()
	registerPaths(t, tbl, "p-1")

	wl1 := makeWorkload("wl-1", graph.SharingAllowed)
	if err := tbl.AllocatePaths(wl1, []string{"p-1"}); err != nil {
		t.Fatalf("first allocation: %v", err)
	}

	wl2 := makeWorkload("wl-2", graph.SharingAllowed)
	if err := tbl.AllocatePaths(wl2, []string{"p-1"}); err != nil {
		t.Errorf("second (shared) allocation should succeed, got: %v", err)
	}
}

func TestAllocatePaths_SharedPathBlocksExclusiveRequest(t *testing.T) {
	// A SHARED path must not be allocated exclusively.
	tbl := makeTable()
	registerPaths(t, tbl, "p-1")

	wl1 := makeWorkload("wl-1", graph.SharingAllowed)
	_ = tbl.AllocatePaths(wl1, []string{"p-1"})

	wl2 := makeWorkload("wl-2", graph.SharingExclusive)
	if err := tbl.AllocatePaths(wl2, []string{"p-1"}); err == nil {
		t.Error("expected error when exclusively allocating a SHARED path, got nil")
	}
}

func TestAllocatePaths_ExclusivePathBlocksAnyRequest(t *testing.T) {
	// An EXCLUSIVE path must not be allocated by any subsequent workload.
	tbl := makeTable()
	registerPaths(t, tbl, "p-1")

	wl1 := makeWorkload("wl-1", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wl1, []string{"p-1"})

	for _, sharing := range []graph.SharingPolicy{graph.SharingExclusive, graph.SharingAllowed} {
		wl2 := makeWorkload("wl-2", sharing)
		if err := tbl.AllocatePaths(wl2, []string{"p-1"}); err == nil {
			t.Errorf("sharing=%s: expected error when allocating EXCLUSIVE path, got nil", sharing)
		}
	}
}

func TestAllocatePaths_PathNotFound(t *testing.T) {
	tbl := makeTable()
	wl := makeWorkload("wl-1", graph.SharingExclusive)
	if err := tbl.AllocatePaths(wl, []string{"no-such-path"}); err == nil {
		t.Error("expected error for unknown path ID, got nil")
	}
}

func TestAllocatePaths_AllOrNothing(t *testing.T) {
	// p-1 is FREE; p-2 is EXCLUSIVE. Allocating [p-1, p-2] must fail without
	// modifying p-1's state.
	tbl := makeTable()
	registerPaths(t, tbl, "p-1", "p-2")

	wl1 := makeWorkload("wl-1", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wl1, []string{"p-2"})

	wl2 := makeWorkload("wl-2", graph.SharingExclusive)
	if err := tbl.AllocatePaths(wl2, []string{"p-1", "p-2"}); err == nil {
		t.Fatal("expected error due to p-2 conflict")
	}

	// p-1 must still be FREE — no partial allocation.
	s, _ := tbl.PathStateOf("p-1")
	if s != StateFree {
		t.Errorf("p-1 should still be FREE after failed all-or-nothing allocation, got %s", s)
	}
	// wl-2 must not have been stored.
	if _, ok := tbl.GetWorkload("wl-2"); ok {
		t.Error("wl-2 should not be stored after failed allocation")
	}
}

// --- DrainWorkload -----------------------------------------------------------

func TestDrainWorkload_TransitionsToDraining(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-1", "p-2")

	wl := makeWorkload("wl-1", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wl, []string{"p-1", "p-2"})

	if err := tbl.DrainWorkload("wl-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, _ := tbl.GetWorkload("wl-1")
	if stored.State != WorkloadDraining {
		t.Errorf("want workload DRAINING, got %s", stored.State)
	}
	for _, id := range []string{"p-1", "p-2"} {
		s, _ := tbl.PathStateOf(id)
		if s != StateDraining {
			t.Errorf("path %s: want DRAINING, got %s", id, s)
		}
	}
}

func TestDrainWorkload_NotFound(t *testing.T) {
	tbl := makeTable()
	if err := tbl.DrainWorkload("no-such-workload"); err == nil {
		t.Error("expected error for unknown workload, got nil")
	}
}

// --- ReleaseWorkload ---------------------------------------------------------

func TestReleaseWorkload_FreesPathsAndCompletesWorkload(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-1", "p-2")

	wl := makeWorkload("wl-1", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wl, []string{"p-1", "p-2"})
	_ = tbl.DrainWorkload("wl-1")

	if err := tbl.ReleaseWorkload("wl-1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stored, _ := tbl.GetWorkload("wl-1")
	if stored.State != WorkloadComplete {
		t.Errorf("want workload COMPLETE, got %s", stored.State)
	}
	for _, id := range []string{"p-1", "p-2"} {
		s, _ := tbl.PathStateOf(id)
		if s != StateFree {
			t.Errorf("path %s: want FREE after release, got %s", id, s)
		}
	}
}

func TestReleaseWorkload_NotFound(t *testing.T) {
	tbl := makeTable()
	if err := tbl.ReleaseWorkload("no-such-workload"); err == nil {
		t.Error("expected error for unknown workload, got nil")
	}
}

// --- FreePaths / SharedPaths -------------------------------------------------

func TestFreePaths_ReturnsOnlyFree(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-free", "p-excl", "p-shared")

	wlExcl := makeWorkload("wl-excl", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wlExcl, []string{"p-excl"})

	wlShared := makeWorkload("wl-shared", graph.SharingAllowed)
	_ = tbl.AllocatePaths(wlShared, []string{"p-shared"})

	free := tbl.FreePaths()
	if len(free) != 1 || free[0].Path.ID != "p-free" {
		t.Errorf("want [p-free], got %v", free)
	}
}

func TestSharedPaths_ReturnsOnlyShared(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-free", "p-shared")

	wl := makeWorkload("wl-1", graph.SharingAllowed)
	_ = tbl.AllocatePaths(wl, []string{"p-shared"})

	shared := tbl.SharedPaths()
	if len(shared) != 1 || shared[0].Path.ID != "p-shared" {
		t.Errorf("want [p-shared], got %v", shared)
	}
}

// --- PathStateOf -------------------------------------------------------------

func TestPathStateOf_KnownAndUnknown(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-1")

	s, ok := tbl.PathStateOf("p-1")
	if !ok || s != StateFree {
		t.Errorf("want FREE ok=true, got %s ok=%v", s, ok)
	}
	_, ok = tbl.PathStateOf("no-such-path")
	if ok {
		t.Error("want ok=false for unknown path, got true")
	}
}

// --- Snapshot ----------------------------------------------------------------

func TestSnapshot_Counts(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p1", "p2", "p3", "p4")

	wlExcl := makeWorkload("wl-excl", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wlExcl, []string{"p1"})

	wlShared := makeWorkload("wl-shared", graph.SharingAllowed)
	_ = tbl.AllocatePaths(wlShared, []string{"p2"})

	_ = tbl.DrainWorkload("wl-excl")

	snap := tbl.Snapshot()
	if snap.Total != 4 {
		t.Errorf("want Total=4, got %d", snap.Total)
	}
	if snap.Free != 2 {
		t.Errorf("want Free=2, got %d", snap.Free)
	}
	if snap.Shared != 1 {
		t.Errorf("want Shared=1, got %d", snap.Shared)
	}
	if snap.Draining != 1 {
		t.Errorf("want Draining=1, got %d", snap.Draining)
	}
	// wl-excl is draining; wl-shared is still active.
	if snap.ActiveWorkloads != 1 {
		t.Errorf("want ActiveWorkloads=1, got %d", snap.ActiveWorkloads)
	}
}

// --- GetWorkload -------------------------------------------------------------

func TestGetWorkload_FoundAndNotFound(t *testing.T) {
	tbl := makeTable()
	registerPaths(t, tbl, "p-1")
	wl := makeWorkload("wl-1", graph.SharingExclusive)
	_ = tbl.AllocatePaths(wl, []string{"p-1"})

	got, ok := tbl.GetWorkload("wl-1")
	if !ok || got.WorkloadID != "wl-1" {
		t.Errorf("want wl-1 found, got ok=%v id=%q", ok, got.WorkloadID)
	}
	_, ok = tbl.GetWorkload("no-such")
	if ok {
		t.Error("want ok=false for unknown workload, got true")
	}
}
