package southbound

import (
	"net/netip"

	"github.com/jalapeno/scoville/internal/graph"
	"github.com/jalapeno/scoville/internal/srv6"
)

// EncodeFlows converts graph.Path objects into EncodedFlow records for
// delivery to the dataplane (gNMI push or setsockopt/route pull).
//
// Encapsulation decision:
//
//   - len(SIDs) == 0: empty flow; EncapFlavor and OuterDA are left empty.
//   - len(SIDs) == 1: single uSID container (≤6 original uSIDs packed by
//     TryPackUSID). H.Encaps.Red applies — the container is placed directly
//     in the outer IPv6 DA; no SRH is needed, SRHRaw is nil.
//   - len(SIDs) > 1: multiple uSID containers (>6 original uSIDs). H.Encaps
//     applies — outer DA = SIDs[0], and a Type-4 SRH carrying all SIDs is
//     built and returned in SRHRaw.
func EncodeFlows(paths []*graph.Path) []EncodedFlow {
	flows := make([]EncodedFlow, 0, len(paths))
	for _, p := range paths {
		flows = append(flows, encodeOne(p))
	}
	return flows
}

func encodeOne(p *graph.Path) EncodedFlow {
	sids := p.SegmentList.SIDs

	flow := EncodedFlow{
		SrcNodeID:   p.SrcID,
		DstNodeID:   p.DstID,
		PathID:      p.ID,
		SegmentList: sids,
	}

	if len(sids) == 0 {
		return flow
	}

	flow.OuterDA = sids[0]

	switch {
	case len(sids) == 1:
		// Single uSID container — H.Encaps.Red suppresses the SRH entirely.
		// The container address in the outer DA is sufficient for forwarding.
		flow.EncapFlavor = string(srv6.FlavorHEncapsRed)

	default:
		// Multiple containers (>6 original uSIDs). Build a full SRH.
		// We still label this H.Encaps.Red because the first SID goes in the
		// outer DA; the SRH carries the remaining state.
		flow.EncapFlavor = string(srv6.FlavorHEncapsRed)
		flow.SRHRaw = buildSRHBytes(sids)
	}

	return flow
}

// buildSRHBytes encodes sids into a raw Type-4 IPv6 Routing Header (RFC 8754).
//
// Wire layout (big-endian):
//
//	bytes  0    Next Header  = 59 (No Next Header)
//	bytes  1    Hdr Ext Len  = (8 + n*16)/8 - 1
//	bytes  2    Routing Type = 4
//	bytes  3    Segments Left = n-1
//	bytes  4    Last Entry   = n-1
//	bytes  5    Flags        = 0
//	bytes 6-7   Tag          = 0
//	bytes 8+    Segment List[0..n-1] — reversed: SL[0]=sids[n-1], SL[n-1]=sids[0]
//
// For H.Encaps.Red, sids[0] is placed in the outer IPv6 DA by the caller.
// The SRH includes the full segment list (including sids[0] at SL[n-1]) so
// that transit nodes have the complete path in the header. Segments Left is
// initialised to n-1; at each hop the router decrements SL and updates the
// outer DA to SL[SL].
func buildSRHBytes(sids []string) []byte {
	n := len(sids)
	buf := make([]byte, 8+n*16)

	buf[0] = 59                     // Next Header: No Next Header
	buf[1] = uint8((8+n*16)/8 - 1) // Hdr Ext Len
	buf[2] = 4                      // Routing Type: SRH
	buf[3] = uint8(n - 1)          // Segments Left
	buf[4] = uint8(n - 1)          // Last Entry (index of first segment)
	buf[5] = 0                      // Flags
	buf[6] = 0                      // Tag (high byte)
	buf[7] = 0                      // Tag (low byte)

	// Segment List is stored in reverse: SL[0] = last hop, SL[n-1] = first.
	for i, sid := range sids {
		a, err := netip.ParseAddr(sid)
		if err != nil {
			continue // leave as zeros; callers should validate SIDs upstream
		}
		raw := a.As16()
		slot := n - 1 - i // reversed index
		copy(buf[8+slot*16:8+(slot+1)*16], raw[:])
	}

	return buf
}
