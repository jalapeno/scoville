package api

import (
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/jalapeno/scoville/pkg/apitypes"
)

// policyStore holds per-topology name→algo_id mappings, guarded by a
// read/write mutex. It is embedded in Server and shared across handlers.
type policyStore struct {
	mu   sync.RWMutex
	data map[string]map[string]uint8 // topoID → policyName → algoID
}

func newPolicyStore() *policyStore {
	return &policyStore{data: make(map[string]map[string]uint8)}
}

// Resolve returns the algo_id for the given (topoID, policyName) pair.
// ok is false when the policy is not registered.
func (ps *policyStore) Resolve(topoID, name string) (uint8, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	m, ok := ps.data[topoID]
	if !ok {
		return 0, false
	}
	id, ok := m[name]
	return id, ok
}

// Set merges entries into the policy map for topoID. An entry with algo_id=0
// removes the named policy.
func (ps *policyStore) Set(topoID string, entries []apitypes.PolicyEntry) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	m, ok := ps.data[topoID]
	if !ok {
		m = make(map[string]uint8)
		ps.data[topoID] = m
	}
	for _, e := range entries {
		if e.AlgoID == 0 {
			delete(m, e.Name)
		} else {
			m[e.Name] = e.AlgoID
		}
	}
	// Clean up empty maps.
	if len(m) == 0 {
		delete(ps.data, topoID)
	}
}

// List returns all policy entries for topoID, sorted by name.
func (ps *policyStore) List(topoID string) []apitypes.PolicyEntry {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	m := ps.data[topoID]
	if len(m) == 0 {
		return nil
	}
	out := make([]apitypes.PolicyEntry, 0, len(m))
	for name, id := range m {
		out = append(out, apitypes.PolicyEntry{Name: name, AlgoID: id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// DeleteAll removes all policies for topoID.
func (ps *policyStore) DeleteAll(topoID string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.data, topoID)
}

// --- HTTP handlers -----------------------------------------------------------

// handlePoliciesSet merges the supplied policy entries into the named topology's
// policy map. To remove a policy, include it with algo_id=0.
//
// POST /topology/{id}/policies
func (s *Server) handlePoliciesSet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.store.Get(id) == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("topology %q not found", id))
		return
	}

	var req apitypes.PoliciesRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if len(req.Policies) == 0 {
		writeError(w, http.StatusBadRequest, "policies list must not be empty")
		return
	}
	for _, e := range req.Policies {
		if e.Name == "" {
			writeError(w, http.StatusBadRequest, "each policy entry must have a non-empty name")
			return
		}
	}

	s.policies.Set(id, req.Policies)
	s.log.Info("policies updated", "topology_id", id, "count", len(req.Policies))

	writeJSON(w, http.StatusOK, apitypes.PoliciesResponse{
		TopologyID: id,
		Policies:   s.policies.List(id),
	})
}

// handlePoliciesGet returns the current policy map for the named topology.
//
// GET /topology/{id}/policies
func (s *Server) handlePoliciesGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.store.Get(id) == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("topology %q not found", id))
		return
	}
	writeJSON(w, http.StatusOK, apitypes.PoliciesResponse{
		TopologyID: id,
		Policies:   s.policies.List(id),
	})
}

// handlePoliciesDelete clears all policies for the named topology.
//
// DELETE /topology/{id}/policies
func (s *Server) handlePoliciesDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.store.Get(id) == nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("topology %q not found", id))
		return
	}
	s.policies.DeleteAll(id)
	s.log.Info("policies cleared", "topology_id", id)
	w.WriteHeader(http.StatusNoContent)
}
