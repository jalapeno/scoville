package srv6

// EncapType identifies the SR data plane.
type EncapType string

const (
	EncapSRv6   EncapType = "SRv6"
	EncapSRMPLS EncapType = "SR-MPLS"
)

// Flavor identifies the SRv6 encapsulation flavor (RFC 9252).
type Flavor string

const (
	FlavorHEncaps    Flavor = "H.Encaps"     // full SRH encapsulation
	FlavorHEncapsRed Flavor = "H.Encaps.Red" // reduced — no SRH when single SID
	FlavorHInsert    Flavor = "H.Insert"     // insert SRH into existing IPv6 packet
)

// SegmentList is an ordered list of SRv6 SIDs that, when pushed onto a
// packet, will steer it along a specific path through the network.
// SIDs[0] is the first segment to be processed (outermost/active segment).
type SegmentList struct {
	Encap  EncapType `json:"encap"`
	Flavor Flavor    `json:"flavor"`
	SIDs   []string  `json:"sids"` // ordered IPv6 address strings
}
