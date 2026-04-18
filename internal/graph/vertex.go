// Package graph implements the in-memory typed property graph used by scoville.
//
// The graph models a network topology as a set of vertices and edges.
// Vertices represent network elements (nodes, interfaces, endpoints, prefixes,
// VRFs). Edges represent relationships between them (physical links, IGP
// adjacencies, BGP sessions, attachments, ownership).
//
// All vertex and edge types embed BaseVertex / BaseEdge, which carry the
// common identity and label fields. Callers interact with the graph via the
// Vertex and Edge interfaces, and type-assert to concrete types when they need
// type-specific fields.
package graph

// VertexType identifies the kind of network element a vertex represents.
type VertexType string

const (
	VTNode      VertexType = "node"
	VTInterface VertexType = "interface"
	VTEndpoint  VertexType = "endpoint"
	VTPrefix    VertexType = "prefix"
	VTVRF       VertexType = "vrf"
)

// Vertex is implemented by all vertex types in the graph.
type Vertex interface {
	GetID() string
	GetType() VertexType
	GetLabels() map[string]string
}

// BaseVertex holds fields common to every vertex type.
type BaseVertex struct {
	// ID is globally unique within a topology. For push-via-JSON topologies the
	// operator assigns it (e.g. "spine-1", "spine-1:eth0"). For BMP-sourced
	// vertices it is derived from the router/peer hash.
	ID string `json:"id"`

	Type VertexType `json:"type"`

	// Labels are arbitrary key/value pairs for policy, affinity, and filtering.
	// Examples: "rack": "A3", "pod": "1", "tier": "spine"
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations carry non-policy metadata (operator notes, external system IDs).
	Annotations map[string]string `json:"annotations,omitempty"`

	// Source indicates how this vertex entered the graph.
	Source TopologySource `json:"source,omitempty"`
}

func (v *BaseVertex) GetID() string                    { return v.ID }
func (v *BaseVertex) GetType() VertexType              { return v.Type }
func (v *BaseVertex) GetLabels() map[string]string     { return v.Labels }

// TopologySource records how a vertex or edge was introduced into the graph.
type TopologySource string

const (
	SourcePush   TopologySource = "push"   // operator uploaded JSON
	SourceBMP    TopologySource = "bmp"    // learned via GoBMP
	SourceGNMI   TopologySource = "gnmi"   // future
	SourceStatic TopologySource = "static" // explicit static config
)
