package api

import (
	"fmt"
	"net/http"

	"github.com/jalapeno/syd/internal/graph"
)

// --- Topology graph API for UI visualization ---

type uiGraphNode struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type uiGraphLink struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Metric    uint32 `json:"metric,omitempty"`
	Bandwidth uint64 `json:"bandwidth,omitempty"`
	Delay     uint32 `json:"delay,omitempty"`
}

type uiTopologyGraph struct {
	Nodes []uiGraphNode `json:"nodes"`
	Links []uiGraphLink `json:"links"`
}

func (s *Server) handleTopologyGraph(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g := s.store.Get(id)
	if g == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("topology %q not found", id))
		return
	}

	// Collect all Node vertices
	nodeVerts := g.VerticesByType(graph.VTNode)
	nodes := make([]uiGraphNode, 0, len(nodeVerts))
	nodeIDs := make(map[string]struct{}, len(nodeVerts))
	for _, v := range nodeVerts {
		n := uiGraphNode{ID: v.GetID()}
		if nd, ok := v.(*graph.Node); ok {
			n.Name = nd.Name
		}
		nodes = append(nodes, n)
		nodeIDs[v.GetID()] = struct{}{}
	}

	// Also include Endpoint vertices
	epVerts := g.VerticesByType(graph.VTEndpoint)
	for _, v := range epVerts {
		nodes = append(nodes, uiGraphNode{ID: v.GetID()})
		nodeIDs[v.GetID()] = struct{}{}
	}

	// Collect LinkEdge and AttachmentEdge as links
	seen := make(map[string]struct{})
	var links []uiGraphLink
	for _, e := range g.AllEdges() {
		// Only include edges where both ends are in our node set
		src, dst := e.GetSrcID(), e.GetDstID()
		if _, ok := nodeIDs[src]; !ok {
			continue
		}
		if _, ok := nodeIDs[dst]; !ok {
			continue
		}

		// Deduplicate bidirectional link edges (keep one per pair)
		pairKey := src + "|" + dst
		reversePairKey := dst + "|" + src
		if _, ok := seen[pairKey]; ok {
			continue
		}
		if _, ok := seen[reversePairKey]; ok {
			continue
		}
		seen[pairKey] = struct{}{}

		link := uiGraphLink{
			ID:     e.GetID(),
			Source: src,
			Target: dst,
		}

		switch le := e.(type) {
		case *graph.LinkEdge:
			link.Metric = le.IGPMetric
			link.Bandwidth = le.MaxBW
			link.Delay = le.UnidirDelay
		}

		links = append(links, link)
	}

	writeJSON(w, http.StatusOK, uiTopologyGraph{
		Nodes: nodes,
		Links: links,
	})
}
