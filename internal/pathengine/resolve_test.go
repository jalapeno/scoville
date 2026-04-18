package pathengine

import (
	"testing"

	"github.com/jalapeno/scoville/internal/graph"
	"github.com/jalapeno/scoville/pkg/apitypes"
)

// makeResolveGraph builds a minimal graph for endpoint resolution tests:
//
//	leaf-1 (Node)
//	  ← attachment ←
//	gpu-0 (Endpoint, address 10.0.0.1)
func makeResolveGraph(t testing.TB) *graph.Graph {
	t.Helper()
	g := graph.New("test")
	mustAdd(t, g.AddVertex(&graph.Node{
		BaseVertex: graph.BaseVertex{ID: "leaf-1", Type: graph.VTNode},
	}))
	mustAdd(t, g.AddVertex(&graph.Endpoint{
		BaseVertex: graph.BaseVertex{ID: "gpu-0", Type: graph.VTEndpoint},
		Addresses:  []string{"10.0.0.1"},
	}))
	mustAdd(t, g.AddEdge(&graph.AttachmentEdge{
		BaseEdge: graph.BaseEdge{
			ID: "att-gpu0-leaf1", Type: graph.ETAttachment,
			SrcID: "gpu-0", DstID: "leaf-1", Directed: true,
		},
	}))
	return g
}

func TestResolveEndpoints_ByNodeID(t *testing.T) {
	g := makeResolveGraph(t)
	specs := []apitypes.EndpointSpec{{ID: "leaf-1"}}

	resolved, errs := ResolveEndpoints(g, specs)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(resolved) != 1 {
		t.Fatalf("want 1 resolved endpoint, got %d", len(resolved))
	}
	if resolved[0].NodeID != "leaf-1" {
		t.Errorf("want NodeID=leaf-1, got %s", resolved[0].NodeID)
	}
}

func TestResolveEndpoints_ByEndpointID(t *testing.T) {
	// gpu-0 is an Endpoint; resolution should follow its AttachmentEdge to leaf-1.
	g := makeResolveGraph(t)
	specs := []apitypes.EndpointSpec{{ID: "gpu-0"}}

	resolved, errs := ResolveEndpoints(g, specs)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if resolved[0].NodeID != "leaf-1" {
		t.Errorf("want NodeID=leaf-1, got %s", resolved[0].NodeID)
	}
	if resolved[0].EndpointID != "gpu-0" {
		t.Errorf("want EndpointID=gpu-0, got %s", resolved[0].EndpointID)
	}
}

func TestResolveEndpoints_ByAddress(t *testing.T) {
	g := makeResolveGraph(t)
	specs := []apitypes.EndpointSpec{{Address: "10.0.0.1"}}

	resolved, errs := ResolveEndpoints(g, specs)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if resolved[0].NodeID != "leaf-1" {
		t.Errorf("want NodeID=leaf-1, got %s", resolved[0].NodeID)
	}
}

func TestResolveEndpoints_UnknownID(t *testing.T) {
	g := makeResolveGraph(t)
	specs := []apitypes.EndpointSpec{{ID: "does-not-exist"}}

	_, errs := ResolveEndpoints(g, specs)
	if len(errs) == 0 {
		t.Fatal("expected error for unknown vertex ID, got none")
	}
}

func TestResolveEndpoints_WrongVertexType(t *testing.T) {
	// Resolving an Interface vertex ID (not Node or Endpoint) should error.
	g := makeResolveGraph(t)
	mustAdd(t, g.AddVertex(&graph.Interface{
		BaseVertex:  graph.BaseVertex{ID: "iface-x", Type: graph.VTInterface},
		OwnerNodeID: "leaf-1",
	}))
	specs := []apitypes.EndpointSpec{{ID: "iface-x"}}

	_, errs := ResolveEndpoints(g, specs)
	if len(errs) == 0 {
		t.Fatal("expected error for interface vertex ID, got none")
	}
}
