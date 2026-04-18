package graph

// EndpointSubtype classifies the kind of workload endpoint a vertex represents.
type EndpointSubtype string

const (
	ESGPU       EndpointSubtype = "gpu"
	ESNIC       EndpointSubtype = "nic"
	ESHost      EndpointSubtype = "host"
	ESVM        EndpointSubtype = "vm"
	ESPod       EndpointSubtype = "pod"
	ESContainer EndpointSubtype = "container"
	ESService   EndpointSubtype = "service"
	ESIoT       EndpointSubtype = "iot"
)

// Endpoint is a vertex that originates or terminates flows — a GPU, NIC, host,
// pod, service, or other workload entity. Endpoints are connected to Node
// vertices via Attachment edges.
//
// An Endpoint with multiple network attachments (e.g. a dual-homed host) has
// multiple Attachment edges, one per uplink, but remains a single vertex.
//
// The Metadata map carries workload-specific attributes that the path engine
// or the allocation layer can use for placement policy:
//
//	"gpu_model":   "H100"
//	"nvlink_tier": "1"         // intra-rail (0) vs inter-rail (1)
//	"rack":        "A"
//	"slot":        "3"
//	"job_id":      "train-42"  // set by the scheduler at allocation time
type Endpoint struct {
	BaseVertex
	Subtype   EndpointSubtype   `json:"subtype"`
	Name      string            `json:"name,omitempty"`
	Addresses []string          `json:"addresses,omitempty"` // IPv4 or IPv6
	Metadata  map[string]string `json:"metadata,omitempty"`
}
