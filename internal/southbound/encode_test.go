package southbound

import (
	"encoding/binary"
	"net/netip"
	"testing"

	"github.com/jalapeno/scoville/internal/graph"
	"github.com/jalapeno/scoville/internal/srv6"
)

// --- helpers -----------------------------------------------------------------

func pathWith(id string, sids []string) *graph.Path {
	return &graph.Path{
		ID:    id,
		SrcID: "src",
		DstID: "dst",
		SegmentList: srv6.SegmentList{
			Encap:  srv6.EncapSRv6,
			Flavor: srv6.FlavorHEncapsRed,
			SIDs:   sids,
		},
	}
}

// --- EncodeFlows -------------------------------------------------------------

func TestEncodeFlows_Empty(t *testing.T) {
	flows := EncodeFlows(nil)
	if len(flows) != 0 {
		t.Errorf("want empty, got %d flows", len(flows))
	}
}

func TestEncodeFlows_SingleSID_NoSRH(t *testing.T) {
	// Single uSID container (≤6 original uSIDs): H.Encaps.Red, no SRH.
	sids := []string{"fc00:0:1:e001::"}
	flows := EncodeFlows([]*graph.Path{pathWith("p1", sids)})
	if len(flows) != 1 {
		t.Fatalf("want 1 flow, got %d", len(flows))
	}
	f := flows[0]
	if f.PathID != "p1" {
		t.Errorf("PathID = %q, want p1", f.PathID)
	}
	if f.OuterDA != sids[0] {
		t.Errorf("OuterDA = %q, want %q", f.OuterDA, sids[0])
	}
	if f.EncapFlavor != string(srv6.FlavorHEncapsRed) {
		t.Errorf("EncapFlavor = %q, want H.Encaps.Red", f.EncapFlavor)
	}
	if f.SRHRaw != nil {
		t.Errorf("SRHRaw should be nil for single SID, got %d bytes", len(f.SRHRaw))
	}
}

func TestEncodeFlows_MultiSID_HasSRH(t *testing.T) {
	// Multiple containers (>6 original uSIDs): SRH required.
	sids := []string{"fc00:0:1:e001:e002:e003::", "fc00:0:4::"}
	flows := EncodeFlows([]*graph.Path{pathWith("p2", sids)})
	if len(flows) != 1 {
		t.Fatalf("want 1 flow, got %d", len(flows))
	}
	f := flows[0]
	if f.OuterDA != sids[0] {
		t.Errorf("OuterDA = %q, want %q", f.OuterDA, sids[0])
	}
	if len(f.SRHRaw) == 0 {
		t.Error("SRHRaw should be non-empty for multiple SIDs")
	}
}

func TestEncodeFlows_EmptySIDList(t *testing.T) {
	flows := EncodeFlows([]*graph.Path{pathWith("p3", nil)})
	if len(flows) != 1 {
		t.Fatalf("want 1 flow, got %d", len(flows))
	}
	f := flows[0]
	if f.OuterDA != "" {
		t.Errorf("OuterDA = %q, want empty for no SIDs", f.OuterDA)
	}
	if f.SRHRaw != nil {
		t.Error("SRHRaw should be nil for empty SID list")
	}
}

func TestEncodeFlows_MultipleFlows(t *testing.T) {
	paths := []*graph.Path{
		pathWith("p1", []string{"fc00:0:1:e001::"}),
		pathWith("p2", []string{"fc00:0:2:e002::"}),
	}
	flows := EncodeFlows(paths)
	if len(flows) != 2 {
		t.Fatalf("want 2 flows, got %d", len(flows))
	}
	if flows[0].PathID != "p1" || flows[1].PathID != "p2" {
		t.Errorf("unexpected flow order: %q, %q", flows[0].PathID, flows[1].PathID)
	}
}

// --- buildSRHBytes -----------------------------------------------------------

func TestBuildSRHBytes_Format(t *testing.T) {
	sids := []string{
		"fc00:0:1:e001::",
		"fc00:0:2:e002::",
		"fc00:0:3::",
	}
	b := buildSRHBytes(sids)

	n := len(sids) // 3
	wantLen := 8 + n*16
	if len(b) != wantLen {
		t.Fatalf("len = %d, want %d", len(b), wantLen)
	}

	// Fixed header fields.
	if b[0] != 59 {
		t.Errorf("NextHeader = %d, want 59", b[0])
	}
	wantHdrExtLen := uint8((8+n*16)/8 - 1)
	if b[1] != wantHdrExtLen {
		t.Errorf("HdrExtLen = %d, want %d", b[1], wantHdrExtLen)
	}
	if b[2] != 4 {
		t.Errorf("RoutingType = %d, want 4", b[2])
	}
	if b[3] != uint8(n-1) {
		t.Errorf("SegmentsLeft = %d, want %d", b[3], n-1)
	}
	if b[4] != uint8(n-1) {
		t.Errorf("LastEntry = %d, want %d", b[4], n-1)
	}
	if b[5] != 0 {
		t.Errorf("Flags = %d, want 0", b[5])
	}
	if tag := binary.BigEndian.Uint16(b[6:8]); tag != 0 {
		t.Errorf("Tag = %d, want 0", tag)
	}

	// Segment List must be in reversed order.
	for i, sid := range sids {
		a, _ := netip.ParseAddr(sid)
		raw := a.As16()
		slot := n - 1 - i // reversed index
		got := b[8+slot*16 : 8+(slot+1)*16]
		for j := range raw {
			if got[j] != raw[j] {
				t.Errorf("SL[%d] byte %d: got %02x, want %02x (SID %q)", slot, j, got[j], raw[j], sid)
			}
		}
	}
}

func TestBuildSRHBytes_SingleSID(t *testing.T) {
	// Even a single SID can be passed (though H.Encaps.Red would skip the SRH
	// in practice; this tests the raw function in isolation).
	b := buildSRHBytes([]string{"fc00:0:1::"})
	wantLen := 8 + 1*16
	if len(b) != wantLen {
		t.Fatalf("len = %d, want %d", len(b), wantLen)
	}
	if b[3] != 0 { // segments left = n-1 = 0
		t.Errorf("SegmentsLeft = %d, want 0", b[3])
	}
}

func TestBuildSRHBytes_TwoSIDs_VerifyReversal(t *testing.T) {
	s0 := "fc00:0:1::"
	s1 := "fc00:0:2::"
	b := buildSRHBytes([]string{s0, s1})

	a0, _ := netip.ParseAddr(s0)
	a1, _ := netip.ParseAddr(s1)
	raw0 := a0.As16()
	raw1 := a1.As16()

	// SL[0] should be sids[1] = s1 (last segment).
	sl0 := b[8 : 8+16]
	for i := range raw1 {
		if sl0[i] != raw1[i] {
			t.Errorf("SL[0] mismatch at byte %d: got %02x want %02x", i, sl0[i], raw1[i])
		}
	}
	// SL[1] should be sids[0] = s0 (first segment).
	sl1 := b[8+16 : 8+32]
	for i := range raw0 {
		if sl1[i] != raw0[i] {
			t.Errorf("SL[1] mismatch at byte %d: got %02x want %02x", i, sl1[i], raw0[i])
		}
	}
}
