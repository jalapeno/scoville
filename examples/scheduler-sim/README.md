# scoville Simulated AI Workload Scheduler

A Python scheduler that drives scoville through realistic job lifecycles —
no GPUs, no NCCL, no hardware required.

Uses only Python stdlib (no pip installs needed).

---

## What it simulates

```
scheduler.py          scoville
─────────────         ────────────────────────────────────────────────
POST /paths/request ─→ allocate SRv6 paths, return packed uSID containers
GET  /paths/.../flows ─→ retrieve outer_da per flow ("program the route")
SSE  /paths/.../events ─→ watch for drain signals (topology change etc.)
POST /paths/.../heartbeat ─→ extend lease every lease/2 seconds
POST /paths/.../complete ─→ graceful drain when training finishes
```

On a drain event the scheduler simulates path migration: it requests a
replacement workload and re-programs routes — exactly what a real NCCL
communicator or PyTorch plugin would do.

---

## Quick start

### 1. Start scoville (from repo root)

```bash
go build -o scoville ./cmd/scoville
./scoville --addr :8080
```

### 2. Run the basic scenario (push topology + one 15s job)

```bash
python3 examples/scheduler-sim/scheduler.py \
  --topology-file examples/ai-fabric/topology.json \
  --scenario basic
```

Expected output:
```
10:00:01  INFO     pushing topology from examples/ai-fabric/topology.json
10:00:01  INFO     topology loaded: ai-fabric  stats={...}
10:00:01  INFO     [sim-basic-001] starting  endpoints=['gpu-0', 'gpu-1']  duration=15s
10:00:01  INFO     [sim-basic-001] allocated 2 path(s)
10:00:01  INFO     [sim-basic-001]   leaf-1 → leaf-2  uSID=['fc00:0:e001:e002:4::']
10:00:01  INFO     [sim-basic-001]   leaf-2 → leaf-1  uSID=['fc00:0:e001:e001:3::']
10:00:01  INFO     [sim-basic-001]   PROGRAM  leaf-1→leaf-2  outer_da=fc00:0:e001:e002:4::
10:00:01  INFO     [sim-basic-001]   PROGRAM  leaf-2→leaf-1  outer_da=fc00:0:e001:e001:3::
10:00:01  INFO     [sim-basic-001] SSE  event=workload_state  state=active  reason=
10:00:16  INFO     [sim-basic-001] training done — calling /complete
10:00:16  INFO     [sim-basic-001] graceful drain started
10:00:16  INFO     [sim-basic-001] SSE  event=workload_state  state=draining  reason=workload_complete
10:00:46  INFO     [sim-basic-001] SSE  event=workload_state  state=complete  reason=workload_complete
10:00:46  INFO     [sim-basic-001] finished
```

---

## Scenarios

| Scenario     | What it exercises |
|--------------|-------------------|
| `basic`      | Single 15s job — core request / SSE / complete loop |
| `lease`      | 20s job with 10s lease — heartbeat extension |
| `concurrent` | 4 jobs running simultaneously — allocation state machine under load |
| `drain`      | 300s job — keeps running until you inject a topology change (see below) |

---

## Against the k8s deployment

```bash
# Port-forward or use the NodePort (30080).
python3 examples/scheduler-sim/scheduler.py \
  --scoville http://<node-ip>:30080 \
  --scenario concurrent
```

If scoville is already connected to BMP and has learned a live topology, skip
`--topology-file` and just set `--topology underlay`:

```bash
python3 examples/scheduler-sim/scheduler.py \
  --scoville http://<node-ip>:30080 \
  --topology underlay \
  --endpoints <node-id-1>,<node-id-2> \
  --scenario basic
```

---

## Triggering a drain mid-job (testing the drain scenario)

While the `drain` scenario is running, inject a topology change from another
terminal to see the scheduler react in real time:

```bash
# Option A: simulate a topology replacement (drains all active workloads)
curl -s -X POST http://localhost:8080/topology \
  -H 'Content-Type: application/json' \
  -d @examples/ai-fabric/topology.json > /dev/null
echo "topology replaced — watch for drain events in scheduler output"
```

With a live BMP feed, a real link failure or BGP-LS withdrawal from the
containerlab topology will automatically drain any workload whose path
traverses the affected node or link.

---

## Connecting to a real AI workload (future)

The scheduler here is a stand-in for a real job launcher. The same HTTP calls
map directly to a PyTorch plugin or NCCL communicator:

| Simulation step           | Real equivalent |
|---------------------------|-----------------|
| `POST /paths/request`     | Plugin called at `torch.distributed.init_process_group()` |
| Program `outer_da`        | `ip -6 route add ... encap seg6 mode encap segs <outer_da>` |
| SSE drain event           | NCCL communicator abort + collective restart on new paths |
| `POST /heartbeat`         | Periodic renew from training loop iteration hook |
| `POST /complete`          | Plugin called at `torch.distributed.destroy_process_group()` |
