package pathengine

import (
	"math"

	"github.com/jalapeno/scoville/internal/graph"
)

// MetricType selects which edge attribute is used as the Dijkstra cost.
type MetricType string

const (
	MetricIGP   MetricType = "igp"
	MetricTE    MetricType = "te"
	MetricDelay MetricType = "delay"
)

// CostFunc returns the cost of traversing a LinkEdge. Returning +Inf
// effectively removes the edge from consideration.
type CostFunc func(e *graph.LinkEdge) float64

// CostFuncFor returns a CostFunc for the given metric type. Edges that are
// administratively down (zero max bandwidth when a bandwidth constraint is
// set) are assigned infinite cost.
func CostFuncFor(mt MetricType) CostFunc {
	switch mt {
	case MetricTE:
		return func(e *graph.LinkEdge) float64 {
			if e.TEMetric == 0 {
				return float64(e.IGPMetric) // fall back to IGP if TE not set
			}
			return float64(e.TEMetric)
		}
	case MetricDelay:
		return func(e *graph.LinkEdge) float64 {
			if e.UnidirDelay == 0 {
				return float64(e.IGPMetric) // fall back to IGP if delay not measured
			}
			return float64(e.UnidirDelay)
		}
	default: // MetricIGP
		return func(e *graph.LinkEdge) float64 {
			return float64(e.IGPMetric)
		}
	}
}

// ExcludedSet holds the nodes, edges, and SRLG groups that must not be used
// in a path computation. It grows as paths are allocated within a workload to
// enforce disjointness constraints.
type ExcludedSet struct {
	Nodes map[string]struct{} // vertex IDs of excluded transit nodes
	Edges map[string]struct{} // edge IDs of excluded links
	SRLGs map[uint32]struct{} // excluded SRLG group numbers
}

// NewExcludedSet returns an empty ExcludedSet.
func NewExcludedSet() *ExcludedSet {
	return &ExcludedSet{
		Nodes: make(map[string]struct{}),
		Edges: make(map[string]struct{}),
		SRLGs: make(map[uint32]struct{}),
	}
}

// Clone returns a deep copy of the ExcludedSet.
func (ex *ExcludedSet) Clone() *ExcludedSet {
	c := NewExcludedSet()
	for k := range ex.Nodes {
		c.Nodes[k] = struct{}{}
	}
	for k := range ex.Edges {
		c.Edges[k] = struct{}{}
	}
	for k := range ex.SRLGs {
		c.SRLGs[k] = struct{}{}
	}
	return c
}

// AddPath records the nodes, edges, and SRLGs of a computed path into the
// excluded set. srcID and dstID are never added to the node exclusion set
// because they are shared endpoints across all pairs in a workload.
func (ex *ExcludedSet) AddPath(p *graph.Path, level graph.DisjointnessLevel, g *graph.Graph, srcID, dstID string) {
	switch level {
	case graph.DisjointnessNone:
		return

	case graph.DisjointnessLink, graph.DisjointnessSRLG:
		for _, eid := range p.EdgeIDs {
			ex.Edges[eid] = struct{}{}
			if level == graph.DisjointnessSRLG {
				if e := g.GetEdge(eid); e != nil {
					if le, ok := e.(*graph.LinkEdge); ok {
						for _, srlg := range le.SRLG {
							ex.SRLGs[srlg] = struct{}{}
						}
					}
				}
			}
		}

	case graph.DisjointnessNode:
		// Exclude all intermediate (transit) nodes — never src or dst.
		for _, vid := range p.VertexIDs {
			if vid != srcID && vid != dstID {
				ex.Nodes[vid] = struct{}{}
			}
		}
		// Also exclude links so that the SPF doesn't route back through
		// an excluded node via a different edge.
		for _, eid := range p.EdgeIDs {
			ex.Edges[eid] = struct{}{}
		}
	}
}

// EdgeAllowed returns true if the given LinkEdge may be used in a path,
// given the graph, exclusion set, and path constraints.
func EdgeAllowed(e *graph.LinkEdge, ex *ExcludedSet, c graph.PathConstraints, g *graph.Graph) bool {
	// Excluded edge ID.
	if _, excluded := ex.Edges[e.GetID()]; excluded {
		return false
	}

	// SRLG exclusion: if any of the edge's SRLG groups are excluded, block it.
	for _, srlg := range e.SRLG {
		if _, excluded := ex.SRLGs[srlg]; excluded {
			return false
		}
	}

	// Admin group affinity: exclude bits must not be set.
	if c.ExcludeGroup != 0 && e.AdminGroup&c.ExcludeGroup != 0 {
		return false
	}

	// Admin group include: required bits must all be set (if specified).
	if c.AdminGroup != 0 && e.AdminGroup&c.AdminGroup != c.AdminGroup {
		return false
	}

	// Bandwidth: edge must have at least the required available bandwidth.
	if c.MinBandwidthBPS > 0 {
		avail := e.UnidirAvailBW
		if avail == 0 {
			avail = e.MaxBW // fall back to max if real-time data not available
		}
		if avail < c.MinBandwidthBPS {
			return false
		}
	}

	// Flex-Algo: when a specific algorithm is requested, only traverse edges
	// where both endpoint nodes participate in that algorithm. Participation is
	// indicated by the node having an SRv6 locator with the matching AlgoID —
	// the same signal BGP-LS uses to distribute Flex-Algo topology membership.
	if c.AlgoID != 0 {
		if !nodeHasAlgo(g, e.GetSrcID(), c.AlgoID) || !nodeHasAlgo(g, e.GetDstID(), c.AlgoID) {
			return false
		}
	}

	return true
}

// nodeHasAlgo reports whether the node identified by nodeID has an SRv6
// locator for the given Flex-Algo algorithm ID.
func nodeHasAlgo(g *graph.Graph, nodeID string, algoID uint8) bool {
	v := g.GetVertex(nodeID)
	if v == nil {
		return false
	}
	n, ok := v.(*graph.Node)
	if !ok {
		return false
	}
	for _, loc := range n.SRv6Locators {
		if loc.AlgoID == algoID {
			return true
		}
	}
	return false
}

// NodeAllowed returns true if a transit node may be visited.
func NodeAllowed(nodeID string, ex *ExcludedSet) bool {
	_, excluded := ex.Nodes[nodeID]
	return !excluded
}

// pathMetric computes the PathMetric for a sequence of LinkEdges.
func pathMetric(edges []*graph.LinkEdge) graph.PathMetric {
	m := graph.PathMetric{
		BottleneckBW: math.MaxUint64,
		HopCount:     len(edges),
	}
	for _, e := range edges {
		m.IGPMetric += e.IGPMetric
		m.TEMetric += e.TEMetric
		m.DelayUS += e.UnidirDelay

		bw := e.UnidirAvailBW
		if bw == 0 {
			bw = e.MaxBW
		}
		if bw > 0 && bw < m.BottleneckBW {
			m.BottleneckBW = bw
		}
	}
	if m.BottleneckBW == math.MaxUint64 {
		m.BottleneckBW = 0
	}
	return m
}
