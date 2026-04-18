// Package gnmi implements a SouthboundDriver that programs SONiC switches via
// gNMI. It is used with EncapModeToR when the Top-of-Rack performs SRv6
// encapsulation on behalf of attached GPU hosts.
//
// # Target discovery
//
// The driver maps graph Node vertex IDs to gNMI target addresses using two
// mechanisms, tried in order:
//
//  1. Node annotation "management_ip" — set by the operator when pushing a
//     topology via JSON. Value format: "host:port" (e.g. "10.0.0.1:57400").
//     This is the preferred approach for statically provisioned fabrics.
//
//  2. --gnmi-target-map flag — a comma-separated list of "nodeID=host:port"
//     pairs supplied at startup. Useful for BMP-sourced topologies where the
//     operator cannot annotate nodes in-band.
//
// If neither source provides an address for a node the driver logs a warning
// and skips that flow (other flows in the same workload continue to be
// programmed).
//
// # SONiC YANG model
//
// SRv6 policies are expressed using the SONiC CONFIG_DB schema:
//
//	SRV6_SID_LIST|{policy_name}  →  { "path": [ sid1, sid2, … ] }
//	SRV6_MY_LOCALSID_TABLE|{sid} →  { "action": "H_Encaps_Red", … }
//
// Policy names are derived from the PathID to ensure idempotency across
// ProgramWorkload calls for the same allocation.
package gnmi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/jalapeno/scoville/internal/graph"
	"github.com/jalapeno/scoville/internal/southbound"
)

// GNMIClient is the minimal interface the driver needs from a gNMI transport.
// The real implementation is provided by the caller (e.g. using
// github.com/openconfig/gnmi/client or a grpc-based client). Keeping the
// interface here means the driver package compiles without pulling in gRPC.
type GNMIClient interface {
	// Set performs a gNMI Set RPC. op is "update" or "delete".
	// path is the gNMI path string; val is the JSON-encoded value (for update)
	// or nil (for delete).
	Set(ctx context.Context, target, op, path string, val []byte) error
}

// DialFunc is a factory that returns a GNMIClient for the given target address.
// The caller is responsible for closing / reusing connections.
type DialFunc func(ctx context.Context, target string) (GNMIClient, error)

// Driver is the gNMI southbound driver. It programs SONiC switches when
// workload paths are allocated (ProgramWorkload) or released (DeleteWorkload).
type Driver struct {
	store     *graph.Store
	targetMap map[string]string // nodeID → "host:port", from --gnmi-target-map
	dial      DialFunc
	log       *slog.Logger

	mu      sync.Mutex
	clients map[string]GNMIClient // target → cached client
}

// New creates a gNMI driver. targetMap provides the static nodeID→address
// mapping (from --gnmi-target-map); the store is used to look up Node
// annotations for dynamic address resolution. dial is called whenever the
// driver needs a new connection to a target.
func New(store *graph.Store, targetMap map[string]string, dial DialFunc, log *slog.Logger) *Driver {
	if targetMap == nil {
		targetMap = make(map[string]string)
	}
	return &Driver{
		store:     store,
		targetMap: targetMap,
		dial:      dial,
		log:       log,
		clients:   make(map[string]GNMIClient),
	}
}

// ParseTargetMap parses the --gnmi-target-map flag value into a map.
// Format: "nodeID1=host:port1,nodeID2=host:port2".
func ParseTargetMap(s string) (map[string]string, error) {
	m := make(map[string]string)
	if s == "" {
		return m, nil
	}
	for _, entry := range strings.Split(s, ",") {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("gnmi-target-map: invalid entry %q (want nodeID=host:port)", entry)
		}
		m[parts[0]] = parts[1]
	}
	return m, nil
}

// ProgramWorkload programs an SRv6 encap policy on the ToR for each flow.
func (d *Driver) ProgramWorkload(ctx context.Context, req *southbound.ProgramRequest) error {
	var errs []string
	for i := range req.Flows {
		f := &req.Flows[i]
		if err := d.programFlow(ctx, req.TopologyID, f); err != nil {
			d.log.Warn("gnmi: program flow failed",
				"workload_id", req.WorkloadID,
				"path_id", f.PathID,
				"error", err,
			)
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("gnmi ProgramWorkload partial failure (%d/%d flows): %s",
			len(errs), len(req.Flows), strings.Join(errs, "; "))
	}
	return nil
}

// DeleteWorkload removes all SRv6 policies installed for the workload.
func (d *Driver) DeleteWorkload(ctx context.Context, workloadID string) error {
	// Without persisting the set of programmed policies we cannot enumerate
	// what to delete here. The caller must pass ProgramRequest or track
	// installed policies externally. For now, log a warning.
	//
	// TODO: accept []EncodedFlow or store installed policy names in the driver.
	d.log.Warn("gnmi: DeleteWorkload called without flow list — policies may leak",
		"workload_id", workloadID,
	)
	return nil
}

// DeleteFlows removes the SRv6 policies for a specific set of flows.
// This is the preferred deletion path; DeleteWorkload is a fallback.
func (d *Driver) DeleteFlows(ctx context.Context, topoID string, flows []southbound.EncodedFlow) error {
	var errs []string
	for i := range flows {
		f := &flows[i]
		if err := d.deleteFlow(ctx, topoID, f); err != nil {
			d.log.Warn("gnmi: delete flow failed",
				"path_id", f.PathID,
				"error", err,
			)
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("gnmi DeleteFlows partial failure: %s", strings.Join(errs, "; "))
	}
	return nil
}

// --- internal helpers -------------------------------------------------------

func (d *Driver) programFlow(ctx context.Context, topoID string, f *southbound.EncodedFlow) error {
	if len(f.SegmentList) == 0 {
		return nil
	}
	target, err := d.resolveTarget(topoID, f.SrcNodeID)
	if err != nil {
		return err
	}
	client, err := d.clientFor(ctx, target)
	if err != nil {
		return fmt.Errorf("dial %s: %w", target, err)
	}
	return sonicUpsertSIDList(ctx, client, f.PathID, f.SegmentList)
}

func (d *Driver) deleteFlow(ctx context.Context, topoID string, f *southbound.EncodedFlow) error {
	target, err := d.resolveTarget(topoID, f.SrcNodeID)
	if err != nil {
		return err
	}
	client, err := d.clientFor(ctx, target)
	if err != nil {
		return fmt.Errorf("dial %s: %w", target, err)
	}
	return sonicDeleteSIDList(ctx, client, f.PathID)
}

// resolveTarget finds the gNMI address for nodeID.
// Priority: Node annotation "management_ip" > targetMap flag.
func (d *Driver) resolveTarget(topoID, nodeID string) (string, error) {
	// 1. Node annotation (push-via-JSON topologies).
	g := d.store.Get(topoID)
	if g != nil {
		v := g.GetVertex(nodeID)
		if v != nil {
			bv, ok := v.(*graph.Node)
			if ok && bv.Annotations != nil {
				if addr, found := bv.Annotations["management_ip"]; found && addr != "" {
					return addr, nil
				}
			}
		}
	}

	// 2. --gnmi-target-map flag.
	if addr, ok := d.targetMap[nodeID]; ok {
		return addr, nil
	}

	return "", fmt.Errorf("no gNMI target address for node %q (set annotation \"management_ip\" or --gnmi-target-map)", nodeID)
}

func (d *Driver) clientFor(ctx context.Context, target string) (GNMIClient, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c, ok := d.clients[target]; ok {
		return c, nil
	}
	c, err := d.dial(ctx, target)
	if err != nil {
		return nil, err
	}
	d.clients[target] = c
	return c, nil
}
