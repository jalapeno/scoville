package graph

// Compose creates a new Graph named id by merging the provided source graphs
// into a single unified view.
//
// # Stitch logic
//
// BGP session edges in peer graphs use LocalBGPID (a BGP router-ID such as
// "10.0.0.6") as SrcID. IGP nodes in underlay graphs are keyed by IS-IS
// system ID ("0000.0000.0006"). These two IDs refer to the same physical
// router but don't match. The stitch resolves the discrepancy using
// graph.Node.RouterID — every IGP-derived node stores its BGP router-ID there,
// providing the cross-namespace join key.
//
// # Duplicate vertex deduplication
//
// Two kinds of stub vertices can duplicate an IGP node in a composite graph:
//
//  1. NSExternalBGP peer nodes — created when an eBGP session is seen from a
//     router that is also in the IGP domain. Their RouterID matches an IGP
//     node's RouterID (e.g. peer:10.0.0.6_10.6.6.2 duplicates xrd06).
//
//  2. Nexthop stub nodes — created by the unicast prefix handler's fallback
//     path when no peer spec is available. Their ID IS the nexthop IP address
//     (e.g. "10.0.0.6"), which equals the RouterID of the corresponding IGP
//     node.
//
// Both cases are detected by checking the routerIDToNodeID index: if a
// vertex's own ID appears as a RouterID key, or if its RouterID maps to an
// IGP node, the vertex is a duplicate. Duplicate vertices are skipped in
// pass 1; BGPReachabilityEdges that reference them have their SrcID rewritten
// to the IGP node ID so reachability is preserved.
//
// # Algorithm
//
//  1. Build a RouterID → nodeID secondary index from all VTNode vertices
//     across all source graphs (IGP nodes only; NSExternalBGP nodes excluded).
//  2. Build a dupVertexID → igpNodeID map covering both kinds of duplicates.
//  3. Pass 1 — copy all vertices, skipping vertices in the dedup map.
//  4. Pass 2 — copy all edges:
//     - ETBGPSession: rewrite SrcID from LocalBGPID to IGP node ID; drop if
//       unresolvable.
//     - ETBGPReachability: rewrite SrcID if the source vertex was a duplicate.
//     - All other types: copy verbatim; silently skip if vertices missing.
//
// # Staleness
//
// The composed graph is a point-in-time snapshot. Subsequent BMP updates to
// the source graphs are not reflected. Call Compose again (and PUT the result
// in the Store) to refresh.
func Compose(id string, sources ...*Graph) *Graph {
	out := New(id)

	// --- build RouterID → nodeID index (IGP nodes only) -------------------
	// RouterID is the BGP loopback IP stored on IGP-derived Node vertices.
	// NSExternalBGP nodes are excluded so a peer vertex for 10.0.0.6 doesn't
	// shadow the IGP node for 0000.0000.0006 in the index.
	routerIDToNodeID := make(map[string]string)
	for _, src := range sources {
		src.mu.RLock()
		for _, v := range src.vertices {
			if n, ok := v.(*Node); ok && n.RouterID != "" && n.Subtype != NSExternalBGP {
				// Skip stubs where ID == RouterID (IP-addressed, not an IGP node).
				if n.ID != n.RouterID {
					routerIDToNodeID[n.RouterID] = n.ID
				}
			}
		}
		src.mu.RUnlock()
	}

	// --- build dupVertexID → igpNodeID dedup map --------------------------
	// Covers two cases:
	//   a) NSExternalBGP peer node whose RouterID maps to a known IGP node
	//      (e.g. "peer:10.0.0.6_10.6.6.2" → "0000.0000.0006")
	//   b) Nexthop stub node whose plain-IP ID equals a known RouterID
	//      (e.g. "10.0.0.6" → "0000.0000.0006")
	dupVertexToIGPID := make(map[string]string)
	for _, src := range sources {
		src.mu.RLock()
		for _, v := range src.vertices {
			n, ok := v.(*Node)
			if !ok {
				continue
			}
			// Case (a): NSExternalBGP peer whose RouterID is a known IGP RouterID.
			if n.Subtype == NSExternalBGP && n.RouterID != "" {
				if igpID, exists := routerIDToNodeID[n.RouterID]; exists {
					dupVertexToIGPID[n.ID] = igpID
				}
				continue
			}
			// Case (b): stub node whose own ID is a known RouterID (plain IP stub).
			if igpID, exists := routerIDToNodeID[n.ID]; exists {
				dupVertexToIGPID[n.ID] = igpID
			}
		}
		src.mu.RUnlock()
	}

	// --- pre-compute nh: nodes that have at least one edge -------------------
	// An nh: nexthop stub with incoming edges is a legitimate ownership target
	// (a prefix with no known eBGP peer uses it as a fallback for path
	// resolution). An nh: stub with NO edges is an orphan left over from the
	// startup race: a prefix arrived before its peerSpec was registered,
	// creating the stub+OwnershipEdge, then a later message added a real
	// BGPReachabilityEdge and the OwnershipEdge was removed — but the stub
	// vertex was never cleaned up. Both are excluded from the composed graph
	// if they have no edges; kept if they have edges.
	nhWithEdges := make(map[string]struct{})
	for _, src := range sources {
		src.mu.RLock()
		for _, e := range src.edges {
			if dst := e.GetDstID(); len(dst) > 3 && dst[:3] == "nh:" {
				nhWithEdges[dst] = struct{}{}
			}
		}
		src.mu.RUnlock()
	}

	// --- pass 1: copy all vertices, skipping duplicates and bare stubs -----
	for _, src := range sources {
		src.mu.RLock()
		for _, v := range src.vertices {
			if _, isDup := dupVertexToIGPID[v.GetID()]; isDup {
				continue
			}
			// Drop plain stub nodes — Node vertices with no RouterID and no
			// Subtype were auto-created by UpsertBGPSession (LocalBGPID stubs
			// for the local router) or EnsureNode. In the composed graph they
			// either duplicate a full IGP node (already handled by dedup above)
			// or become floating orphans because their BGPSessionEdge is dropped
			// by the stitching logic (no matching IGP RouterID). Either way they
			// add noise without contributing connectivity.
			//
			// Exception A: nh: nexthop stubs that still have at least one
			// incoming OwnershipEdge ARE kept — they are the target of the
			// ResolvePrefix ownership fallback for prefixes with no known eBGP
			// peer. Orphaned nh: stubs (no edges) are dropped.
			//
			// Exception B: NSExternalBGP peer nodes always have a Subtype set,
			// so they are never matched by this filter.
			if n, ok := v.(*Node); ok && n.RouterID == "" && string(n.Subtype) == "" {
				id := n.ID
				if len(id) >= 3 && id[:3] == "nh:" {
					// Keep only if it has live edges in a source graph.
					if _, hasEdge := nhWithEdges[id]; !hasEdge {
						continue
					}
				} else {
					continue // plain IP stub — always drop
				}
			}
			_ = out.AddVertex(v)
		}
		src.mu.RUnlock()
	}

	// --- pass 2: copy all edges, stitching BGP sessions and dedup peers ---
	for _, src := range sources {
		src.mu.RLock()
		for _, e := range src.edges {
			switch typed := e.(type) {
			case *BGPSessionEdge:
				// Rewrite SrcID from LocalBGPID to the canonical IGP node ID.
				igpID, ok := routerIDToNodeID[typed.SrcID]
				if !ok {
					// Local end is not a known IGP node — drop this edge.
					continue
				}
				rewritten := *typed
				rewritten.SrcID = igpID
				rewritten.ID = "bgpsess:" + igpID + ":" + typed.RemoteIP
				_ = out.AddEdge(&rewritten)
			case *BGPReachabilityEdge:
				// If the source vertex was a duplicate, rewrite SrcID to the
				// IGP node ID so reachability edges remain connected.
				if igpID, isDup := dupVertexToIGPID[typed.SrcID]; isDup {
					rewritten := *typed
					rewritten.SrcID = igpID
					rewritten.ID = "bgpreach:" + igpID + ":" + typed.DstID
					_ = out.AddEdge(&rewritten)
				} else {
					_ = out.AddEdge(e)
				}
			default:
				_ = out.AddEdge(e)
			}
		}
		src.mu.RUnlock()
	}

	return out
}
