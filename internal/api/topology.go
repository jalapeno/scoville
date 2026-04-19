package api

import (
	"fmt"
	"net/http"

	"github.com/jalapeno/scoville/internal/allocation"
	"github.com/jalapeno/scoville/internal/graph"
	"github.com/jalapeno/scoville/internal/topology"
	"github.com/jalapeno/scoville/pkg/apitypes"
)

func (s *Server) handleTopologyPush(w http.ResponseWriter, r *http.Request) {
	doc, err := topology.Parse(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	g, errs := topology.Build(doc)
	if len(errs) > 0 {
		detail := make([]string, len(errs))
		for i, e := range errs {
			detail[i] = e.Error()
		}
		writeError(w, http.StatusUnprocessableEntity, "topology build errors", detail...)
		return
	}

	// Incremental update: if a topology with this ID already exists, only
	// invalidate workloads whose paths traverse elements that were removed.
	// Workloads on paths that still exist in the new topology remain active.
	// If this is a first push, create a fresh allocation table.
	if oldG := s.store.Get(g.ID()); oldG != nil {
		if table := s.tables.Get(g.ID()); table != nil {
			invalidateRemovedElements(oldG, g, table)
		}
		s.log.Info("topology updated incrementally", "topology_id", g.ID())
	} else {
		s.tables.Put(g.ID(), allocation.NewTable(g.ID()))
	}

	s.store.Put(g)

	s.log.Info("topology loaded", "topology_id", g.ID(), "stats", g.Stats())

	writeJSON(w, http.StatusOK, apitypes.TopologyResponse{
		TopologyID:  g.ID(),
		Description: doc.Description,
		Stats:       g.Stats(),
	})
}

// invalidateRemovedElements drains workloads whose paths traverse any vertex or
// edge that is present in oldG but absent from newG. Called on incremental
// topology push so only affected allocations are invalidated — workloads on
// paths that remain topologically valid continue uninterrupted.
func invalidateRemovedElements(oldG, newG *graph.Graph, table *allocation.Table) {
	newVerts := make(map[string]struct{}, len(newG.AllVertices()))
	for _, v := range newG.AllVertices() {
		newVerts[v.GetID()] = struct{}{}
	}
	for _, v := range oldG.AllVertices() {
		if _, exists := newVerts[v.GetID()]; !exists {
			table.InvalidateElement(v.GetID())
		}
	}

	newEdges := make(map[string]struct{}, len(newG.AllEdges()))
	for _, e := range newG.AllEdges() {
		newEdges[e.GetID()] = struct{}{}
	}
	for _, e := range oldG.AllEdges() {
		if _, exists := newEdges[e.GetID()]; !exists {
			table.InvalidateElement(e.GetID())
		}
	}
}

func (s *Server) handleTopologyList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, apitypes.TopologyListResponse{
		TopologyIDs: s.store.List(),
	})
}

func (s *Server) handleTopologyGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g := s.store.Get(id)
	if g == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("topology %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, apitypes.TopologyResponse{
		TopologyID: g.ID(),
		Stats:      g.Stats(),
	})
}

func (s *Server) handleTopologyNodes(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g := s.store.Get(id)
	if g == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("topology %q not found", id))
		return
	}
	verts := g.VerticesByType(graph.VTNode)
	nodes := make([]apitypes.NodeSummary, len(verts))
	for i, v := range verts {
		n := apitypes.NodeSummary{ID: v.GetID()}
		if nd, ok := v.(*graph.Node); ok {
			n.Name = nd.Name
		}
		nodes[i] = n
	}
	writeJSON(w, http.StatusOK, apitypes.TopologyNodesResponse{
		TopologyID: id,
		Nodes:      nodes,
	})
}

func (s *Server) handleTopologyDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.store.Get(id) == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("topology %q not found", id))
		return
	}
	s.store.Delete(id)
	s.tables.Delete(id)
	s.log.Info("topology deleted", "topology_id", id)
	w.WriteHeader(http.StatusNoContent)
}
