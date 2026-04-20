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

	nodes := make([]uiGraphNode, 0)
	nodeIDs := make(map[string]struct{})

	// Node vertices
	for _, v := range g.VerticesByType(graph.VTNode) {
		n := uiGraphNode{ID: v.GetID()}
		if nd, ok := v.(*graph.Node); ok {
			n.Name = nd.Name
		}
		nodes = append(nodes, n)
		nodeIDs[v.GetID()] = struct{}{}
	}

	// Endpoint vertices
	for _, v := range g.VerticesByType(graph.VTEndpoint) {
		n := uiGraphNode{ID: v.GetID()}
		if ep, ok := v.(*graph.Endpoint); ok {
			n.Name = ep.Name
		}
		nodes = append(nodes, n)
		nodeIDs[v.GetID()] = struct{}{}
	}

	// Prefix vertices — present in prefix-layer graphs (underlay-prefixes-v4/v6).
	// Include them so the graph endpoint is useful for those topologies too.
	for _, v := range g.VerticesByType(graph.VTPrefix) {
		n := uiGraphNode{ID: v.GetID()}
		if pv, ok := v.(*graph.Prefix); ok {
			n.Name = pv.Prefix // CIDR string as display name
		}
		nodes = append(nodes, n)
		nodeIDs[v.GetID()] = struct{}{}
	}

	// Collect edges where both endpoints are in the node set.
	// Initialize as non-nil so the JSON field is always an array, never null.
	seen := make(map[string]struct{})
	links := make([]uiGraphLink, 0)
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
