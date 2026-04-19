// Package srv6 defines SRv6 data types used throughout syd.
// Naming follows RFC 8986 (SRv6 Network Programming) and the micro-segment
// (uSID) draft where applicable.
package srv6

// BehaviorType identifies the SRv6 endpoint behavior bound to a SID.
// Values match the IANA "SRv6 Endpoint Behaviors" registry.
type BehaviorType string

const (
	// Node SIDs (uN — micro-segment Node)
	BehaviorEnd    BehaviorType = "End"     // basic node SID, PSP/USP optional
	BehaviorEndPSP BehaviorType = "End.PSP" // Penultimate Segment Pop
	BehaviorEndUSP BehaviorType = "End.USP" // Ultimate Segment Pop

	// Adjacency SIDs (uA — micro-segment Adjacency, bound to an interface)
	BehaviorEndX    BehaviorType = "End.X"    // L3 cross-connect
	BehaviorEndXPSP BehaviorType = "End.X.PSP"
	BehaviorEndXUSP BehaviorType = "End.X.USP"

	// Table/VRF SIDs (uDT — micro-segment Decapsulation + Table lookup)
	BehaviorEndDT4  BehaviorType = "End.DT4"  // IPv4 table lookup
	BehaviorEndDT6  BehaviorType = "End.DT6"  // IPv6 table lookup
	BehaviorEndDT46 BehaviorType = "End.DT46" // dual-stack table lookup
	BehaviorEndDT2  BehaviorType = "End.DT2"  // L2 bridge table

	// Cross-connect SIDs
	BehaviorEndDX2 BehaviorType = "End.DX2" // L2 cross-connect
	BehaviorEndDX4 BehaviorType = "End.DX4" // L3 IPv4 cross-connect
	BehaviorEndDX6 BehaviorType = "End.DX6" // L3 IPv6 cross-connect

	// Binding SID / policy head-end
	BehaviorEndB6Encaps    BehaviorType = "End.B6.Encaps"
	BehaviorEndB6EncapsRed BehaviorType = "End.B6.Encaps.Red"
)

// SIDStructure describes the bit-field layout of an SRv6 SID, per RFC 9252 / uSID draft.
// All lengths are in bits and must sum to 128.
type SIDStructure struct {
	LocatorBlockLen uint8 `json:"locator_block_len"` // typically 32 or 48
	LocatorNodeLen  uint8 `json:"locator_node_len"`  // typically 16 or 24
	FunctionLen     uint8 `json:"function_len"`      // typically 16
	ArgumentLen     uint8 `json:"argument_len"`      // 0 in most deployments
}

// SID is a generic SRv6 SID. It carries the IPv6 address, the endpoint
// behavior it implements, and an optional structure description.
type SID struct {
	Value     string        `json:"sid"`                  // IPv6 address, e.g. "2001:db8:1:1:1::"
	Behavior  BehaviorType  `json:"behavior"`
	Structure *SIDStructure `json:"structure,omitempty"`
	AlgoID    uint8         `json:"algo_id,omitempty"` // Flex-Algo ID, 0 = default SPF
}

// UASID is an SRv6 uA (micro-segment Adjacency) SID bound to a specific
// outgoing interface on a node. The AlgoID field distinguishes uA SIDs
// computed under different Flex-Algo topologies.
type UASID struct {
	SID
	Weight uint8 `json:"weight,omitempty"` // for UCMP / load-balancing
}

// AdjSID is an SR-MPLS adjacency SID, kept for dual-plane or SR-MPLS fabrics.
type AdjSID struct {
	Label    uint32 `json:"label"`
	Flags    uint8  `json:"flags,omitempty"`
	Weight   uint8  `json:"weight,omitempty"`
	AlgoID   uint8  `json:"algo_id,omitempty"`
}
