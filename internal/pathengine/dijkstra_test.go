package pathengine

import (
	"testing"

	"github.com/jalapeno/scoville/internal/graph"
	"github.com/jalapeno/scoville/internal/srv6"
)

// makeDualSpineGraph adds a second spine to the standard graph so we have two
// alternative paths with different costs:
//
//	leaf-1 --[m=10]--> spine-1 --[m=10, BW=200]--> leaf-2   (total cost 20)
//	leaf-1 --[m=20]--> spine-2 --[m=10, BW= 50]--> leaf-2   (total cost 30)
func makeDualSpineGraph(t testing.TB) *graph.Graph {
	t.Helper()
	g := makeLeafSpineGraph(t)

	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex: graph.BaseVertex{ID: "spine-2", Type: graph.VTNode},
	}))

	// Give the existing spine-1 edges explicit BW so bandwidth tests work.
	// Recreate them with UnidirAvailBW set (AddEdge replaces on same ID).
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-l1-s1", Type: graph.ETIGPAdjacency, SrcID: "leaf-1", DstID: "spine-1", Directed: true},
		LocalIfaceID:  "leaf-1-eth0",
		RemoteIfaceID: "spine-1-eth0",
		IGPMetric:     10,
		UnidirAvailBW: 200,
	}))
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-s1-l2", Type: graph.ETIGPAdjacency, SrcID: "spine-1", DstID: "leaf-2", Directed: true},
		LocalIfaceID:  "spine-1-eth1",
		RemoteIfaceID: "leaf-2-eth0",
		IGPMetric:     10,
		UnidirAvailBW: 200,
	}))

	// spine-2 edges with lower BW.
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-l1-s2", Type: graph.ETIGPAdjacency, SrcID: "leaf-1", DstID: "spine-2", Directed: true},
		IGPMetric:     20,
		UnidirAvailBW: 50,
	}))
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-s2-l2", Type: graph.ETIGPAdjacency, SrcID: "spine-2", DstID: "leaf-2", Directed: true},
		IGPMetric:     10,
		UnidirAvailBW: 50,
	}))
	return g
}

func TestDijkstra_ShortestPath(t *testing.T) {
	// With two spines, Dijkstra should prefer the lower-cost spine-1 path.
	g := makeDualSpineGraph(t)
	constraints := graph.PathConstraints{}
	ex := NewExcludedSet()

	spf, err := Dijkstra(g, "leaf-1", "leaf-2", CostFuncFor(MetricIGP), constraints, ex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spf.Edges) != 2 {
		t.Fatalf("want 2 hops, got %d", len(spf.Edges))
	}
	// The path should traverse spine-1, not spine-2.
	if spf.NodeIDs[1] != "spine-1" {
		t.Errorf("want spine-1 as transit, got %s", spf.NodeIDs[1])
	}
	if spf.TotalCost != 20 {
		t.Errorf("want total cost 20, got %v", spf.TotalCost)
	}
}

func TestDijkstra_EdgeExclusion(t *testing.T) {
	// With the spine-1 edges excluded, the path must go via spine-2.
	g := makeDualSpineGraph(t)
	constraints := graph.PathConstraints{}
	ex := NewExcludedSet()
	ex.Edges["e-l1-s1"] = struct{}{}
	ex.Edges["e-s1-l2"] = struct{}{}

	spf, err := Dijkstra(g, "leaf-1", "leaf-2", CostFuncFor(MetricIGP), constraints, ex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spf.NodeIDs[1] != "spine-2" {
		t.Errorf("want spine-2 as transit after spine-1 exclusion, got %s", spf.NodeIDs[1])
	}
}

func TestDijkstra_NodeExclusion(t *testing.T) {
	// With spine-1 excluded as a transit node, the path must use spine-2.
	g := makeDualSpineGraph(t)
	constraints := graph.PathConstraints{}
	ex := NewExcludedSet()
	ex.Nodes["spine-1"] = struct{}{}

	spf, err := Dijkstra(g, "leaf-1", "leaf-2", CostFuncFor(MetricIGP), constraints, ex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range spf.NodeIDs {
		if n == "spine-1" {
			t.Errorf("spine-1 appears in path despite node exclusion: %v", spf.NodeIDs)
		}
	}
}

func TestDijkstra_BandwidthConstraint(t *testing.T) {
	// MinBW=100 filters out spine-2 (BW=50); path must go via spine-1 (BW=200).
	g := makeDualSpineGraph(t)
	constraints := graph.PathConstraints{MinBandwidthBPS: 100}
	ex := NewExcludedSet()

	spf, err := Dijkstra(g, "leaf-1", "leaf-2", CostFuncFor(MetricIGP), constraints, ex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spf.NodeIDs[1] != "spine-1" {
		t.Errorf("want spine-1 (BW 200 >= 100), got %s", spf.NodeIDs[1])
	}
}

func TestDijkstra_BandwidthConstraintNoPath(t *testing.T) {
	// MinBW=300 exceeds all edge BW → no valid path.
	g := makeDualSpineGraph(t)
	constraints := graph.PathConstraints{MinBandwidthBPS: 300}
	ex := NewExcludedSet()

	_, err := Dijkstra(g, "leaf-1", "leaf-2", CostFuncFor(MetricIGP), constraints, ex)
	if err == nil {
		t.Fatal("expected error for unsatisfiable bandwidth, got nil")
	}
}

func TestDijkstra_SrcEqualsDst(t *testing.T) {
	// Dijkstra with src==dst returns an empty-edge result immediately.
	g := makeLeafSpineGraph(t)
	spf, err := Dijkstra(g, "leaf-1", "leaf-1", CostFuncFor(MetricIGP), graph.PathConstraints{}, NewExcludedSet())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spf.Edges) != 0 {
		t.Errorf("want 0 edges for src==dst, got %d", len(spf.Edges))
	}
}

func TestDijkstra_FlexAlgoFilter(t *testing.T) {
	// Graph: leaf-1 can reach leaf-2 via spine-1 (cost 20) or spine-2 (cost 5).
	// Only leaf-1, spine-1, and leaf-2 have algo-128 locators; spine-2 does not.
	//
	// Without algo filter: Dijkstra picks spine-2 (cheaper).
	// With algo_id=128:    Dijkstra must pick spine-1 (spine-2 pruned).
	g := graph.New("test")

	algo128loc := func(nodeID, prefix, sid string) []srv6.Locator {
		return []srv6.Locator{
			{Prefix: prefix + "/48", AlgoID: 0, NodeSID: &srv6.SID{Value: sid, Behavior: srv6.BehaviorEnd}},
			{Prefix: prefix + "28::/48", AlgoID: 128, NodeSID: &srv6.SID{Value: sid + "28::", Behavior: srv6.BehaviorEnd}},
		}
	}

	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex:   graph.BaseVertex{ID: "leaf-1", Type: graph.VTNode},
		SRv6Locators: algo128loc("leaf-1", "fc00:0:1::", "fc00:0:1::"),
	}))
	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex:   graph.BaseVertex{ID: "spine-1", Type: graph.VTNode},
		SRv6Locators: algo128loc("spine-1", "fc00:0:2::", "fc00:0:2::"),
	}))
	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex:   graph.BaseVertex{ID: "leaf-2", Type: graph.VTNode},
		SRv6Locators: algo128loc("leaf-2", "fc00:0:4::", "fc00:0:4::"),
	}))
	// spine-2 has no algo-128 locator — it is not in the Flex-Algo 128 topology.
	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex: graph.BaseVertex{ID: "spine-2", Type: graph.VTNode},
		SRv6Locators: []srv6.Locator{
			{Prefix: "fc00:0:5::/48", AlgoID: 0, NodeSID: &srv6.SID{Value: "fc00:0:5::", Behavior: srv6.BehaviorEnd}},
		},
	}))

	addEdge := func(id, src, dst string, cost uint32) {
		mustAdd(t, g.AddEdge(&graph.LinkEdge{
			BaseEdge:  graph.BaseEdge{ID: id, Type: graph.ETIGPAdjacency, SrcID: src, DstID: dst, Directed: true},
			IGPMetric: cost,
		}))
	}
	addEdge("e-l1-s1", "leaf-1", "spine-1", 10) // spine-1 path: cost 20
	addEdge("e-s1-l2", "spine-1", "leaf-2", 10)
	addEdge("e-l1-s2", "leaf-1", "spine-2", 1) // spine-2 path: cost 5 (cheaper)
	addEdge("e-s2-l2", "spine-2", "leaf-2", 4)

	ex := NewExcludedSet()
	cf := CostFuncFor(MetricIGP)

	// Without algo filter: spine-2 (cost 5) wins.
	spf, err := Dijkstra(g, "leaf-1", "leaf-2", cf, graph.PathConstraints{}, ex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spf.NodeIDs[1] != "spine-2" {
		t.Errorf("baseline: want spine-2 as transit (cost 5), got %s", spf.NodeIDs[1])
	}

	// With algo_id=128: spine-2 is pruned; spine-1 (cost 20) is the only path.
	spf, err = Dijkstra(g, "leaf-1", "leaf-2", cf, graph.PathConstraints{AlgoID: 128}, ex)
	if err != nil {
		t.Fatalf("unexpected error with algo filter: %v", err)
	}
	if spf.NodeIDs[1] != "spine-1" {
		t.Errorf("algo=128: want spine-1 as transit (spine-2 pruned), got %s", spf.NodeIDs[1])
	}

	// With algo_id=128 and spine-1 also excluded: no valid path.
	ex2 := NewExcludedSet()
	ex2.Nodes["spine-1"] = struct{}{}
	_, err = Dijkstra(g, "leaf-1", "leaf-2", cf, graph.PathConstraints{AlgoID: 128}, ex2)
	if err == nil {
		t.Error("want error when all algo-128 paths excluded, got nil")
	}
}

func TestDijkstra_NoPath(t *testing.T) {
	// A disconnected graph (no edges from leaf-1 to leaf-2) returns an error.
	g := graph.New("test")
	mustAdd(t, g.AddVertex(&graph.Node{BaseVertex: graph.BaseVertex{ID: "A", Type: graph.VTNode}}))
	mustAdd(t, g.AddVertex(&graph.Node{BaseVertex: graph.BaseVertex{ID: "B", Type: graph.VTNode}}))
	// No edges added.

	_, err := Dijkstra(g, "A", "B", CostFuncFor(MetricIGP), graph.PathConstraints{}, NewExcludedSet())
	if err == nil {
		t.Fatal("expected error for disconnected graph, got nil")
	}
}
