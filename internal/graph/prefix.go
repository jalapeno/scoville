package graph

import "github.com/jalapeno/scoville/internal/srv6"

// PrefixType classifies how a prefix is used in the network.
type PrefixType string

const (
	PTLoopback  PrefixType = "loopback"  // node loopback / router-id prefix
	PTTransit   PrefixType = "transit"   // point-to-point link prefix
	PTAnycast   PrefixType = "anycast"   // same prefix on multiple nodes
	PTServiceVIP PrefixType = "service_vip" // load-balanced service address
	PTLocator   PrefixType = "locator"   // SRv6 locator block
)

// Prefix is a vertex representing an IP prefix or subnet. Prefixes are first-
// class vertices so that anycast destinations can be modeled correctly: a
// single Prefix vertex may have Ownership edges to multiple Node vertices.
//
// For SRv6 locators the SRv6Locator field links this vertex to the locator
// definition on the owning node.
//
// The path engine resolves a destination address to one or more Prefix vertices
// (longest-prefix match), then follows Ownership edges to find candidate nodes,
// and computes paths to those nodes.
type Prefix struct {
	BaseVertex
	Prefix    string     `json:"prefix"`              // CIDR notation, e.g. "192.0.2.0/24"
	PrefixLen int32      `json:"prefix_len"`
	PrefixType PrefixType `json:"prefix_type,omitempty"`

	// IGP attributes, populated by BMP or explicit config
	IGPMetric    uint32 `json:"igp_metric,omitempty"`
	PrefixMetric uint32 `json:"prefix_metric,omitempty"`

	// SRv6 locator, set when this prefix is an SRv6 locator block
	SRv6Locator *srv6.Locator `json:"srv6_locator,omitempty"`

	// OwnerNodeIDs is a convenience slice listing the IDs of nodes that
	// originate this prefix. For unicast prefixes this has one entry; for
	// anycast it may have many. Ownership edges in the graph are authoritative;
	// this field is a denormalization for fast lookup.
	OwnerNodeIDs []string `json:"owner_node_ids,omitempty"`
}
