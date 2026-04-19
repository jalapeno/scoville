# syd — AI Fabric Quick-Start Example

This example walks through a complete lifecycle against a small 2-tier BGP-only
fabric: push a topology, request SRv6 paths for a GPU workload, retrieve the
packed uSID segment lists, and release the workload when training is done.

---

## Topology overview

```
  gpu-0 (10.0.1.1)          gpu-1 (10.0.2.1)
       |                          |
    leaf-1 ——— spine-1 ——— leaf-2
    leaf-1 ——— spine-2 ——— leaf-2
```

- **BGP-only** — no IGP, no `igp_router_id` fields. This is the typical
  hyperscale AI fabric model.
- **SRv6 uSID F3216** — 32-bit block `fc00:0::/32`, 16-bit node, 16-bit function.
- **Management IPs** for optional gNMI ToR-mode encap are in each node's
  `annotations.management_ip`. For BGP-only fabrics this is the correct way to
  tell syd where to reach each switch via gNMI — no IGP identity needed.

Node SIDs and uA SIDs:

| Node    | uN SID        | eth0 uA SID       | eth1 uA SID       |
|---------|---------------|-------------------|-------------------|
| spine-1 | fc00:0:1::    | fc00:0:1:e001::   | fc00:0:1:e002::   |
| spine-2 | fc00:0:2::    | fc00:0:2:e001::   | fc00:0:2:e002::   |
| leaf-1  | fc00:0:3::    | fc00:0:3:e001::   | fc00:0:3:e002::   |
| leaf-2  | fc00:0:4::    | fc00:0:4:e001::   | fc00:0:4:e002::   |

---

## Prerequisites

- Go 1.21 or later (`go version`)
- `curl` (or any HTTP client)

---

## 1. Build syd

From the repository root:

```bash
go build -o syd ./cmd/syd
```

---

## 2. Start syd

```bash
./syd --addr :8080
```

Expected output:
```
time=... level=INFO msg="syd starting" addr=:8080 bmp=false
```

The server is now listening on `http://localhost:8080`. BMP is disabled by
default — the push-via-JSON topology API is all you need for this example.

---

## 3. Push the topology

```bash
curl -s -X POST http://localhost:8080/topology \
  -H 'Content-Type: application/json' \
  -d @examples/ai-fabric/topology.json | jq .
```

Expected response:
```json
{
  "topology_id": "ai-fabric",
  "description": "2-tier BGP-only AI fabric — 2 spines, 2 leaves, 2 GPU hosts. SRv6 uSID F3216.",
  "stats": {
    "topology_id": "ai-fabric",
    "total_vertices": 14,
    "total_edges": 18,
    "nodes": 4,
    "interfaces": 8,
    "endpoints": 2
  }
}
```

Verify it's registered:

```bash
curl -s http://localhost:8080/topology | jq .
# → {"topology_ids":["ai-fabric"]}
```

---

## 4. Request paths for a GPU workload

This is what the AI workload scheduler (or a PyTorch plugin) would call.
`pairing_mode: "bidir_paired"` computes one forward + one reverse path per
GPU pair, with the forward and reverse paths constrained to the same physical
links. `disjointness: "node"` ensures no two pairs share a transit spine
(important for ECMP-safe all-reduce).

```bash
curl -s -X POST http://localhost:8080/paths/request \
  -H 'Content-Type: application/json' \
  -d '{
    "topology_id":  "ai-fabric",
    "workload_id":  "train-job-001",
    "pairing_mode": "bidir_paired",
    "disjointness": "node",
    "sharing":      "exclusive",
    "endpoints": [
      {"id": "gpu-0", "role": "all-to-all"},
      {"id": "gpu-1", "role": "all-to-all"}
    ]
  }' | jq .
```

Expected response (paths trimmed for brevity):
```json
{
  "workload_id": "train-job-001",
  "topology_id": "ai-fabric",
  "paths": [
    {
      "src_id": "leaf-1",
      "dst_id": "leaf-2",
      "path_id": "train-job-001-...",
      "segment_list": {
        "encap": "SRv6",
        "flavor": "H.Encaps.Red",
        "sids": ["fc00:0:e001:e002:4::"]
      },
      ...
    },
    ...
  ],
  "allocation_state": { "total_free_after": 0 }
}
```

### Understanding the packed uSID

The 3 raw SIDs for the forward path leaf-1 → spine-1 → leaf-2:

```
leaf-1 uA (e001)  fc00:0:3:e001::   → slot 0 = e001
spine-1 uA (e002) fc00:0:1:e002::   → slot 1 = e002
leaf-2 uN         fc00:0:4::         → slot 2 = 0004
```

After F3216 uSID packing into a single 128-bit container:

```
fc00:0000 | e001 | e002 | 0004 | 0000 | 0000 | 0000
= fc00:0:e001:e002:4::
```

**H.Encaps.Red** applies because all 3 raw SIDs fit in one container (≤6).
The GPU/host sets `outer_da = fc00:0:e001:e002:4::` — no SRH needed. The
three-hop path is encoded entirely in the destination address.

---

## 5. Check workload status

```bash
curl -s http://localhost:8080/paths/train-job-001 | jq .
```

```json
{
  "workload_id": "train-job-001",
  "topology_id": "ai-fabric",
  "state":       "active",
  "path_count":  2,
  "created_at":  "2026-04-14T...",
  "drain_reason": ""
}
```

---

## 6. Get flows (pull model / host encap)

For the **host encap** model (GPUs do their own SRv6 encapsulation), fetch the
segment lists. A PyTorch plugin or NCCL communicator would call this at job
start to program routes or setsockopt entries.

```bash
curl -s http://localhost:8080/paths/train-job-001/flows | jq .
```

```json
{
  "workload_id": "train-job-001",
  "topology_id": "ai-fabric",
  "flows": [
    {
      "src_node_id":   "leaf-1",
      "dst_node_id":   "leaf-2",
      "path_id":       "train-job-001-...",
      "segment_list":  ["fc00:0:e001:e002:4::"],
      "encap_flavor":  "H.Encaps.Red",
      "outer_da":      "fc00:0:e001:e002:4::"
    },
    {
      "src_node_id":   "leaf-2",
      "dst_node_id":   "leaf-1",
      "path_id":       "train-job-001-...",
      "segment_list":  ["fc00:0:e001:e001:3::"],
      "encap_flavor":  "H.Encaps.Red",
      "outer_da":      "fc00:0:e001:e001:3::"
    }
  ]
}
```

`srh_raw` is absent because both paths have a single uSID container — no SRH
is needed. The host simply sends to `outer_da`.

### Programming the route on a Linux host

```bash
# On the host attached to leaf-1, send to gpu-1 via the packed uSID:
ip -6 route add 10.0.2.1/32 \
  encap seg6 mode encap segs fc00:0:e001:e002:4:: \
  dev eth0
```

The reverse path `fc00:0:e001:e001:3::` decodes as:
- leaf-2-eth0 uA (e001, toward spine-1) + spine-1-eth0 uA (e001, toward leaf-1) + leaf-1 uN (0003)

Both directions use the same physical links (spine-1) because `bidir_paired`
mode anchors forward and reverse to the same physical path. With `disjointness: "node"`
and a second pair of GPUs, the second pair would be forced to spine-2.

For paths with `srh_raw` (>6 original uSIDs, rare), the base64 bytes contain a
ready-to-use RFC 8754 Type-4 Routing Header that can be passed directly to the
kernel or a user-space SRH builder.

---

## 7. Subscribe to state changes (SSE)

The scheduler can stream lifecycle events rather than polling:

```bash
curl -sN http://localhost:8080/paths/train-job-001/events
```

You will receive an immediate snapshot event and subsequent events on each
state transition:

```
event: workload_state
data: {"workload_id":"train-job-001","topology_id":"ai-fabric","state":"active","path_count":2,"drain_reason":""}
```

The stream closes automatically when the workload reaches `complete`.

---

## 8. Release the workload (graceful drain)

When training finishes, signal completion. The paths enter a 30-second
**draining** grace period before being returned to free — in-flight packets
clear naturally.

```bash
curl -s -X POST http://localhost:8080/paths/train-job-001/complete \
  -H 'Content-Type: application/json' \
  -d '{}' -w "%{http_code}\n"
# → 204
```

Check the transition:

```bash
curl -s http://localhost:8080/paths/train-job-001 | jq '{state,drain_reason}'
# → {"state": "draining", "drain_reason": "workload_complete"}
```

For an immediate release (no grace period):

```bash
curl -s -X POST http://localhost:8080/paths/train-job-001/complete \
  -H 'Content-Type: application/json' \
  -d '{"immediate": true}' -w "%{http_code}\n"
# → 204
```

---

## 9. Lease-based workloads (optional)

For workloads where the scheduler may crash, use a lease with a heartbeat:

```bash
# Request paths with a 60-second lease.
curl -s -X POST http://localhost:8080/paths/request \
  -H 'Content-Type: application/json' \
  -d '{
    "topology_id":            "ai-fabric",
    "workload_id":            "train-job-002",
    "pairing_mode":           "bidir_paired",
    "endpoints":              [{"id":"gpu-0"},{"id":"gpu-1"}],
    "lease_duration_seconds": 60
  }' | jq .workload_id

# Renew every 30 seconds from the scheduler:
curl -s -X POST http://localhost:8080/paths/train-job-002/heartbeat \
  -w "%{http_code}\n"
# → 204

# If the heartbeat stops, syd automatically drains the workload after 60s.
```

---

## 10. ToR encap mode (gNMI push to SONiC)

When the switch performs SRv6 encapsulation instead of the host, start syd
with a southbound driver. This requires a gNMI client library wired at
startup (see `internal/southbound/gnmi`). The topology already contains
`management_ip` annotations for each switch — no extra configuration needed.

For BMP-learned topologies where you cannot embed annotations in the topology
document (the graph is learned from the network, not pushed), use
`--gnmi-target-map` at startup:

```bash
./syd \
  --addr :8080 \
  --bmp \
  --bmp-topo underlay \
  --gnmi-target-map "spine-1=192.168.100.1:57400,spine-2=192.168.100.2:57400,leaf-1=192.168.100.3:57400,leaf-2=192.168.100.4:57400"
```

BGP-only fabrics (no IGP) work identically to IGP fabrics here — gNMI target
resolution is based on the management-plane IP in `annotations`, not on any
routing protocol identity. Nodes without an IGP router-id are fully supported.

---

## Topology scaling

The same JSON schema handles larger fabrics. For a full rail-optimized AI
cluster:

- Add more leaf switches with `"tier": "leaf"` labels.
- Add more spine switches (or super-spines for 3-tier).
- Add GPU endpoints and attachment edges per rack.
- Scale uSID addressing: with F3216 each container holds up to 6 hops, which
  covers all realistic 2-tier and 3-tier paths without an SRH.

For a 4-tier (spine-superspine) fabric where paths could exceed 6 hops in
theory, syd falls back to H.Encaps with a full SRH automatically — the
`srh_raw` field in the `/flows` response will be populated.
