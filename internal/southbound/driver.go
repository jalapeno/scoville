// Package southbound defines the interface for programming SRv6 forwarding
// state into the dataplane, and the shared types used by all driver
// implementations.
//
// Two drivers are provided:
//
//   - noop (EncapModeHost): the host/GPU programs its own SRv6 state via the
//     pull endpoint GET /paths/{workload_id}/flows. The driver itself is a
//     no-op that always returns nil.
//
//   - gnmi (EncapModeToR): the Top-of-Rack switch performs SRv6 encapsulation.
//     The driver programs SONiC via gNMI when paths are allocated or released.
package southbound

import "context"

// EncapMode controls which entity performs SRv6 encapsulation.
type EncapMode string

const (
	// EncapModeHost is the pull model: the host/GPU programs its own SRv6
	// state. The driver is a no-op; callers use GET /paths/{workload_id}/flows
	// to retrieve segment lists and program them via setsockopt or iproute2.
	EncapModeHost EncapMode = "host"

	// EncapModeToR is the push model: the Top-of-Rack switch performs SRv6
	// encapsulation. The driver programs SONiC via gNMI when paths are
	// allocated or invalidated.
	EncapModeToR EncapMode = "tor"
)

// EncodedFlow carries the encapsulation parameters for a single src→dst flow
// within a workload, suitable for both push (gNMI) and pull (setsockopt/route)
// delivery to the dataplane.
type EncodedFlow struct {
	// SrcNodeID and DstNodeID are the graph vertex IDs of the path endpoints.
	SrcNodeID string
	DstNodeID string

	// SrcIP and DstIP are the endpoint IP addresses when known.
	SrcIP string
	DstIP string

	// PathID is the allocation table path ID, for correlation and idempotent
	// gNMI policy naming.
	PathID string

	// SegmentList is the final packed SID list produced by the path engine.
	// len==1 means a single uSID container (≤6 original uSIDs packed by
	// TryPackUSID); len>1 means multiple containers (>6 original uSIDs).
	SegmentList []string

	// EncapFlavor is the SRv6 encapsulation flavor string, one of
	// "H.Encaps.Red" (default, len==1) or "H.Encaps" (len>1).
	EncapFlavor string

	// OuterDA is the outer IPv6 destination address for the encapsulated
	// packet. Always equal to SegmentList[0] for H.Encaps.Red.
	// Empty when SegmentList is empty.
	OuterDA string

	// SRHRaw is the raw Type-4 IPv6 Routing Header (RFC 8754) for setsockopt
	// or kernel SRv6 route configuration. Non-nil only when len(SegmentList)>1.
	//
	// Format:
	//   byte 0:      Next Header (59 = No Next Header)
	//   byte 1:      Hdr Ext Len  = (8 + n*16)/8 - 1
	//   byte 2:      Routing Type = 4
	//   byte 3:      Segments Left = n-1
	//   byte 4:      Last Entry   = n-1
	//   bytes 5-7:   Flags(1) + Tag(2) = 0
	//   bytes 8+:    Segment List[0..n-1] in reversed order
	//                (SL[0]=last segment, SL[n-1]=first segment)
	SRHRaw []byte
}

// ProgramRequest carries everything the southbound driver needs to install
// forwarding state for a single workload allocation.
type ProgramRequest struct {
	WorkloadID string
	TopologyID string
	// Flows is one EncodedFlow per src→dst path in the workload.
	Flows []EncodedFlow
}

// SouthboundDriver programs SRv6 forwarding state for workload allocations.
// All implementations must be safe for concurrent use.
type SouthboundDriver interface {
	// ProgramWorkload installs SRv6 encap state for all flows in req.
	// The no-op driver always returns nil immediately.
	ProgramWorkload(ctx context.Context, req *ProgramRequest) error

	// DeleteWorkload removes all forwarding state installed for workloadID.
	DeleteWorkload(ctx context.Context, workloadID string) error
}
