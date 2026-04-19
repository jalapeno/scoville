package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jalapeno/syd/internal/allocation"
	"github.com/jalapeno/syd/internal/api"
	"github.com/jalapeno/syd/internal/graph"
	"github.com/jalapeno/syd/pkg/apitypes"
)

// apiMinimalDoc is a small but complete topology document used to exercise the
// topology push endpoint. topology_id is "test-topo".
const apiMinimalDoc = `{
  "topology_id": "test-topo",
  "source": "push",
  "nodes": [
    {"id": "leaf-1", "subtype": "switch"},
    {"id": "leaf-2", "subtype": "switch"}
  ],
  "interfaces": [
    {"id": "leaf-1-eth0", "owner_node_id": "leaf-1"},
    {"id": "leaf-2-eth0", "owner_node_id": "leaf-2"}
  ],
  "endpoints": [
    {"id": "gpu-0", "subtype": "gpu", "addresses": ["10.0.0.1"]},
    {"id": "gpu-1", "subtype": "gpu", "addresses": ["10.0.0.2"]}
  ],
  "edges": [
    {"id": "att-0", "type": "attachment", "src_id": "gpu-0", "dst_id": "leaf-1"},
    {"id": "att-1", "type": "attachment", "src_id": "gpu-1", "dst_id": "leaf-2"}
  ]
}`

// --- helpers -----------------------------------------------------------------

// newTestServer creates a Server backed by an empty store and a shared
// TableSet. Both are returned so tests can inspect allocation state directly
// after seeding or making HTTP calls.
func newTestServer(t *testing.T) (*allocation.TableSet, string) {
	t.Helper()
	store := graph.NewStore()
	tables := allocation.NewTableSet()
	srv := api.New(store, tables, slog.Default())
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return tables, ts.URL
}

// doRequest sends an HTTP request with an optional JSON body and returns the
// response. The response body is left open for the caller to read and close.
func doRequest(t *testing.T, method, url string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP %s %s: %v", method, url, err)
	}
	return resp
}

// doRaw sends a raw string body (used for invalid-JSON tests).
func doRaw(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTTP %s %s: %v", method, url, err)
	}
	return resp
}

// pushTopology POSTs apiMinimalDoc to /topology and asserts a 200 response.
func pushTopology(t *testing.T, baseURL string) {
	t.Helper()
	resp := doRaw(t, http.MethodPost, baseURL+"/topology", apiMinimalDoc)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("push topology: want 200, got %d: %s", resp.StatusCode, b)
	}
}

// seedWorkload registers a single dummy path into the given topology's table
// and allocates it to workloadID. leaseDur=0 means no lease. Returns the table.
func seedWorkload(t *testing.T, tables *allocation.TableSet, topologyID, workloadID string, leaseDur time.Duration) *allocation.Table {
	t.Helper()
	table := tables.Get(topologyID)
	if table == nil {
		t.Fatalf("no allocation table for topology %q — was the topology pushed?", topologyID)
	}
	table.RegisterPath(&graph.Path{ID: "seed-path"})
	wl := &allocation.WorkloadAllocation{
		WorkloadID:    workloadID,
		Sharing:       graph.SharingExclusive,
		LeaseDuration: leaseDur,
	}
	if leaseDur > 0 {
		wl.LeaseExpires = time.Now().Add(leaseDur)
	}
	if err := table.AllocatePaths(wl, []string{"seed-path"}); err != nil {
		t.Fatalf("seedWorkload: %v", err)
	}
	return table
}

// --- Topology CRUD -----------------------------------------------------------

func TestTopologyPush_Success(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRaw(t, http.MethodPost, baseURL+"/topology", apiMinimalDoc)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	var body apitypes.TopologyResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TopologyID != "test-topo" {
		t.Errorf("want topology_id=test-topo, got %q", body.TopologyID)
	}
}

func TestTopologyPush_InvalidJSON(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRaw(t, http.MethodPost, baseURL+"/topology", `{not valid json}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestTopologyPush_MissingTopologyID(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRaw(t, http.MethodPost, baseURL+"/topology", `{"nodes":[]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestTopologyPush_IncrementalInvalidation(t *testing.T) {
	// Topology v1: three nodes A, B, C.
	const v1 = `{
		"topology_id": "incr-topo",
		"source":      "push",
		"nodes": [
			{"id": "node-a", "subtype": "switch"},
			{"id": "node-b", "subtype": "switch"},
			{"id": "node-c", "subtype": "switch"}
		],
		"edges": []
	}`
	// Topology v2: node-c removed.
	const v2 = `{
		"topology_id": "incr-topo",
		"source":      "push",
		"nodes": [
			{"id": "node-a", "subtype": "switch"},
			{"id": "node-b", "subtype": "switch"}
		],
		"edges": []
	}`

	tables, baseURL := newTestServer(t)

	// Push v1.
	resp := doRaw(t, http.MethodPost, baseURL+"/topology", v1)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("v1 push: want 200, got %d", resp.StatusCode)
	}

	table := tables.Get("incr-topo")
	if table == nil {
		t.Fatal("allocation table not created on first push")
	}

	// wl-affected: path traverses node-c; should be drained when c is removed.
	table.RegisterPath(&graph.Path{
		ID:        "path-through-c",
		VertexIDs: []string{"node-a", "node-c"},
	})
	if err := table.AllocatePaths(&allocation.WorkloadAllocation{
		WorkloadID: "wl-affected",
		Sharing:    graph.SharingExclusive,
	}, []string{"path-through-c"}); err != nil {
		t.Fatalf("allocate wl-affected: %v", err)
	}

	// wl-safe: path stays within node-a and node-b; should remain active.
	table.RegisterPath(&graph.Path{
		ID:        "path-ab",
		VertexIDs: []string{"node-a", "node-b"},
	})
	if err := table.AllocatePaths(&allocation.WorkloadAllocation{
		WorkloadID: "wl-safe",
		Sharing:    graph.SharingExclusive,
	}, []string{"path-ab"}); err != nil {
		t.Fatalf("allocate wl-safe: %v", err)
	}

	// Push v2 (node-c removed).
	resp2 := doRaw(t, http.MethodPost, baseURL+"/topology", v2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("v2 push: want 200, got %d", resp2.StatusCode)
	}

	// The same table instance must still be in use — not replaced.
	if tables.Get("incr-topo") != table {
		t.Error("allocation table was replaced on incremental push; expected same instance")
	}

	// wl-affected must be DRAINING.
	wlAffected, ok := table.GetWorkload("wl-affected")
	if !ok {
		t.Fatal("wl-affected not found in table")
	}
	if wlAffected.State != allocation.WorkloadDraining {
		t.Errorf("wl-affected: want DRAINING, got %s", wlAffected.State)
	}
	if wlAffected.DrainReason != allocation.DrainReasonTopologyChange {
		t.Errorf("wl-affected drain reason: want topology_change, got %s", wlAffected.DrainReason)
	}

	// wl-safe must still be ACTIVE.
	wlSafe, ok := table.GetWorkload("wl-safe")
	if !ok {
		t.Fatal("wl-safe not found in table")
	}
	if wlSafe.State != allocation.WorkloadActive {
		t.Errorf("wl-safe: want ACTIVE, got %s", wlSafe.State)
	}
}

func TestTopologyList_Empty(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodGet, baseURL+"/topology", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body apitypes.TopologyListResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.TopologyIDs) != 0 {
		t.Errorf("want empty list, got %v", body.TopologyIDs)
	}
}

func TestTopologyList_AfterPush(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodGet, baseURL+"/topology", nil)
	defer resp.Body.Close()
	var body apitypes.TopologyListResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.TopologyIDs) != 1 || body.TopologyIDs[0] != "test-topo" {
		t.Errorf("want [test-topo], got %v", body.TopologyIDs)
	}
}

func TestTopologyGet_Found(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodGet, baseURL+"/topology/test-topo", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body apitypes.TopologyResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.TopologyID != "test-topo" {
		t.Errorf("want test-topo, got %q", body.TopologyID)
	}
}

func TestTopologyGet_NotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodGet, baseURL+"/topology/no-such-topo", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestTopologyDelete_Success(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodDelete, baseURL+"/topology/test-topo", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}

	// Subsequent GET must return 404.
	resp2 := doRequest(t, http.MethodGet, baseURL+"/topology/test-topo", nil)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("want 404 after delete, got %d", resp2.StatusCode)
	}
}

func TestTopologyDelete_NotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodDelete, baseURL+"/topology/no-such-topo", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// --- Path request validation -------------------------------------------------

func TestPathRequest_MissingWorkloadID(t *testing.T) {
	_, baseURL := newTestServer(t)
	req := apitypes.PathRequest{
		TopologyID: "test-topo",
		Endpoints:  []apitypes.EndpointSpec{{ID: "a"}, {ID: "b"}},
	}
	resp := doRequest(t, http.MethodPost, baseURL+"/paths/request", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for missing workload_id, got %d", resp.StatusCode)
	}
}

func TestPathRequest_MissingTopologyID(t *testing.T) {
	_, baseURL := newTestServer(t)
	req := apitypes.PathRequest{
		WorkloadID: "wl-1",
		Endpoints:  []apitypes.EndpointSpec{{ID: "a"}, {ID: "b"}},
	}
	resp := doRequest(t, http.MethodPost, baseURL+"/paths/request", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for missing topology_id, got %d", resp.StatusCode)
	}
}

func TestPathRequest_TooFewEndpoints(t *testing.T) {
	_, baseURL := newTestServer(t)
	req := apitypes.PathRequest{
		WorkloadID: "wl-1",
		TopologyID: "test-topo",
		Endpoints:  []apitypes.EndpointSpec{{ID: "only-one"}},
	}
	resp := doRequest(t, http.MethodPost, baseURL+"/paths/request", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for < 2 endpoints, got %d", resp.StatusCode)
	}
}

func TestPathRequest_TopologyNotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	req := apitypes.PathRequest{
		WorkloadID: "wl-1",
		TopologyID: "no-such-topo",
		Endpoints:  []apitypes.EndpointSpec{{ID: "a"}, {ID: "b"}},
	}
	resp := doRequest(t, http.MethodPost, baseURL+"/paths/request", req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404 for unknown topology, got %d", resp.StatusCode)
	}
}

// --- Workload status ---------------------------------------------------------

func TestWorkloadStatus_NotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodGet, baseURL+"/paths/no-such-workload", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestWorkloadStatus_Found(t *testing.T) {
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	seedWorkload(t, tables, "test-topo", "wl-status", 0)

	resp := doRequest(t, http.MethodGet, baseURL+"/paths/wl-status", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, b)
	}
	var body apitypes.WorkloadStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.WorkloadID != "wl-status" {
		t.Errorf("want WorkloadID=wl-status, got %q", body.WorkloadID)
	}
	if body.State != "active" {
		t.Errorf("want State=active, got %q", body.State)
	}
}

// --- Workload complete -------------------------------------------------------

func TestWorkloadComplete_NotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodPost, baseURL+"/paths/no-such-workload/complete", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestWorkloadComplete_Immediate(t *testing.T) {
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	table := seedWorkload(t, tables, "test-topo", "wl-complete", 0)

	req := apitypes.WorkloadCompleteRequest{Immediate: true}
	resp := doRequest(t, http.MethodPost, baseURL+"/paths/wl-complete/complete", req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}

	wl, ok := table.GetWorkload("wl-complete")
	if !ok {
		t.Fatal("workload not found after immediate complete")
	}
	if wl.State != allocation.WorkloadComplete {
		t.Errorf("want COMPLETE, got %s", wl.State)
	}

	s, _ := table.PathStateOf("seed-path")
	if s != allocation.StateFree {
		t.Errorf("want path FREE after immediate complete, got %s", s)
	}
}

func TestWorkloadComplete_Graceful(t *testing.T) {
	// Graceful complete (Immediate=false) transitions workload to DRAINING
	// immediately and starts the 30-second drain timer in the background.
	// We verify the transition and let the timer expire naturally after the
	// test exits — the goroutine is safe because it holds only table references.
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	table := seedWorkload(t, tables, "test-topo", "wl-graceful", 0)

	resp := doRequest(t, http.MethodPost, baseURL+"/paths/wl-graceful/complete",
		apitypes.WorkloadCompleteRequest{Immediate: false})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}

	wl, _ := table.GetWorkload("wl-graceful")
	if wl.State != allocation.WorkloadDraining {
		t.Errorf("want DRAINING after graceful complete, got %s", wl.State)
	}
	s, _ := table.PathStateOf("seed-path")
	if s != allocation.StateDraining {
		t.Errorf("want path DRAINING, got %s", s)
	}
}

// --- Heartbeat ---------------------------------------------------------------

func TestHeartbeat_NotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodPost, baseURL+"/paths/no-such-workload/heartbeat", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestHeartbeat_NoLease(t *testing.T) {
	// Workload without a lease cannot be heartbeated → 409.
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	seedWorkload(t, tables, "test-topo", "wl-nolease", 0)

	resp := doRequest(t, http.MethodPost, baseURL+"/paths/wl-nolease/heartbeat", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("want 409 for heartbeat on no-lease workload, got %d", resp.StatusCode)
	}
}

func TestHeartbeat_Success(t *testing.T) {
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	table := seedWorkload(t, tables, "test-topo", "wl-hb", time.Hour)

	wlBefore, _ := table.GetWorkload("wl-hb")
	before := wlBefore.LeaseExpires

	resp := doRequest(t, http.MethodPost, baseURL+"/paths/wl-hb/heartbeat", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 204, got %d: %s", resp.StatusCode, b)
	}

	wlAfter, _ := table.GetWorkload("wl-hb")
	if !wlAfter.LeaseExpires.After(before) {
		t.Errorf("heartbeat should extend expiry: before=%s after=%s", before, wlAfter.LeaseExpires)
	}
}

// --- Path state snapshot -----------------------------------------------------

func TestPathState_Empty(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodGet, baseURL+"/paths/state", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body apitypes.AllocationTableResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Topologies) != 0 {
		t.Errorf("want empty topologies list, got %v", body.Topologies)
	}
}

func TestPathState_AfterPush(t *testing.T) {
	_, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	resp := doRequest(t, http.MethodGet, baseURL+"/paths/state", nil)
	defer resp.Body.Close()
	var body apitypes.AllocationTableResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Topologies) != 1 {
		t.Errorf("want 1 topology snapshot, got %d", len(body.Topologies))
	}
}
