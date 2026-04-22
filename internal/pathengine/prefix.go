package pathengine

import (
	"fmt"

	"github.com/jalapeno/syd/internal/graph"
)

// PrefixResolution holds the result of resolving a CIDR prefix to the IGP
// border node that advertises it, along with the BGP path attributes needed
// for the final hop.
type PrefixResolution struct {
	PrefixVertexID string   // e.g. "pfx:10.0.0.0/8"
	NodeID         string   // IGP node ID to use as SPF src/dst
	BGPNexthop     string   // BGP NEXT_HOP for the external destination
	LocalPref      uint32   // best-path local preference (higher = preferred)
	ASPath         []uint32 // AS_PATH of the selected best path
}

// ResolvePrefix finds the best-path IGP border node for the given CIDR prefix
// in graph g. It is used for both egress (dst_prefix) and ingress (src_prefix)
// path requests:
//
//   - Egress: NodeID is the SRv6 path destination; traffic is SRv6-encapsulated
//     to this node, then follows BGP forwarding to BGPNexthop.
//   - Ingress: NodeID is the SRv6 path source; the border router imposes the
//     SRv6 header and forwards toward the GPU endpoint.
//
// Resolution order:
//
//  1. BGPReachabilityEdge — for externally-originated BGP prefixes. Finds all
//     inbound ETBGPReachability edges on the prefix vertex. If the edge's SrcID
//     is an NSExternalBGP peer vertex (not yet stitched to IGP), follows the
//     peer's inbound ETBGPSession edges to reach the IGP node. Best path is
//     selected by LocalPref descending, then ASPath length ascending.
//
//  2. OwnershipEdge fallback — for locally-originated prefixes with no BGP
//     reachability. Follows outbound ETOwnership edges from the prefix vertex
//     to the owning IGP node.
func ResolvePrefix(g *graph.Graph, cidr string) (*PrefixResolution, error) {
	pfxID, err := findPrefixVertex(g, cidr)
	if err != nil {
		return nil, err
	}

	// --- attempt BGP reachability resolution --------------------------------
	var best *PrefixResolution

	for _, e := range g.InEdges(pfxID) {
		reach, ok := e.(*graph.BGPReachabilityEdge)
		if !ok {
			continue
		}

		nodeID, err := resolveReachabilityNode(g, reach.SrcID)
		if err != nil {
			continue // skip unresolvable edges
		}

		res := &PrefixResolution{
			PrefixVertexID: pfxID,
			NodeID:         nodeID,
			BGPNexthop:     reach.NextHop,
			LocalPref:      reach.LocalPref,
			ASPath:         reach.ASPath,
		}

		if best == nil || isBetterPath(res, best) {
			best = res
		}
	}

	if best != nil {
		return best, nil
	}

	// --- fallback: ownership edge (locally-originated prefix) ---------------
	for _, e := range g.OutEdges(pfxID) {
		if e.GetType() != graph.ETOwnership {
			continue
		}
		v := g.GetVertex(e.GetDstID())
		if v == nil || v.GetType() != graph.VTNode {
			continue
		}
		n, ok := v.(*graph.Node)
		if !ok || n.Subtype == graph.NSExternalBGP {
			continue
		}
		return &PrefixResolution{
			PrefixVertexID: pfxID,
			NodeID:         e.GetDstID(),
		}, nil
	}

	return nil, fmt.Errorf("prefix %q has no BGP reachability or ownership edges in the topology", cidr)
}

// findPrefixVertex returns the vertex ID for the given CIDR string. It first
// tries the canonical key "pfx:<cidr>", then falls back to a linear scan of
// all VTPrefix vertices comparing the Prefix.Prefix field. Returns an error if
// no matching vertex is found.
func findPrefixVertex(g *graph.Graph, cidr string) (string, error) {
	// Fast path: canonical key.
	if v := g.GetVertex("pfx:" + cidr); v != nil {
		return v.GetID(), nil
	}
	// Slow path: linear scan (handles non-canonical CIDR formatting).
	for _, v := range g.VerticesByType(graph.VTPrefix) {
		if p, ok := v.(*graph.Prefix); ok && p.Prefix == cidr {
			return v.GetID(), nil
		}
	}
	return "", fmt.Errorf("prefix %q not found in topology", cidr)
}

// resolveReachabilityNode returns the IGP node ID that the given SrcID (from
// a BGPReachabilityEdge) represents. For IGP nodes the SrcID is used directly.
// For NSExternalBGP peer vertices, the function follows the inbound BGPSession
// edge to find the stitched IGP node.
func resolveReachabilityNode(g *graph.Graph, srcID string) (string, error) {
	v := g.GetVertex(srcID)
	if v == nil {
		return "", fmt.Errorf("vertex %q not found", srcID)
	}
	if v.GetType() != graph.VTNode {
		return "", fmt.Errorf("vertex %q is not a node", srcID)
	}
	n, ok := v.(*graph.Node)
	if !ok {
		return "", fmt.Errorf("vertex %q is not a *graph.Node", srcID)
	}

	// IGP node — use directly.
	if n.Subtype != graph.NSExternalBGP {
		return srcID, nil
	}

	// External BGP peer — find the BGPSession edge whose DstID is this peer.
	// In a composed graph, BGPSessionEdge.SrcID has already been rewritten to
	// the IGP node ID. DstID is the remote peer vertex ID.
	for _, e := range g.InEdges(srcID) {
		if e.GetType() == graph.ETBGPSession {
			igpNode := g.GetVertex(e.GetSrcID())
			if igpNode != nil && igpNode.GetType() == graph.VTNode {
				if igpN, ok := igpNode.(*graph.Node); ok && igpN.Subtype != graph.NSExternalBGP {
					return e.GetSrcID(), nil
				}
			}
		}
	}
	return "", fmt.Errorf("no IGP node found for external BGP peer %q", srcID)
}

// isBetterPath returns true if candidate is a better BGP path than current.
// Selection criteria: higher LocalPref wins; on tie, shorter ASPath wins.
func isBetterPath(candidate, current *PrefixResolution) bool {
	if candidate.LocalPref != current.LocalPref {
		return candidate.LocalPref > current.LocalPref
	}
	return len(candidate.ASPath) < len(current.ASPath)
}
