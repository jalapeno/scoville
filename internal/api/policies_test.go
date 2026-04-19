package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jalapeno/scoville/pkg/apitypes"
)

// --- POST /topology/{id}/policies --------------------------------------------

func TestPoliciesSet_TopologyNotFound(t *testing.T) {
	_, baseURL := newTestServer(t)

	resp := doRequest(t, http.MethodPost, baseURL+"/topology/no-such-topo/policies",
		apitypes.PoliciesRequest{Policies: []apitypes.PolicyEntry{{Name: "p1", AlgoID: 128}}},
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestPoliciesSet_EmptyList(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodPost, baseURL+"/topology/test-topo/policies",
		apitypes.PoliciesRequest{Policies: nil},
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestPoliciesSet_Success(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodPost, baseURL+"/topology/test-topo/policies",
		apitypes.PoliciesRequest{Policies: []apitypes.PolicyEntry{
			{Name: "carbon-optimized", AlgoID: 130},
			{Name: "latency-sensitive", AlgoID: 128},
		}},
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body apitypes.PoliciesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TopologyID != "test-topo" {
		t.Errorf("want topology_id=test-topo, got %q", body.TopologyID)
	}
	if len(body.Policies) != 2 {
		t.Fatalf("want 2 policies, got %d", len(body.Policies))
	}
	// Sorted by name: carbon-optimized first.
	if body.Policies[0].Name != "carbon-optimized" || body.Policies[0].AlgoID != 130 {
		t.Errorf("unexpected first entry: %+v", body.Policies[0])
	}
	if body.Policies[1].Name != "latency-sensitive" || body.Policies[1].AlgoID != 128 {
		t.Errorf("unexpected second entry: %+v", body.Policies[1])
	}
}

func TestPoliciesSet_MergesIncrementally(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	// First POST: two policies.
	doRequest(t, http.MethodPost, baseURL+"/topology/test-topo/policies",
		apitypes.PoliciesRequest{Policies: []apitypes.PolicyEntry{
			{Name: "p1", AlgoID: 128},
			{Name: "p2", AlgoID: 129},
		}},
	).Body.Close()

	// Second POST: remove p2 (algo_id=0), add p3.
	doRequest(t, http.MethodPost, baseURL+"/topology/test-topo/policies",
		apitypes.PoliciesRequest{Policies: []apitypes.PolicyEntry{
			{Name: "p2", AlgoID: 0},
			{Name: "p3", AlgoID: 130},
		}},
	).Body.Close()

	resp := doRequest(t, http.MethodGet, baseURL+"/topology/test-topo/policies", nil)
	defer resp.Body.Close()
	var body apitypes.PoliciesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Should have p1 and p3; p2 was removed.
	names := make(map[string]uint8, len(body.Policies))
	for _, e := range body.Policies {
		names[e.Name] = e.AlgoID
	}
	if names["p1"] != 128 {
		t.Errorf("p1 should be algo 128, got %d", names["p1"])
	}
	if _, has := names["p2"]; has {
		t.Errorf("p2 should have been removed")
	}
	if names["p3"] != 130 {
		t.Errorf("p3 should be algo 130, got %d", names["p3"])
	}
}

// --- GET /topology/{id}/policies ---------------------------------------------

func TestPoliciesGet_Empty(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodGet, baseURL+"/topology/test-topo/policies", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body apitypes.PoliciesResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Policies) != 0 {
		t.Errorf("want empty list, got %v", body.Policies)
	}
}

func TestPoliciesGet_TopologyNotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodGet, baseURL+"/topology/no-such-topo/policies", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// --- DELETE /topology/{id}/policies ------------------------------------------

func TestPoliciesDelete_ClearsAll(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	doRequest(t, http.MethodPost, baseURL+"/topology/test-topo/policies",
		apitypes.PoliciesRequest{Policies: []apitypes.PolicyEntry{{Name: "p1", AlgoID: 128}}},
	).Body.Close()

	resp := doRequest(t, http.MethodDelete, baseURL+"/topology/test-topo/policies", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}

	resp2 := doRequest(t, http.MethodGet, baseURL+"/topology/test-topo/policies", nil)
	defer resp2.Body.Close()
	var body apitypes.PoliciesResponse
	_ = json.NewDecoder(resp2.Body).Decode(&body)
	if len(body.Policies) != 0 {
		t.Errorf("want empty after delete, got %v", body.Policies)
	}
}

// --- Policy resolution in POST /paths/request --------------------------------

func TestPathRequest_UnknownPolicy(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodPost, baseURL+"/paths/request",
		apitypes.PathRequest{
			TopologyID: "test-topo",
			WorkloadID: "wl-policy-test",
			Endpoints: []apitypes.EndpointSpec{
				{ID: "leaf-1"},
				{ID: "leaf-2"},
			},
			Policy: "carbon-optimized", // not registered
		},
	)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", resp.StatusCode)
	}
}
