# syd Kubernetes Deployment

Deploys syd alongside an existing GoBMP + NATS stack.

## Architecture

```
containerlab (XRd / FRR)
  └─ BMP/TCP ──→ GoBMP pod (your existing pod)
                   └─ NATS JetStream ──→ syd pod  ←── scheduler / UI
                                          (namespace: syd)
```

syd does **not** open a BMP port. It only connects **out** to NATS and
listens on HTTP :8080.

---

## Prerequisites

- GoBMP pod already running and receiving BMP from your containerlab nodes
- NATS JetStream accessible within the cluster
  - Find the NATS service: `kubectl get svc -A | grep nats`
  - Update `NATS_URL` in `configmap.yaml` to match

---

## 1. Build and push the image

From the repo root:

```bash
# Build (adjust tag and registry as needed)
docker build -t ghcr.io/jalapeno/syd:latest .

# Push to your registry
docker push ghcr.io/jalapeno/syd:latest
```

> **Note on the gobmp replace directive**: `go.mod` uses
> `replace github.com/sbezverk/gobmp => ../gobmp`. The Dockerfile copies
> `../gobmp/` into the build context. If your gobmp directory is elsewhere,
> edit the `COPY ../gobmp/` line in the Dockerfile.

For a quick local test without a registry, load directly into the cluster:

```bash
# kind
kind load docker-image syd:latest

# k3s / containerd
docker save syd:latest | ssh <node> "sudo k3s ctr images import -"
```

---

## 2. Configure NATS URL

Edit `deploy/k8s/configmap.yaml` and set `NATS_URL` to the address of your
NATS service. Common patterns:

```yaml
# NATS in the same namespace as GoBMP:
NATS_URL: "nats://nats.gobmp.svc.cluster.local:4222"

# NATS in the default namespace:
NATS_URL: "nats://nats.default.svc.cluster.local:4222"

# NATS as a NodePort (if not in k8s):
NATS_URL: "nats://192.168.1.10:4222"
```

Also set `BMP_TOPO` to match the topology ID your GoBMP instance uses
(default: `underlay`).

---

## 3. Deploy

```bash
kubectl apply -k deploy/k8s/
```

Verify:

```bash
kubectl -n syd get pods
# NAME                   READY   STATUS    RESTARTS   AGE
# syd-xxx               1/1     Running   0          30s

kubectl -n syd logs -f deployment/syd
# time=... level=INFO msg="bmp collector configured" nats_url=... topo_id=underlay
# time=... level=INFO msg="syd starting" addr=:8080 bmp=true encap_mode=host
```

---

## 4. Access the API

**From inside the cluster** (other pods, e.g. the scheduler sim):

```
http://syd.syd.svc.cluster.local:8080
```

**From your laptop / containerlab VM** (NodePort):

```bash
# Find your node IP
kubectl get nodes -o wide

curl http://<node-ip>:30080/topology
```

Or use port-forward for one-off testing:

```bash
kubectl -n syd port-forward deployment/syd 8080:8080
curl http://localhost:8080/topology
```

---

## 5. Check BMP topology ingestion

Once your containerlab nodes are sending BMP to GoBMP, syd will start
building the underlay topology. Watch the log:

```bash
kubectl -n syd logs -f deployment/syd | grep -E "topology|workload|bmp"
```

Then query the topology:

```bash
# List learned topologies
curl http://<node-ip>:30080/topology

# Inspect the underlay graph
curl http://<node-ip>:30080/topology/underlay
```

---

## 6. Run the simulated scheduler against k8s syd

```bash
# From your laptop with port-forward running:
python3 examples/scheduler-sim/scheduler.py \
  --syd http://localhost:8080 \
  --topology underlay \
  --endpoints <node-id-from-bmp>,<another-node-id> \
  --scenario basic
```

Find valid node IDs from the BMP-learned topology:

```bash
curl http://localhost:8080/topology/underlay | python3 -c \
  "import sys,json; s=json.load(sys.stdin)['stats']; print(s)"
```

---

## Updating the deployment

```bash
# After rebuilding the image:
kubectl -n syd rollout restart deployment/syd
kubectl -n syd rollout status deployment/syd
```

## Teardown

```bash
kubectl delete -k deploy/k8s/
```
