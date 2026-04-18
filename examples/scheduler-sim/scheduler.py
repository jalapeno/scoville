#!/usr/bin/env python3
"""
scoville Simulated AI Workload Scheduler
=====================================
Simulates the lifecycle of AI training jobs interacting with scoville.

Each job:
  1. Calls POST /paths/request to get SRv6 segment lists.
  2. "Programs" the routes (logs the packed uSID containers).
  3. Opens a GET /paths/{id}/events SSE stream to watch for drain signals.
  4. Sends periodic heartbeats if the workload has a lease.
  5. Calls POST /paths/{id}/complete when the job finishes.
  6. Handles drain events — simulates "migration" by requesting fresh paths.

Multiple jobs run concurrently. Run with --help for options.
"""

import argparse
import json
import logging
import random
import sys
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Optional

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

log = logging.getLogger("scheduler-sim")


@dataclass
class JobSpec:
    """Describes one simulated training job."""
    workload_id: str
    topology_id: str
    endpoints: list[str]        # graph vertex IDs or IP addresses
    duration_sec: float         # how long the "training" runs
    lease_sec: int = 0          # 0 = no lease; >0 = heartbeat every lease/2
    disjointness: str = "node"
    sharing: str = "exclusive"
    pairing_mode: str = "bidir_paired"


# ---------------------------------------------------------------------------
# HTTP helpers (stdlib-only, no external deps)
# ---------------------------------------------------------------------------

class ScovilleClient:
    def __init__(self, base_url: str, timeout: int = 10):
        self.base = base_url.rstrip("/")
        self.timeout = timeout

    def _request(self, method: str, path: str, body: Optional[dict] = None) -> dict:
        url = self.base + path
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(
            url, data=data, method=method,
            headers={"Content-Type": "application/json"} if data else {},
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                raw = resp.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            raw = e.read().decode(errors="replace")
            raise RuntimeError(f"HTTP {e.code} {method} {path}: {raw}") from e

    def push_topology(self, doc: dict) -> dict:
        return self._request("POST", "/topology", doc)

    def request_paths(self, spec: JobSpec) -> dict:
        return self._request("POST", "/paths/request", {
            "topology_id":  spec.topology_id,
            "workload_id":  spec.workload_id,
            "pairing_mode": spec.pairing_mode,
            "disjointness": spec.disjointness,
            "sharing":      spec.sharing,
            "lease_duration_seconds": spec.lease_sec,
            "endpoints":    [{"id": ep} for ep in spec.endpoints],
        })

    def heartbeat(self, workload_id: str) -> None:
        self._request("POST", f"/paths/{workload_id}/heartbeat")

    def complete(self, workload_id: str, immediate: bool = False) -> None:
        self._request("POST", f"/paths/{workload_id}/complete",
                      {"immediate": immediate})

    def flows(self, workload_id: str) -> dict:
        return self._request("GET", f"/paths/{workload_id}/flows")

    def status(self, workload_id: str) -> dict:
        return self._request("GET", f"/paths/{workload_id}")

    def events_iter(self, workload_id: str, timeout: int = 120):
        """
        Generator yielding (event_name, data_dict) from the SSE stream.
        Exits when the connection closes or timeout elapses.
        """
        url = f"{self.base}/paths/{workload_id}/events"
        req = urllib.request.Request(url)
        try:
            resp = urllib.request.urlopen(req, timeout=timeout)
        except Exception as e:
            log.warning("[%s] SSE open failed: %s", workload_id, e)
            return

        event_name = None
        try:
            for raw_line in resp:
                line = raw_line.decode().rstrip("\n").rstrip("\r")
                if line.startswith("event: "):
                    event_name = line[7:]
                elif line.startswith("data: "):
                    try:
                        data = json.loads(line[6:])
                    except json.JSONDecodeError:
                        data = {}
                    yield event_name or "message", data
                    event_name = None
                # blank line = end of event frame; handled implicitly above
        except Exception:
            pass  # connection closed by server or timeout
        finally:
            resp.close()


# ---------------------------------------------------------------------------
# Job runner
# ---------------------------------------------------------------------------

class JobRunner:
    def __init__(self, client: ScovilleClient, spec: JobSpec):
        self.client = client
        self.spec = spec
        self._stop = threading.Event()

    def run(self) -> None:
        s = self.spec
        log.info("[%s] starting  endpoints=%s  duration=%.0fs  lease=%ds",
                 s.workload_id, s.endpoints, s.duration_sec, s.lease_sec)

        # --- 1. Request paths ------------------------------------------------
        try:
            resp = self.client.request_paths(s)
        except RuntimeError as e:
            log.error("[%s] path request failed: %s", s.workload_id, e)
            return

        paths = resp.get("paths", [])
        log.info("[%s] allocated %d path(s)", s.workload_id, len(paths))
        for p in paths:
            sids = p.get("segment_list", {}).get("sids", [])
            log.info("[%s]   %s → %s  uSID=%s",
                     s.workload_id, p.get("src_id"), p.get("dst_id"), sids)

        # --- 2. Fetch flows and "program" routes (pull model) ---------------
        try:
            flows_resp = self.client.flows(s.workload_id)
            for f in flows_resp.get("flows", []):
                srh_note = " (+ SRH)" if f.get("srh_raw") else ""
                log.info("[%s]   PROGRAM  %s→%s  outer_da=%s%s",
                         s.workload_id,
                         f.get("src_node_id"), f.get("dst_node_id"),
                         f.get("outer_da"), srh_note)
        except RuntimeError as e:
            log.warning("[%s] flows fetch failed: %s", s.workload_id, e)

        # --- 3. Start heartbeat thread (if lease configured) ----------------
        hb_thread = None
        if s.lease_sec > 0:
            hb_thread = threading.Thread(
                target=self._heartbeat_loop,
                name=f"hb-{s.workload_id}",
                daemon=True,
            )
            hb_thread.start()

        # --- 4. Watch SSE stream for drain signals in a background thread ---
        sse_done = threading.Event()
        drain_received = threading.Event()

        def watch_sse():
            for evt, data in self.client.events_iter(s.workload_id, timeout=int(s.duration_sec + 30)):
                state = data.get("state", "")
                reason = data.get("drain_reason", "")
                log.info("[%s] SSE  event=%s  state=%s  reason=%s",
                         s.workload_id, evt, state, reason)
                if state == "draining" and reason:
                    drain_received.set()
                if state == "complete":
                    break
            sse_done.set()

        sse_thread = threading.Thread(target=watch_sse,
                                      name=f"sse-{s.workload_id}", daemon=True)
        sse_thread.start()

        # --- 5. Simulate training running ------------------------------------
        deadline = time.monotonic() + s.duration_sec
        while time.monotonic() < deadline and not self._stop.is_set():
            if drain_received.is_set():
                log.warning("[%s] drain received mid-job — simulating path migration",
                            s.workload_id)
                self._migrate_paths()
                drain_received.clear()
            time.sleep(0.5)

        # --- 6. Complete the workload ----------------------------------------
        self._stop.set()
        log.info("[%s] training done — calling /complete", s.workload_id)
        try:
            self.client.complete(s.workload_id, immediate=False)
            log.info("[%s] graceful drain started", s.workload_id)
        except RuntimeError as e:
            log.error("[%s] complete failed: %s", s.workload_id, e)

        # Wait for the SSE stream to deliver the 'complete' event (up to 35s).
        sse_done.wait(timeout=35)
        log.info("[%s] finished", s.workload_id)

    def _heartbeat_loop(self) -> None:
        interval = max(self.spec.lease_sec // 2, 5)
        while not self._stop.wait(timeout=interval):
            try:
                self.client.heartbeat(self.spec.workload_id)
                log.debug("[%s] heartbeat sent", self.spec.workload_id)
            except RuntimeError as e:
                log.warning("[%s] heartbeat failed: %s", self.spec.workload_id, e)

    def _migrate_paths(self) -> None:
        """
        Simulates what a real scheduler would do on a drain event:
        request a replacement workload and re-program routes.
        In a real NCCL integration this would trigger a collective
        communication restart on the new paths.
        """
        new_id = f"{self.spec.workload_id}-migrated-{int(time.time())}"
        log.info("[%s] requesting replacement workload %s", self.spec.workload_id, new_id)
        migrated_spec = JobSpec(
            workload_id=new_id,
            topology_id=self.spec.topology_id,
            endpoints=self.spec.endpoints,
            duration_sec=self.spec.duration_sec,  # not used here
            lease_sec=self.spec.lease_sec,
            disjointness=self.spec.disjointness,
            sharing=self.spec.sharing,
            pairing_mode=self.spec.pairing_mode,
        )
        try:
            resp = self.client.request_paths(migrated_spec)
            paths = resp.get("paths", [])
            log.info("[%s] migration succeeded — %d new path(s)", new_id, len(paths))
        except RuntimeError as e:
            log.error("[%s] migration request failed: %s", new_id, e)


# ---------------------------------------------------------------------------
# Built-in job scenarios
# ---------------------------------------------------------------------------

SCENARIOS = {
    "basic": "Single job, 15s, no lease — validates the core request/complete loop",
    "lease": "Single job, 20s, 10s lease — exercises heartbeat extension",
    "concurrent": "Four jobs at once, exclusive node-disjoint paths, 30s — stress-tests allocation",
    "drain": "One job that runs forever until you manually invalidate an element via scoville API",
}

def make_jobs_basic(topo: str, endpoints: list[str]) -> list[JobSpec]:
    return [JobSpec(
        workload_id="sim-basic-001",
        topology_id=topo,
        endpoints=endpoints,
        duration_sec=15,
    )]

def make_jobs_lease(topo: str, endpoints: list[str]) -> list[JobSpec]:
    return [JobSpec(
        workload_id="sim-lease-001",
        topology_id=topo,
        endpoints=endpoints,
        duration_sec=20,
        lease_sec=10,
    )]

def make_jobs_concurrent(topo: str, endpoints: list[str]) -> list[JobSpec]:
    # Stagger start times by giving each job a different duration.
    return [
        JobSpec(workload_id=f"sim-job-{i:03d}", topology_id=topo,
                endpoints=endpoints, duration_sec=20 + i*5, lease_sec=15)
        for i in range(4)
    ]

def make_jobs_drain(topo: str, endpoints: list[str]) -> list[JobSpec]:
    return [JobSpec(
        workload_id="sim-drain-001",
        topology_id=topo,
        endpoints=endpoints,
        duration_sec=300,   # long-running; watch for drain events
        lease_sec=30,
    )]


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main() -> None:
    parser = argparse.ArgumentParser(
        description="scoville simulated AI workload scheduler",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="\n".join(f"  {k:12s}  {v}" for k, v in SCENARIOS.items()),
    )
    parser.add_argument("--scoville", default="http://localhost:8080",
                        help="scoville base URL (default: http://localhost:8080)")
    parser.add_argument("--topology", default="ai-fabric",
                        help="Topology ID to use for path requests")
    parser.add_argument("--topology-file", default="",
                        help="If set, push this JSON file as the topology before running jobs")
    parser.add_argument("--endpoints", default="gpu-0,gpu-1",
                        help="Comma-separated endpoint IDs (default: gpu-0,gpu-1)")
    parser.add_argument("--scenario", default="basic",
                        choices=list(SCENARIOS.keys()),
                        help="Which scenario to run (default: basic)")
    parser.add_argument("-v", "--verbose", action="store_true",
                        help="Enable debug logging")
    args = parser.parse_args()

    logging.basicConfig(
        level=logging.DEBUG if args.verbose else logging.INFO,
        format="%(asctime)s  %(levelname)-7s  %(message)s",
        datefmt="%H:%M:%S",
    )

    client = ScovilleClient(args.scoville)
    endpoints = [e.strip() for e in args.endpoints.split(",")]

    # Optionally push a topology first.
    if args.topology_file:
        log.info("pushing topology from %s", args.topology_file)
        with open(args.topology_file) as f:
            doc = json.load(f)
        try:
            resp = client.push_topology(doc)
            log.info("topology loaded: %s  stats=%s",
                     resp.get("topology_id"), resp.get("stats"))
        except RuntimeError as e:
            log.error("topology push failed: %s", e)
            sys.exit(1)

    # Build job list for the chosen scenario.
    builders = {
        "basic":      make_jobs_basic,
        "lease":      make_jobs_lease,
        "concurrent": make_jobs_concurrent,
        "drain":      make_jobs_drain,
    }
    jobs = builders[args.scenario](args.topology, endpoints)

    log.info("scenario=%s  jobs=%d  scoville=%s", args.scenario, len(jobs), args.scoville)

    if len(jobs) == 1:
        JobRunner(client, jobs[0]).run()
    else:
        # Run all jobs concurrently, staggered by a small random delay.
        threads = []
        for job in jobs:
            delay = random.uniform(0, 2)
            def _run(j=job, d=delay):
                time.sleep(d)
                JobRunner(client, j).run()
            t = threading.Thread(target=_run, name=job.workload_id, daemon=False)
            threads.append(t)
            t.start()
        for t in threads:
            t.join()

    log.info("all jobs done")


if __name__ == "__main__":
    main()
