package pathengine

import (
	"testing"

	"github.com/jalapeno/syd/internal/graph"
	"github.com/jalapeno/syd/internal/srv6"
)

// f3216 is the F3216 uSID structure for uA SIDs (funcLen=16).
var f3216 = &srv6.SIDStructure{LocatorBlockLen: 32, LocatorNodeLen: 16, FunctionLen: 16}

// f3216uN is the F3216 uSID structure for uN SIDs (funcLen=0).
// Real IOS-XR uN SIDs advertise FunctionLen=0: the node locator IS the SID,
// no function bits are present. This must match srv6/usid_test.go.
var f3216uN = &srv6.SIDStructure{LocatorBlockLen: 32, LocatorNodeLen: 16, FunctionLen: 0}

// mustAdd calls t.Fatal if err is non-nil. Used to keep graph construction
// in tests free of repetitive error checks.
func mustAdd(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("graph setup: %v", err)
	}
}

// makeLeafSpineGraph builds a three-node, single-spine leaf-spine-leaf graph
// with forward and reverse LinkEdges and F3216 uA/uN SIDs on every node and
// interface. This is the standard test topology used by most pathengine tests.
//
//	leaf-1 --[e-l1-s1, m=10]--> spine-1 --[e-s1-l2, m=10]--> leaf-2
//	leaf-1 <--[e-s1-l1, m=10]-- spine-1 <--[e-l2-s1, m=10]-- leaf-2
//
// SID assignments:
//
//	leaf-1  uN: fc00:0:3::     leaf-1-eth0  uA: fc00:0:3:e001::
//	spine-1 uN: fc00:0:2::     spine-1-eth0 uA: fc00:0:2:e001::  (toward leaf-1)
//	                           spine-1-eth1 uA: fc00:0:2:e002::  (toward leaf-2)
//	leaf-2  uN: fc00:0:4::     leaf-2-eth0  uA: fc00:0:4:e001::
func makeLeafSpineGraph(t testing.TB) *graph.Graph {
	t.Helper()
	g := graph.New("test")

	// ---- Nodes ----
	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex: graph.BaseVertex{ID: "leaf-1", Type: graph.VTNode},
		SRv6Locators: []srv6.Locator{{
			Prefix: "fc00:0:3::/48", AlgoID: 0,
			NodeSID: &srv6.SID{Value: "fc00:0:3::", Behavior: srv6.BehaviorEnd, Structure: f3216uN},
		}},
	}))
	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex: graph.BaseVertex{ID: "spine-1", Type: graph.VTNode},
		SRv6Locators: []srv6.Locator{{
			Prefix: "fc00:0:2::/48", AlgoID: 0,
			NodeSID: &srv6.SID{Value: "fc00:0:2::", Behavior: srv6.BehaviorEnd, Structure: f3216uN},
		}},
	}))
	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex: graph.BaseVertex{ID: "leaf-2", Type: graph.VTNode},
		SRv6Locators: []srv6.Locator{{
			Prefix: "fc00:0:4::/48", AlgoID: 0,
			NodeSID: &srv6.SID{Value: "fc00:0:4::", Behavior: srv6.BehaviorEnd, Structure: f3216uN},
		}},
	}))

	// ---- Interfaces ----
	mustAdd(t, g.AddVertex(&graph.Interface{
		BaseVertex:  graph.BaseVertex{ID: "leaf-1-eth0", Type: graph.VTInterface},
		OwnerNodeID: "leaf-1",
		SRv6uASIDs: []srv6.UASID{{
			SID: srv6.SID{Value: "fc00:0:3:e001::", Behavior: srv6.BehaviorEndX, Structure: f3216},
		}},
	}))
	mustAdd(t, g.AddVertex(&graph.Interface{
		BaseVertex:  graph.BaseVertex{ID: "spine-1-eth0", Type: graph.VTInterface},
		OwnerNodeID: "spine-1",
		SRv6uASIDs: []srv6.UASID{{
			SID: srv6.SID{Value: "fc00:0:2:e001::", Behavior: srv6.BehaviorEndX, Structure: f3216},
		}},
	}))
	mustAdd(t, g.AddVertex(&graph.Interface{
		BaseVertex:  graph.BaseVertex{ID: "spine-1-eth1", Type: graph.VTInterface},
		OwnerNodeID: "spine-1",
		SRv6uASIDs: []srv6.UASID{{
			SID: srv6.SID{Value: "fc00:0:2:e002::", Behavior: srv6.BehaviorEndX, Structure: f3216},
		}},
	}))
	mustAdd(t, g.AddVertex(&graph.Interface{
		BaseVertex:  graph.BaseVertex{ID: "leaf-2-eth0", Type: graph.VTInterface},
		OwnerNodeID: "leaf-2",
		SRv6uASIDs: []srv6.UASID{{
			SID: srv6.SID{Value: "fc00:0:4:e001::", Behavior: srv6.BehaviorEndX, Structure: f3216},
		}},
	}))

	// ---- Edges: forward ----
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-l1-s1", Type: graph.ETIGPAdjacency, SrcID: "leaf-1", DstID: "spine-1", Directed: true},
		LocalIfaceID:  "leaf-1-eth0",
		RemoteIfaceID: "spine-1-eth0",
		IGPMetric:     10,
	}))
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-s1-l2", Type: graph.ETIGPAdjacency, SrcID: "spine-1", DstID: "leaf-2", Directed: true},
		LocalIfaceID:  "spine-1-eth1",
		RemoteIfaceID: "leaf-2-eth0",
		IGPMetric:     10,
	}))

	// ---- Edges: reverse ----
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-s1-l1", Type: graph.ETIGPAdjacency, SrcID: "spine-1", DstID: "leaf-1", Directed: true},
		LocalIfaceID:  "spine-1-eth0",
		RemoteIfaceID: "leaf-1-eth0",
		IGPMetric:     10,
	}))
	mustAdd(t, g.AddEdge(&graph.LinkEdge{
		BaseEdge:      graph.BaseEdge{ID: "e-l2-s1", Type: graph.ETIGPAdjacency, SrcID: "leaf-2", DstID: "spine-1", Directed: true},
		LocalIfaceID:  "leaf-2-eth0",
		RemoteIfaceID: "spine-1-eth1",
		IGPMetric:     10,
	}))

	return g
}
