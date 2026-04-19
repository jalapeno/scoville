1. On the k8s node — clone and deploy NATS first


git clone git@github.com:jalapeno/syd.git
cd syd

kubectl apply -f deploy/k8s/nats.yaml

# Wait for it to be ready
kubectl -n jalapeno rollout status deployment/nats

```
nats-server: /etc/nats/nats-server.conf:15:3: "$G" is a Reserved Account
```

# Quick sanity check — JetStream should show up
kubectl -n jalapeno port-forward svc/nats 8222:8222 &
curl -s http://localhost:8222/jsz | python3 -m json.tool | grep -E "config|memory"
kill %1
2. Redeploy GoBMP with NATS config


kubectl apply -f deploy/k8s/gobmp-collector.yaml

kubectl -n jalapeno rollout status deployment/gobmp
kubectl -n jalapeno logs -f deployment/gobmp
In the logs you should see GoBMP connecting to NATS and publishing on gobmp.parsed.* subjects once your BMP sources are pointed at it.

3. Build and deploy syd

You'll need to build the image on the node (or on your Mac and load it):


# On the k8s node, from the repo root:
docker build -t syd:latest .

# For k3s:
docker save syd:latest | sudo k3s ctr images import -

# For kind:
kind load docker-image syd:latest
Then:


# Update the NATS URL in the configmap to point at your jalapeno namespace NATS
# It should be: nats://nats.jalapeno:4222
# (the default in configmap.yaml is already set to that)

kubectl apply -k deploy/k8s/
kubectl -n syd rollout status deployment/syd
kubectl -n syd logs -f deployment/syd
You should see:


level=INFO msg="bmp collector configured" nats_url=nats://nats.jalapeno:4222
level=INFO msg="syd starting" addr=:8080 bmp=true encap_mode=host
Once the containerlab BMP streams are flowing, the topology will start populating and you can hit curl http://<node-ip>:30080/topology from your laptop.

### BMP

cisco@jalapeno-host:~/syd$ curl -s http://localhost:30080/topology/underlay/nodes | python3 -m json.tool | grep name
            "name": "xrd01"
            "name": "xrd15"
            "name": "xrd25"
            "name": "xrd08"
            "name": "xrd29"
            "name": "xrd18"
            "name": "xrd07"
            "name": "xrd09"
            "name": "xrd06"
            "name": "xrd31"
            "name": "xrd02"
            "name": "xrd32"
            "name": "xrd28"
            "name": "xrd17"
            "name": "xrd03"
            "name": "xrd16"
            "name": "xrd04"
cisco@jalapeno-host:~/syd$ curl -s -X POST http://localhost:30080/paths/request   -H 'Content-Type: application/json'   -d '{
    "topology_id": "underlay",
    "workload_id": "test-xrd01-xrd28",
    "endpoints": [
      {"id": "0000.0000.0001"},
      {"id": "0000.0000.0028"}
    ]
  }' | python3 -m json.tool
{
    "workload_id": "test-xrd01-xrd28",
    "topology_id": "underlay",
    "paths": [
        {
            "src_id": "",
            "dst_id": "",
            "segment_list": {
                "encap": "SRv6",
                "flavor": "H.Encaps.Red",
                "sids": [
                    "fc00:0:1:e002::",
                    "fc00:0:4:e004::",
                    "fc00:0:16::",
                    "fc00:0:28::"
                ]
            },
            "metric": {
                "igp_metric": 12,
                "delay_us": 65,
                "hop_count": 3
            },
            "path_id": "test-xrd01-xrd28-1776571627833027192-0"
        },
        {
            "src_id": "",
            "dst_id": "",
            "segment_list": {
                "encap": "SRv6",
                "flavor": "H.Encaps.Red",
                "sids": [
                    "fc00:0:28::",
                    "fc00:0:16::",
                    "fc00:0:4::",
                    "fc00:0:1::"
                ]
            },
            "metric": {
                "igp_metric": 3,
                "delay_us": 70,
                "hop_count": 3
            },
            "path_id": "test-xrd01-xrd28-1776571627833027192-1"
        }
    ],
    "allocation_state": {
        "paths_from_free": 0,
        "paths_from_shared": 0,
        "total_free_after": 0
    }
}
cisco@jalapeno-host:~/syd$ curl -s http://localhost:30080/paths/test-xrd01-xrd28/flows | python3 -m json.tool
{
    "workload_id": "test-xrd01-xrd28",
    "topology_id": "underlay",
    "flows": [
        {
            "src_node_id": "",
            "dst_node_id": "",
            "path_id": "test-xrd01-xrd28-1776571627833027192-0",
            "segment_list": [
                "fc00:0:1:e002::",
                "fc00:0:4:e004::",
                "fc00:0:16::",
                "fc00:0:28::"
            ],
            "encap_flavor": "H.Encaps.Red",
            "outer_da": "fc00:0:1:e002::",
            "srh_raw": "OwgEAwMAAAD8AAAAACgAAAAAAAAAAAAA/AAAAAAWAAAAAAAAAAAAAPwAAAAABOAEAAAAAAAAAAD8AAAAAAHgAgAAAAAAAAAA"
        },
        {
            "src_node_id": "",
            "dst_node_id": "",
            "path_id": "test-xrd01-xrd28-1776571627833027192-1",
            "segment_list": [
                "fc00:0:28::",
                "fc00:0:16::",
                "fc00:0:4::",
                "fc00:0:1::"
            ],
            "encap_flavor": "H.Encaps.Red",
            "outer_da": "fc00:0:28::",
            "srh_raw": "OwgEAwMAAAD8AAAAAAEAAAAAAAAAAAAA/AAAAAAEAAAAAAAAAAAAAPwAAAAAFgAAAAAAAAAAAAD8AAAAACgAAAAAAAAAAAAA"
        }
    ]
}

```
curl -s -X POST http://localhost:30080/paths/request \
  -H 'Content-Type: application/json' \
  -d '{
    "topology_id": "underlay",
    "workload_id": "test-alltoall-4",
    "endpoints": [
      {"id": "0000.0000.0001"},
      {"id": "0000.0000.0002"},
      {"id": "0000.0000.0028"},
      {"id": "0000.0000.0029"}
    ],
    "disjointness": "link",
    "pairing_mode": "all_directed"
  }' | python3 -c "
import sys, json
d = json.load(sys.stdin)
print(f'workload: {d[\"workload_id\"]}')
print(f'paths: {len(d[\"paths\"])}')
for p in d['paths']:
    sids = p['segment_list']['sids']
    print(f'  {p[\"src_id\"]} -> {p[\"dst_id\"]}  hops={p[\"metric\"][\"hop_count\"]}  sids={len(sids)}')
"
```


### Debugging gobmp-nats

```
curl -s http://localhost:30080/topology/underlay/nodes | python3 -m json.tool | grep name
```

nats cli:
```
 kubectl -n jalapeno port-forward svc/nats 4222:4222 &
```

```
curl -s 'http://localhost:8222/jsz/streams/goBMP/subjects' | python3 -m json.tool
```
```
nats -s nats://localhost:4222 consumer next goBMP   --subject gobmp.parsed.ls_node   --all --count 500 --raw 2>/dev/null   | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try:
        m = json.loads(line)
        print(m)
    except: pass"


---

## Session: uSID container packing + bug fixes

### Bug fixes applied

**1. IOS-XR uSID behavior codes (translator.go)**

IOS-XR advertises non-IANA behavior codes for uN/uA SIDs:
- `0x0030` = uN (micro-node, no function bits)
- `0x0039` = uA (micro-adjacency, 16-bit function)

These were added to `behaviorFromCode()` alongside the IANA codes `0x0041`/`0x0042`.

**2. Allocation table missing on BMP startup (main.go)**

BMP-driven topologies bypass `POST /topology`, so `allocation.NewTable` was never
called. Fixed by pre-creating the table in `main.go` when `--bmp` is enabled:

```go
tables.Put(*bmpTopo, allocation.NewTable(*bmpTopo))
```

**3. Empty topology after pod restart (collector.go)**

Durable JetStream consumers remember their ack position. After a restart the
in-memory store is empty, but the consumer resumes from where it left off and
misses all prior topology messages. Fixed by deleting consumers on startup to
force a full `DeliverAll` replay:

```go
c.deleteConsumers()  // called in Start() before subscribeAll()
```

**4. Empty src_id / dst_id in path responses (resolve.go)**

When an endpoint spec `id` resolved directly to a Node vertex, `EndpointID` was
left as an empty string. Fixed by setting `EndpointID: spec.ID` in that branch.

---

### uSID container packing

SRv6 uSID paths can be compressed into container addresses. The packing rules:

- All SIDs must share the same `LocatorBlockLen` and `LocatorNodeLen`.
- Slot width = `nodeLen + maxFuncLen` across all items.
  - All-uN path (funcLen=0): 16-bit slots, capacity = (128-blockLen) / 16
  - Mixed uA+uN or all-uA path (funcLen=16): 32-bit slots, capacity = (128-blockLen) / 32
- For F3216 (blockLen=32): all-uN capacity=6, mixed/uA capacity=3.
- Each container is packed from bytes `[blockBytes:]` onward.
- If SIDs overflow one container, additional containers are produced (for SRH).

**uA SIDStructure from SubTLVs**: `ls_link` End.X SIDs carry a SID Structure
sub-TLV (`gobmpsrv6.SIDStructure`) inside `EndXSIDTLV.SubTLVs []SubTLV`. The
translator now type-asserts this and populates the `Structure` field, which
`TryPackUSID` needs to determine the correct slot width.

---

### NATS diagnostics

```bash
# Port-forward NATS
kubectl -n jalapeno port-forward svc/nats 4222:4222 &

# Stream overview: message counts per subject
curl -s 'http://localhost:8222/jsz/streams/goBMP/subjects' | python3 -m json.tool

# Tail live ls_node messages (shows protocol_id: 1=IS-IS L1, 2=IS-IS L2)
nats -s nats://localhost:4222 sub gobmp.parsed.ls_node

# Pull all ls_node messages and print protocol_id + router ID
nats -s nats://localhost:4222 consumer next goBMP \
  --subject gobmp.parsed.ls_node --all --count 500 --raw 2>/dev/null \
  | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try:
        m = json.loads(line)
        print(m.get('protocol_id'), m.get('igp_router_id'), m.get('name'))
    except: pass"

# Check ls_link for End.X SIDs (uA)
nats -s nats://localhost:4222 consumer next goBMP \
  --subject gobmp.parsed.ls_link --all --count 500 --raw 2>/dev/null \
  | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try:
        m = json.loads(line)
        sids = m.get('srv6_endx_sid') or []
        if sids:
            print(m.get('igp_router_id'), '->', m.get('remote_igp_router_id'), [s.get('srv6_sid') for s in sids])
    except: pass"

# Check ls_srv6_sid for node locator SIDs (uN)
nats -s nats://localhost:4222 consumer next goBMP \
  --subject gobmp.parsed.ls_srv6_sid --all --count 500 --raw 2>/dev/null \
  | python3 -c "
import sys, json
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try:
        m = json.loads(line)
        print(m.get('igp_router_id'), m.get('srv6_sid'), 'behavior:', hex(m.get('srv6_endpoint_behavior', {}).get('endpoint_behavior', 0)))
    except: pass"

# Delete stale consumers manually (syd does this on startup, but useful for debugging)
nats -s nats://localhost:4222 consumer rm goBMP syd-gobmp-parsed-ls_node
nats -s nats://localhost:4222 consumer rm goBMP syd-gobmp-parsed-ls_link
nats -s nats://localhost:4222 consumer rm goBMP syd-gobmp-parsed-ls_srv6_sid
nats -s nats://localhost:4222 consumer rm goBMP syd-gobmp-parsed-peer
```

---

### Path request with uSID packing — expected output

```bash
curl -s -X POST http://localhost:30080/paths/request \
  -H 'Content-Type: application/json' \
  -d '{
    "topology_id": "underlay",
    "workload_id": "test-xrd01-xrd28",
    "endpoints": [
      {"id": "0000.0000.0001"},
      {"id": "0000.0000.0028"}
    ]
  }' | python3 -m json.tool
```

Expected (F3216, mixed uA+uN forward, all-uN return):

```
Forward (xrd01→xrd28): mixed uA+uN, 32-bit slots, capacity=3 → 2 containers
  Container 1: fc00:0:1:e002:4:e004:16:0   (xrd01→uA, xrd04→uA, xrd16→uN)
  Container 2: fc00:0:28::                  (xrd28 uN, standalone)

Return (xrd28→xrd01): all-uN, 16-bit slots, capacity=6 → 1 container
  Container 1: fc00:0:28:16:4:1::           (xrd28, xrd16, xrd04, xrd01)
```

Actual output confirmed:
```json
{
    "paths": [
        {
            "src_id": "0000.0000.0001",
            "dst_id": "0000.0000.0028",
            "segment_list": {
                "sids": ["fc00:0:1:e002:4:e004:16:0", "fc00:0:28::"]
            }
        },
        {
            "src_id": "0000.0000.0028",
            "dst_id": "0000.0000.0001",
            "segment_list": {
                "sids": ["fc00:0:28:16:4:1::"]
            }
        }
    ]
}
```
