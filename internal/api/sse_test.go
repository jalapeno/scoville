package api_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jalapeno/syd/internal/allocation"
	"github.com/jalapeno/syd/internal/graph"
	"github.com/jalapeno/syd/pkg/apitypes"
)

// readSSEEvent reads the next SSE event from a bufio.Scanner that is scanning
// an event-stream response body line by line. It returns the event name and
// decoded data. An empty name means the stream ended or a blank frame arrived.
func readSSEEvent(t *testing.T, scanner *bufio.Scanner, timeout time.Duration) (event string, data apitypes.WorkloadEvent, ok bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !scanner.Scan() {
			return "", data, false
		}
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			raw := strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				t.Fatalf("SSE data unmarshal: %v (raw: %s)", err, raw)
			}
		} else if line == "" && event != "" {
			// Blank line terminates one event.
			return event, data, true
		}
	}
	return "", data, false
}

// openSSE opens a GET /paths/{workload_id}/events stream and returns a scanner
// wrapping the response body. The test registers a cleanup to close the body.
func openSSE(t *testing.T, baseURL, workloadID string) *bufio.Scanner {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/paths/"+workloadID+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// Use a client with no response timeout so the stream stays open.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("events stream: want 200, got %d", resp.StatusCode)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return bufio.NewScanner(resp.Body)
}

// seedAllocatedWorkload pushes the minimal topology, registers a path, and
// allocates it — leaving the workload active.
func seedAllocatedWorkload(t *testing.T, tables *allocation.TableSet, baseURL, workloadID string) *allocation.Table {
	t.Helper()
	pushTopology(t, baseURL)
	return seedWorkload(t, tables, "test-topo", workloadID, 0)
}

// --- SSE endpoint tests -------------------------------------------------------

func TestSSE_NotFound(t *testing.T) {
	_, baseURL := newTestServer(t)
	resp := doRequest(t, http.MethodGet, baseURL+"/paths/no-such-workload/events", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestSSE_ContentType(t *testing.T) {
	tables, baseURL := newTestServer(t)
	seedAllocatedWorkload(t, tables, baseURL, "wl-sse-ct")

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/paths/wl-sse-ct/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

func TestSSE_InitialEvent_ActiveState(t *testing.T) {
	tables, baseURL := newTestServer(t)
	seedAllocatedWorkload(t, tables, baseURL, "wl-sse-init")

	scanner := openSSE(t, baseURL, "wl-sse-init")
	evt, data, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive initial SSE event")
	}
	if evt != "workload_state" {
		t.Errorf("event name = %q, want workload_state", evt)
	}
	if data.WorkloadID != "wl-sse-init" {
		t.Errorf("workload_id = %q, want wl-sse-init", data.WorkloadID)
	}
	if data.State != "active" {
		t.Errorf("state = %q, want active", data.State)
	}
	if data.DrainReason != "" {
		t.Errorf("drain_reason = %q, want empty for active workload", data.DrainReason)
	}
}

func TestSSE_DrainEvent_WorkloadComplete(t *testing.T) {
	tables, baseURL := newTestServer(t)
	table := seedAllocatedWorkload(t, tables, baseURL, "wl-sse-drain")

	scanner := openSSE(t, baseURL, "wl-sse-drain")

	// Consume the initial "active" event.
	_, _, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive initial event")
	}

	// Drain via the table directly (simulates workload_complete).
	_ = table.DrainWorkload("wl-sse-drain")

	// Should now receive the draining event.
	evt, data, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive drain event")
	}
	if evt != "workload_state" {
		t.Errorf("event = %q, want workload_state", evt)
	}
	if data.State != "draining" {
		t.Errorf("state = %q, want draining", data.State)
	}
	if data.DrainReason != "workload_complete" {
		t.Errorf("drain_reason = %q, want workload_complete", data.DrainReason)
	}
}

func TestSSE_DrainEvent_TopologyChange(t *testing.T) {
	tables, baseURL := newTestServer(t)
	pushTopology(t, baseURL)

	// Register a path that traverses "leaf-1" before allocating the workload.
	table := tables.Get("test-topo")
	if table == nil {
		t.Fatal("no allocation table for test-topo")
	}
	table.RegisterPath(&graph.Path{ID: "seed-path-topo", VertexIDs: []string{"leaf-1"}})
	wl := &allocation.WorkloadAllocation{
		WorkloadID: "wl-sse-topo",
		Sharing:    graph.SharingExclusive,
	}
	if err := table.AllocatePaths(wl, []string{"seed-path-topo"}); err != nil {
		t.Fatalf("AllocatePaths: %v", err)
	}

	scanner := openSSE(t, baseURL, "wl-sse-topo")
	_, _, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive initial event")
	}

	// Simulate BMP withdrawal of leaf-1.
	table.InvalidateElement("leaf-1")

	evt, data, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive topology-change drain event")
	}
	_ = evt
	if data.State != "draining" {
		t.Errorf("state = %q, want draining", data.State)
	}
	if data.DrainReason != "topology_change" {
		t.Errorf("drain_reason = %q, want topology_change", data.DrainReason)
	}
}

func TestSSE_StreamClosesOnComplete(t *testing.T) {
	tables, baseURL := newTestServer(t)
	table := seedAllocatedWorkload(t, tables, baseURL, "wl-sse-close")

	scanner := openSSE(t, baseURL, "wl-sse-close")
	_, _, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive initial event")
	}

	// Drain then immediately release (Immediate=true path).
	_ = table.DrainWorkload("wl-sse-close")
	_, _, ok = readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive draining event")
	}

	_ = table.ReleaseWorkload("wl-sse-close")
	evt, data, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive complete event")
	}
	_ = evt
	if data.State != "complete" {
		t.Errorf("state = %q, want complete", data.State)
	}

	// After complete the server should close the stream; the scanner should EOF.
	done := make(chan bool, 1)
	go func() { done <- !scanner.Scan() }()
	select {
	case eof := <-done:
		if !eof {
			t.Error("expected EOF after complete event, got another line")
		}
	case <-time.After(2 * time.Second):
		t.Error("stream did not close within 2s after workload complete")
	}
}

func TestSSE_AlreadyComplete_SendsEventAndCloses(t *testing.T) {
	tables, baseURL := newTestServer(t)
	table := seedAllocatedWorkload(t, tables, baseURL, "wl-sse-done")

	// Complete the workload before opening the stream.
	_ = table.DrainWorkload("wl-sse-done")
	_ = table.ReleaseWorkload("wl-sse-done")

	scanner := openSSE(t, baseURL, "wl-sse-done")
	evt, data, ok := readSSEEvent(t, scanner, 2*time.Second)
	if !ok {
		t.Fatal("did not receive initial event for already-complete workload")
	}
	_ = evt
	if data.State != "complete" {
		t.Errorf("state = %q, want complete", data.State)
	}

	// Stream must close immediately.
	done := make(chan bool, 1)
	go func() { done <- !scanner.Scan() }()
	select {
	case eof := <-done:
		if !eof {
			t.Error("expected EOF, got another line")
		}
	case <-time.After(2 * time.Second):
		t.Error("stream did not close for already-complete workload")
	}
}

// TestSSE_WorkloadStatus_IncludesDrainReason checks that the regular status
// endpoint also surfaces the drain_reason field.
func TestSSE_WorkloadStatus_IncludesDrainReason(t *testing.T) {
	tables, baseURL := newTestServer(t)
	table := seedAllocatedWorkload(t, tables, baseURL, "wl-reason-status")

	table.InvalidateElement("leaf-1") // no match — reason stays empty

	// Seed path has no vertices; InvalidateElement won't drain it.
	// Drain manually with the topology_replaced reason via DrainAll.
	table.DrainAll()

	resp := doRequest(t, http.MethodGet, baseURL+"/paths/wl-reason-status", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body apitypes.WorkloadStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.DrainReason != "topology_replaced" {
		t.Errorf("drain_reason = %q, want topology_replaced", body.DrainReason)
	}
}
