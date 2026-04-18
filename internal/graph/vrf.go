package graph

import "github.com/jalapeno/scoville/internal/srv6"

// VRF is a vertex representing a VRF (Virtual Routing and Forwarding) instance
// on a specific node. The (OwnerNodeID, Name) pair uniquely identifies a VRF
// within a topology.
//
// An Ownership edge connects the VRF vertex to its owning Node. Prefix vertices
// within the VRF are connected to the VRF vertex via Ownership edges.
//
// The SRv6uDTSID field holds the uDT (micro-segment Decapsulation + Table
// lookup) SID used to steer traffic into this VRF via SRv6.
type VRF struct {
	BaseVertex
	Name        string       `json:"name"`
	OwnerNodeID string       `json:"owner_node_id"`

	// Route Distinguisher and Route Targets (BGP L3VPN / RFC 4364)
	RD       string   `json:"rd,omitempty"`        // e.g. "65000:100"
	RTImport []string `json:"rt_import,omitempty"` // e.g. ["65000:100"]
	RTExport []string `json:"rt_export,omitempty"`

	// SRv6 uDT SID for steering into this VRF
	SRv6uDTSID *srv6.SID `json:"srv6_udt_sid,omitempty"`
}
