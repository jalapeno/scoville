package api_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jalapeno/syd/internal/allocation"
	"github.com/jalapeno/syd/internal/graph"
	"github.com/jalapeno/syd/internal/srv6"
	"github.com/jalapeno/syd/pkg/apitypes"
)

// seedAllocatedPath registers a path with the given SIDs and allocates it to
// workloadID in the named topology. Returns the table for further manipulation.
func seedAllocatedPath(t *testing.T, tables *allocation.TableSet, topoID, workloadID string, sids []string) *allocation.Table {
	t.Helper()
	table := tables.Get(topoID)
	if table == nil {
		t.Fatalf("no allocation table for topology %q", topoID)
	}
	p := &graph.Path{
		ID:    "flow-path-" + workloadID,
		SrcID: "src-node",
		DstID: "dst-node",
		SegmentList: srv6.SegmentList{
			Encap:  srv6.EncapSRv6,
			Flavor: srv6.FlavorHEncapsRed,
			SIDs:   sids,
		},
	}
	table.RegisterPath(p)
	wl := &allocation.WorkloadAllocation{
		WorkloadID: workloadID,
		Sharing:    graph.SharingExclusive,
	}
	if err := table.AllocatePaths(wl, []string{p.ID}); err != nil {
		t.Fatalf("seedAllocatedPath: %v", err)
	}
	return table
}

// --- /flows endpoint tests ---------------------------------------------------

func TestFlows_NotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodGet, baseURL+"/paths/no-such-workload/flows", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestFlows_SingleSID_NoSRH(t *testing.T) {
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	seedAllocatedPath(t, tables, "test-topo", "wl-flows-single",
		[]string{"fc00:0:1:e001::"})

	resp := doRequest(t, http.MethodGet, baseURL+"/paths/wl-flows-single/flows", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body apitypes.FlowsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.WorkloadID != "wl-flows-single" {
		t.Errorf("workload_id = %q, want wl-flows-single", body.WorkloadID)
	}
	if body.TopologyID != "test-topo" {
		t.Errorf("topology_id = %q, want test-topo", body.TopologyID)
	}
	if len(body.Flows) != 1 {
		t.Fatalf("want 1 flow, got %d", len(body.Flows))
	}

	f := body.Flows[0]
	if f.OuterDA != "fc00:0:1:e001::" {
		t.Errorf("outer_da = %q, want fc00:0:1:e001::", f.OuterDA)
	}
	if f.EncapFlavor != "H.Encaps.Red" {
		t.Errorf("encap_flavor = %q, want H.Encaps.Red", f.EncapFlavor)
	}
	if f.SRHRaw != "" {
		t.Errorf("srh_raw = %q, want empty for single SID", f.SRHRaw)
	}
	if len(f.SegmentList) != 1 || f.SegmentList[0] != "fc00:0:1:e001::" {
		t.Errorf("segment_list = %v, want [fc00:0:1:e001::]", f.SegmentList)
	}
}

func TestFlows_MultiSID_HasSRH(t *testing.T) {
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	sids := []string{"fc00:0:1:e001:e002:e003::", "fc00:0:4::"}
	seedAllocatedPath(t, tables, "test-topo", "wl-flows-multi", sids)

	resp := doRequest(t, http.MethodGet, baseURL+"/paths/wl-flows-multi/flows", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body apitypes.FlowsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Flows) != 1 {
		t.Fatalf("want 1 flow, got %d", len(body.Flows))
	}

	f := body.Flows[0]
	if f.OuterDA != sids[0] {
		t.Errorf("outer_da = %q, want %q", f.OuterDA, sids[0])
	}
	if f.SRHRaw == "" {
		t.Error("srh_raw must be non-empty for multiple SIDs")
	}
	// Decode and check the SRH header magic bytes.
	raw, err := base64.StdEncoding.DecodeString(f.SRHRaw)
	if err != nil {
		t.Fatalf("base64 decode srh_raw: %v", err)
	}
	if len(raw) < 8 {
		t.Fatalf("srh_raw too short: %d bytes", len(raw))
	}
	if raw[2] != 4 {
		t.Errorf("routing type = %d, want 4 (SRH)", raw[2])
	}
	if raw[3] != uint8(len(sids)-1) {
		t.Errorf("segments_left = %d, want %d", raw[3], len(sids)-1)
	}
}

func TestFlows_EmptySIDList_NoSRH(t *testing.T) {
	// A path with no SIDs (e.g. direct local delivery) should return a flow
	// with empty outer_da and no srh_raw.
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	seedAllocatedPath(t, tables, "test-topo", "wl-flows-empty", nil)

	resp := doRequest(t, http.MethodGet, baseURL+"/paths/wl-flows-empty/flows", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var body apitypes.FlowsResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Flows) != 1 {
		t.Fatalf("want 1 flow, got %d", len(body.Flows))
	}
	f := body.Flows[0]
	if f.OuterDA != "" {
		t.Errorf("outer_da = %q, want empty for no SIDs", f.OuterDA)
	}
	if f.SRHRaw != "" {
		t.Errorf("srh_raw = %q, want empty", f.SRHRaw)
	}
}

func TestFlows_ContentType(t *testing.T) {
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)
	seedAllocatedPath(t, tables, "test-topo", "wl-flows-ct", []string{"fc00:0:1::"})

	resp := doRequest(t, http.MethodGet, baseURL+"/paths/wl-flows-ct/flows", nil)
	defer resp.Body.Close()
	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}
