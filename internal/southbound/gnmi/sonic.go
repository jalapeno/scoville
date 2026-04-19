package gnmi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// SONiC SRv6 CONFIG_DB schema helpers.
//
// SONiC represents SRv6 forwarding state in CONFIG_DB using two tables:
//
//	SRV6_SID_LIST|{name}
//	  "path" → JSON array of SID strings, e.g. ["fc00:0:1:e001::", "fc00:0:2::"]
//
// The gNMI path to a CONFIG_DB table entry follows the pattern:
//
//	/sonic-db:CONFIG_DB/SRV6_SID_LIST/SRV6_SID_LIST_LIST[name={name}]
//
// Policy names are derived from path IDs to ensure idempotency.

const (
	// sonicCfgPrefix is the gNMI path prefix for SONiC CONFIG_DB operations.
	sonicCfgPrefix = "/sonic-db:CONFIG_DB"

	// srv6SIDListTable is the SONiC CONFIG_DB table for SRv6 SID lists.
	srv6SIDListTable = "SRV6_SID_LIST"
)

// sonicSIDListPath returns the gNMI path for an SRv6 SID list entry.
func sonicSIDListPath(name string) string {
	return fmt.Sprintf("%s/%s/%s_LIST[name=%s]",
		sonicCfgPrefix, srv6SIDListTable, srv6SIDListTable, name)
}

// sonicPolicyName converts a path ID into a SONiC-safe policy name by
// replacing non-alphanumeric characters with underscores.
func sonicPolicyName(pathID string) string {
	var b strings.Builder
	for _, r := range pathID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return "syd_" + b.String()
}

// sonicSIDListVal is the JSON value written to SRV6_SID_LIST.
type sonicSIDListVal struct {
	Path []string `json:"path"`
}

// sonicUpsertSIDList programs or updates an SRv6 SID list in SONiC CONFIG_DB.
func sonicUpsertSIDList(ctx context.Context, client GNMIClient, pathID string, sids []string) error {
	name := sonicPolicyName(pathID)
	val, err := json.Marshal(sonicSIDListVal{Path: sids})
	if err != nil {
		return fmt.Errorf("marshal SID list for %q: %w", name, err)
	}
	return client.Set(ctx, "", "update", sonicSIDListPath(name), val)
}

// sonicDeleteSIDList removes an SRv6 SID list from SONiC CONFIG_DB.
func sonicDeleteSIDList(ctx context.Context, client GNMIClient, pathID string) error {
	name := sonicPolicyName(pathID)
	return client.Set(ctx, "", "delete", sonicSIDListPath(name), nil)
}
