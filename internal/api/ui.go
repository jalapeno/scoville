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
	Type string `json:"type,omitempty"`
}

type uiGraphLink struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Type      string `json:"type,omitempty"`
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

	// Collect all vertices except Interfaces (which are internal to links
	// and clutter the visualization).
	allVerts := g.AllVertices()
	nodeIDs := make(map[string]struct{}, len(allVerts))
	var nodes []uiGraphNode
	for _, v := range allVerts {
		vt := v.GetType()
		if vt == graph.VTInterface {
			continue // skip interfaces — they're internal to link modeling
		}
		n := uiGraphNode{
			ID:   v.GetID(),
			Type: string(vt),
		}
		if nd, ok := v.(*graph.Node); ok {
			n.Name = nd.Name
		}
		nodes = append(nodes, n)
		nodeIDs[v.GetID()] = struct{}{}
	}

	// Collect all edges where both endpoints are in our visible node set.
	seen := make(map[string]struct{})
	var links []uiGraphLink
	for _, e := range g.AllEdges() {
		src, dst := e.GetSrcID(), e.GetDstID()
		if _, ok := nodeIDs[src]; !ok {
			continue
		}
		if _, ok := nodeIDs[dst]; !ok {
			continue
		}

		// Deduplicate bidirectional edges (keep one per pair)
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
			Type:   string(e.GetType()),
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
