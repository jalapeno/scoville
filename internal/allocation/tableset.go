package allocation

import (
	"sync"

	"github.com/jalapeno/syd/internal/graph"
)

// TableSet is a goroutine-safe registry of allocation Tables keyed by topology
// ID. It replaces a plain map[string]*Table, which would not be safe for
// concurrent access from both HTTP handler goroutines and the BMP collector
// goroutine.
type TableSet struct {
	mu        sync.RWMutex
	tables    map[string]*Table
	onRelease func(topoID, workloadID string, paths []*graph.Path)
}

// NewTableSet creates an empty TableSet.
func NewTableSet() *TableSet {
	return &TableSet{tables: make(map[string]*Table)}
}

// Get returns the Table for the given topology ID, or nil if not registered.
func (ts *TableSet) Get(topoID string) *Table {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.tables[topoID]
}

// SetOnRelease registers fn as the release callback for all current and future
// Tables managed by this set. When a workload transitions to COMPLETE, fn is
// called with the topology ID, workload ID, and the paths that were freed.
// Pass nil to unregister.
func (ts *TableSet) SetOnRelease(fn func(topoID, workloadID string, paths []*graph.Path)) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.onRelease = fn
	for topoID, t := range ts.tables {
		id := topoID
		if fn != nil {
			t.SetOnRelease(func(workloadID string, paths []*graph.Path) {
				fn(id, workloadID, paths)
			})
		} else {
			t.SetOnRelease(nil)
		}
	}
}

// Put registers (or replaces) the Table for topoID. If a release callback has
// been registered via SetOnRelease it is attached to the new Table immediately.
func (ts *TableSet) Put(topoID string, t *Table) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tables[topoID] = t
	if ts.onRelease != nil {
		fn := ts.onRelease
		id := topoID
		t.SetOnRelease(func(workloadID string, paths []*graph.Path) {
			fn(id, workloadID, paths)
		})
	}
}

// Delete removes the Table for topoID. It is a no-op if topoID is not present.
func (ts *TableSet) Delete(topoID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	delete(ts.tables, topoID)
}

// List returns the IDs of all registered topologies.
func (ts *TableSet) List() []string {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	ids := make([]string, 0, len(ts.tables))
	for id := range ts.tables {
		ids = append(ids, id)
	}
	return ids
}

// All returns a snapshot copy of the (topoID → *Table) map. The caller may
// iterate the returned map freely; changes to it do not affect the TableSet.
// The *Table values themselves are safe to call concurrently.
func (ts *TableSet) All() map[string]*Table {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	cp := make(map[string]*Table, len(ts.tables))
	for k, v := range ts.tables {
		cp[k] = v
	}
	return cp
}

// InvalidateElement looks up the table for topoID and, if found, calls
// Table.InvalidateElement(elementID). This is the function signature wired as
// the BMP removal callback so the collector does not need to import this
// package directly.
func (ts *TableSet) InvalidateElement(topoID, elementID string) {
	t := ts.Get(topoID)
	if t == nil {
		return
	}
	t.InvalidateElement(elementID)
}
