1. On the k8s node — clone and deploy NATS first


git clone git@github.com:jalapeno/scoville.git
cd scoville

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

3. Build and deploy scoville

You'll need to build the image on the node (or on your Mac and load it):


# On the k8s node, from the repo root:
docker build -t scoville:latest .

# For k3s:
docker save scoville:latest | sudo k3s ctr images import -

# For kind:
kind load docker-image scoville:latest
Then:


# Update the NATS URL in the configmap to point at your jalapeno namespace NATS
# It should be: nats://nats.jalapeno:4222
# (the default in configmap.yaml is already set to that)

kubectl apply -k deploy/k8s/
kubectl -n scoville rollout status deployment/scoville
kubectl -n scoville logs -f deployment/scoville
You should see:


level=INFO msg="bmp collector configured" nats_url=nats://nats.jalapeno:4222
level=INFO msg="scoville starting" addr=:8080 bmp=true encap_mode=host
Once the containerlab BMP streams are flowing, the topology will start populating and you can hit curl http://<node-ip>:30080/topology from your laptop.

### BMP

cisco@jalapeno-host:~/scoville$ curl -s http://localhost:30080/topology/underlay/nodes | python3 -m json.tool | grep name
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
cisco@jalapeno-host:~/scoville$ curl -s -X POST http://localhost:30080/paths/request   -H 'Content-Type: application/json'   -d '{
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
cisco@jalapeno-host:~/scoville$ curl -s http://localhost:30080/paths/test-xrd01-xrd28/flows | python3 -m json.tool
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

