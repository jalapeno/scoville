package graph

import "github.com/jalapeno/syd/internal/srv6"

// Interface is a first-class vertex representing a physical or logical port on
// a Node. Modeling interfaces as vertices (rather than Node attributes) is
// required for accurate SRv6 uA SID computation: a uA SID encodes a specific
// (node, outgoing-interface) adjacency, so the path engine needs to reason
// about which interface a packet exits on.
//
// In BMP-sourced topologies each LSLink message produces one Interface vertex
// on the local node side. The SRv6ENDXSID field of that LSLink becomes the
// SRv6uASIDs slice here. The reverse LSLink produces the peer's Interface
// vertex.
//
// In push-via-JSON or static topologies the operator provides these fields
// directly in the topology document.
type Interface struct {
	BaseVertex

	// OwnerNodeID is the ID of the Node vertex that owns this interface.
	// An Ownership edge also expresses this relationship in the graph; this
	// field is a convenience denormalization for fast lookups.
	OwnerNodeID string `json:"owner_node_id"`

	Name      string   `json:"name,omitempty"`       // e.g. "eth0", "HundredGigE0/0/0/1"
	Addresses []string `json:"addresses,omitempty"`  // CIDR notation, e.g. ["2001:db8:ff::1/127"]

	// LinkLocalID corresponds to LSLink.LocalLinkID from BMP — an IGP-assigned
	// identifier for this interface within the adjacency advertisement.
	LinkLocalID uint32 `json:"link_local_id,omitempty"`

	// Bandwidth in bits per second.
	Bandwidth uint64 `json:"bandwidth_bps,omitempty"`

	// SRv6uASIDs holds the uA (micro-segment Adjacency) SIDs bound to this
	// interface. There may be multiple: one per Flex-Algo, or one per protection
	// type (e.g. protected vs. unprotected).
	SRv6uASIDs []srv6.UASID `json:"srv6_ua_sids,omitempty"`

	// AdjSIDs holds SR-MPLS adjacency SIDs for dual-plane or SR-MPLS fabrics.
	AdjSIDs []srv6.AdjSID `json:"adj_sids,omitempty"`
}
