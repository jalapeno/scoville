package pathengine

import (
	"fmt"

	"github.com/jalapeno/syd/internal/graph"
	"github.com/jalapeno/syd/internal/srv6"
)

// BuildSegmentList constructs an SRv6 segment list for the given SPFResult.
//
// For each hop the uA SID is taken from the egress Interface vertex on the
// source node of that LinkEdge. The final segment is the uN SID of the
// destination node, anchoring the packet at the far end.
//
// When tenantID is non-empty it must be the vertex ID of a VRF in g. If that
// VRF carries a uDT SID, it is appended as the last segment, producing the
// multi-tenant carrier used by Options 1, 2b, and 3:
//
//	[uA chain] | dest-locator | uDT(Tenant-ID)
//	e.g. fc00:0:e001:e002:3042:d001::
//
// After collecting all raw SIDs the list is passed through TryPackUSID. When
// every SID carries a compatible SIDStructure (byte-aligned, common block
// length) the output is a compressed uSID container list. When structure data
// is absent or mixed the raw SIDs are returned unchanged (standard SRv6 SRH
// behaviour).
//
// Encapsulation: H.Encaps.Red throughout — the SRH is suppressed when only
// one segment remains, reducing header overhead.
func BuildSegmentList(g *graph.Graph, spf *SPFResult, algoID uint8, tenantID string) (srv6.SegmentList, error) {
	if len(spf.Edges) == 0 {
		return srv6.SegmentList{
			Encap:  srv6.EncapSRv6,
			Flavor: srv6.FlavorHEncapsRed,
		}, nil
	}

	items := make([]srv6.SIDItem, 0, len(spf.Edges)+2)

	// One uA SID per hop.
	for i, le := range spf.Edges {
		item, err := uaSIDItemForEdge(g, le, algoID)
		if err != nil {
			return srv6.SegmentList{}, fmt.Errorf("hop %d (%s→%s): %w",
				i, le.GetSrcID(), le.GetDstID(), err)
		}
		items = append(items, item)
	}

	// Destination node uN SID as the final anchor / locator.
	dstID := spf.NodeIDs[len(spf.NodeIDs)-1]
	if item, ok := nodeUNSIDItem(g, dstID, algoID); ok {
		items = append(items, item)
	}

	// Optional tenant uDT SID — appended after the locator for multi-tenant
	// carriers (Options 1, 2b, 3 from the multi-tenancy design).
	if tenantID != "" {
		if item, ok := tenantUDTSIDItem(g, tenantID); ok {
			items = append(items, item)
		}
	}

	// Attempt uSID container compression; falls back to raw SIDs gracefully.
	sids, err := srv6.TryPackUSID(items)
	if err != nil {
		// TryPackUSID only returns an error for truly malformed input; fall back.
		sids = srv6.FallbackValues(items)
	}

	return srv6.SegmentList{
		Encap:  srv6.EncapSRv6,
		Flavor: srv6.FlavorHEncapsRed,
		SIDs:   sids,
	}, nil
}

// uaSIDItemForEdge returns the SIDItem for the egress interface of a LinkEdge.
// Falls back to the source node's uN SID if no interface uA SID is found.
func uaSIDItemForEdge(g *graph.Graph, le *graph.LinkEdge, algoID uint8) (srv6.SIDItem, error) {
	if le.LocalIfaceID != "" {
		v := g.GetVertex(le.LocalIfaceID)
		if v != nil {
			if iface, ok := v.(*graph.Interface); ok {
				if item, found := pickUASIDItem(iface.SRv6uASIDs, algoID); found {
					return item, nil
				}
			}
		}
	}

	// Fall back to source node uN SID.
	if item, ok := nodeUNSIDItem(g, le.GetSrcID(), algoID); ok {
		return item, nil
	}

	return srv6.SIDItem{}, fmt.Errorf(
		"no uA SID on interface %q and no uN SID on node %q — topology missing SRv6 SID data",
		le.LocalIfaceID, le.GetSrcID(),
	)
}

// pickUASIDItem selects the uA SIDItem matching algoID from a UASID slice,
// falling back to algo 0 then the first entry.
func pickUASIDItem(sids []srv6.UASID, algoID uint8) (srv6.SIDItem, bool) {
	var fallback0, fallbackFirst *srv6.UASID
	for i := range sids {
		s := &sids[i]
		if s.AlgoID == algoID {
			return toSIDItem(&s.SID), true
		}
		if s.AlgoID == 0 && fallback0 == nil {
			fallback0 = s
		}
		if fallbackFirst == nil {
			fallbackFirst = s
		}
	}
	if fallback0 != nil {
		return toSIDItem(&fallback0.SID), true
	}
	if fallbackFirst != nil {
		return toSIDItem(&fallbackFirst.SID), true
	}
	return srv6.SIDItem{}, false
}

// nodeUNSIDItem returns the SIDItem for a node's uN SID, checking
// per-locator algo-specific SIDs first, then the top-level node SID.
func nodeUNSIDItem(g *graph.Graph, nodeID string, algoID uint8) (srv6.SIDItem, bool) {
	v := g.GetVertex(nodeID)
	if v == nil {
		return srv6.SIDItem{}, false
	}
	n, ok := v.(*graph.Node)
	if !ok {
		return srv6.SIDItem{}, false
	}
	for _, loc := range n.SRv6Locators {
		if loc.AlgoID == algoID && loc.NodeSID != nil {
			return toSIDItem(loc.NodeSID), true
		}
	}
	if n.SRv6NodeSID != nil {
		return toSIDItem(n.SRv6NodeSID), true
	}
	return srv6.SIDItem{}, false
}

// tenantUDTSIDItem returns the SIDItem for a VRF vertex's uDT SID.
// The tenantID must be the vertex ID of a VRF that carries a non-nil SRv6uDTSID.
func tenantUDTSIDItem(g *graph.Graph, tenantID string) (srv6.SIDItem, bool) {
	v := g.GetVertex(tenantID)
	if v == nil {
		return srv6.SIDItem{}, false
	}
	vrf, ok := v.(*graph.VRF)
	if !ok {
		return srv6.SIDItem{}, false
	}
	if vrf.SRv6uDTSID == nil {
		return srv6.SIDItem{}, false
	}
	return toSIDItem(vrf.SRv6uDTSID), true
}

func toSIDItem(s *srv6.SID) srv6.SIDItem {
	return srv6.SIDItem{Value: s.Value, Behavior: s.Behavior, Structure: s.Structure}
}
