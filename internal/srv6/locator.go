package srv6

// Locator represents an SRv6 locator block assigned to a node.
// A node may have multiple locators (e.g. one per Flex-Algo).
type Locator struct {
	Prefix    string `json:"prefix"`              // CIDR, e.g. "2001:db8:1:1::/48"
	AlgoID    uint8  `json:"algo_id,omitempty"`   // Flex-Algo topology this locator serves
	MetricType string `json:"metric_type,omitempty"` // "igp" | "delay" | "te"
	// Node SID (uN) derived from this locator, if known.
	// Typically the locator prefix with the function bits zeroed.
	NodeSID   *SID   `json:"node_sid,omitempty"`
}
