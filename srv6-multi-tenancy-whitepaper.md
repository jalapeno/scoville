
# Multi-Tenant Design for Hyperscale AI Factories

## SRv6 uSID Segmentation and Traffic Engineering Design Options

Bruce McDougall
April 2026
CONFIDENTIAL
 
## 1. Executive Summary

This whitepaper examines multi-tenant segmentation and traffic engineering design strategies for hyperscale AI factory networks, with Segment Routing over IPv6 (SRv6) using micro-SID (uSID) encoding as the foundational technology for tenant isolation, deterministic path steering, and scalable forwarding at up to 131,072 or more GPUs per cluster.

The document is organized around two interrelated themes. The first is the physical fabric architecture — a four-plane, two-tier Clos design in which each GPU NIC is broken out across four independent fabric planes, each plane being a complete and fully isolated leaf-spine graph. The second theme is the application of SRv6 uSID traffic engineering and multi-tenancy mechanisms on top of that planar fabric, providing deterministic path pinning, tenant isolation, and dynamic flow steering without per-flow state in the network core.

The analysis covers three SRv6 multi-tenancy design options differentiated by the location of SRv6 encapsulation and decapsulation:

•	**Option 1** — Network-Based: ingress and egress leaf switches perform SRv6 encapsulation and decapsulation, with VRF-based tenant isolation at the leaf layer. This is the most operationally familiar model, directly analogous to MPLS L3VPN in service provider networks.

•	**Option 2** — Host-Based: the GPU NIC performs SRv6 encapsulation and decapsulation, with two sub-variants — (2a) a uA-based model where the egress leaf steers the packet to the correct NIC port, and (2b) an anycast-style model where the NIC's IPv6 address is the all-zeros host in a /48 subnet, enabling address reuse across all NICs in an NVL72 chassis.

•	**Option 3** — Hybrid: the transmitting NIC performs SRv6 encapsulation and the egress leaf performs decapsulation and VRF lookup, combining the traffic engineering flexibility of host-based encapsulation with the hardware-accelerated decapsulation of the network-based model.

The paper addresses the F3216 uSID carrier format throughout, examining how its 32-bit block, 16-bit Locator, and 16-bit Function fields accommodate the addressing and steering requirements of two-tier and three-tier planar fabrics. SRv6 locator allocation strategy, GIB/LIB design, and SDN control plane integration are analyzed in depth. Fabric slicing, security boundary definitions, and the trust model implications of operator-controlled versus tenant-controlled NIC encapsulation are examined as well.

An appendix describes the three-tier multi-cluster extension of the design, scaling to 262,144 or 524,288 GPUs across four interconnected clusters via a super-spine tier, with analysis of the switch infrastructure required and the SRv6 uSID carrier implications of the additional forwarding tier.
 
## 2. Hyperscale AI Factory: Planar Fabric Architecture

This section describes the physical network fabric that underpins the multi-tenant SRv6 designs analyzed in subsequent sections. The architecture is a four-plane, two-tier Clos fabric in which each GPU NIC is broken out across four independent, physically and logically isolated fabric planes. This design is consistent with many being deployed in the current generation of hyperscale AI factories.

### 2.1 Compute Node Baseline: NVIDIA GB200 NVL72

The baseline compute node for this design is the NVIDIA GB200 NVL72 — a rack-scale, liquid-cooled system integrating 72 Blackwell GPUs and 36 Grace CPUs in a single rack. The NVL72 is unified by a fifth-generation NVLink Switch fabric providing 130 TB/s of GPU-to-GPU bandwidth within the rack, allowing all 72 GPUs to communicate at 1.8 TB/s per GPU over NVLink. This intra-rack NVLink domain is the scale-up fabric; it handles all GPU-to-GPU communication within the chassis at extremely high bandwidth and is completely transparent to the scale-out Ethernet fabric described in this paper.

A critical architectural property of the NVL72 for network fabric design purposes is that each of the 72 GPUs is equipped with its own independent 800G NIC — the same 1:1 GPU-to-NIC ratio used in prior generations. Each NIC operates as a fully independent PCIe device with its own driver instance, IP stack, and RDMA/RoCEv2 queue pairs. The NICs share a chassis but are otherwise as independent as NICs in separate physical servers.

For the purposes of network fabric design and scale calculations, the NVL72 chassis is treated as nine logical groups of eight GPUs each — a model that maps cleanly onto the 51.2T switch port math described in Section 3.3. This framing is transparent to the NVLink domain, which continues to operate as a unified 72-GPU fabric regardless of how the scale-out network addresses the individual NICs.

### 2.2 Four-Plane Fabric Design Principles

The planar fabric architecture constructs four completely independent network fabrics — planes — and connects each GPU NIC to all four planes simultaneously via a breakout of its single 800G port. Each plane is a full, independent two-tier Clos network with its own leaf tier, spine tier, routing control plane, and failure domain. There is no physical or logical fate-sharing between planes: a hardware fault, software bug, or congestion event in one plane cannot propagate to any other plane.

This independence is operationally valuable beyond fault isolation. Maintenance operations, NOS upgrades, and administrative activities can be carried out on a single plane while the remaining three planes continue to serve workloads without interruption. Different planes may even run different versions of network switch software during staged rollouts, enabling safe phased upgrades across a cluster of tens of thousands of switches.

The four planes provide a natural scaling multiplier: if a single plane supports N GPU endpoints at a given bandwidth, four planes support 4N endpoints at the same per-GPU bandwidth, with aggregate bisection bandwidth scaling proportionally. Cross-plane load balancing and failover are handled entirely by NIC firmware and host software — the network fabric itself has no visibility across plane boundaries.

GPU-to-GPU communication that remains within the same NVL72 chassis is handled exclusively by NVLink and never enters the Ethernet fabric. Inter-chassis GPU-to-GPU traffic enters the fabric at the source NIC's plane uplink, traverses the leaf-spine-leaf path within that plane, and exits at the destination NIC's corresponding plane interface. The choice of which plane to use for a given flow is made by NCCL in coordination with the NIC firmware.

### 2.3 NIC Breakout and Leaf Connectivity

Each GPU's 800G NIC is broken out into four independent links, one per plane, using structured breakout cabling. Two breakout configurations are supported, yielding two cluster scale configurations analyzed throughout this paper:

•	**Config A** — 4x200G breakout: each GPU presents one 200G uplink to each of the four planes. This configuration uses ConnectX-8 or equivalent 800G NICs and provides the highest per-GPU bandwidth per plane.
•	**Config B** — 4x100G breakout: each GPU presents one 100G uplink to each of the four planes. This configuration is consistent with ConnectX-7 (400G NIC) deployments and enables a larger GPU count per cluster at the cost of halved per-plane per-GPU bandwidth.

In both configurations, each NIC breakout link connects to a different leaf switch within the corresponding plane. No leaf switch has more than one port connected to a given NVL72 chassis. This one-port-per-chassis-per-leaf constraint is a fundamental topological property that enables the address reuse we see in multi-tenancy option 2b.

### 2.4 Two-Tier Leaf and Spine Switch Configuration

All switches in the fabric — leaf and spine — are based on 51.2 Tbps silicon with a native port radix of 512x100G. In Config A, the leaf tier uses breakout to present 128x200G downlinks to GPU NICs and 256x100G uplinks to the spine tier; in Config B, all 512 ports operate natively at 100G with 256 downlinks to GPUs and 256 uplinks to spine. Both configurations are non-blocking: leaf downlink bandwidth equals leaf uplink bandwidth with no oversubscription. Config A supports 128 GPUs per leaf; Config B supports 256. Each leaf connects to 256 different spine nodes, one uplink port per spine node.

Spine switches use all 512x100G ports as downlinks to leaves, providing full any-to-any connectivity within the plane. Each spine connects to all 512 leaves in its plane — one port per leaf — so any leaf can reach any other leaf in a single spine hop, and every leaf-to-leaf path is 3 hops: leaf → spine → leaf. Each spine node belongs exclusively to one plane-spine group with no inter-plane connectivity.

### 2.5 Cluster Scale: Two-Tier Fabric

With the leaf and spine configurations established, the per-plane and full-cluster GPU counts follow directly. The two configurations yield meaningfully different GPU scales while using identical switch infrastructure:

| Parameter | Config A (4x200G) | Config B (4x100G) |
| --- | --- | --- |
| Per-GPU plane uplink | 1x200G | 1x100G |
| GPUs per leaf | 128 | 256 |
| Leaves per plane | 512 | 512 |
| Spine nodes per plane | 256 | 256 |
| GPUs per plane | 65,536 | 131,072 |
| Total GPUs (4 planes) | 65,536 | 131,072 |
| NVL72 racks | ~910 | ~1,820 |
| Total leaves (4 planes) | 2,048 | 2,048 |
| Total spine nodes (4 planes) | 1,024 | 1,024 |
| Total fabric switches | 3,072 | 3,072 |

A key observation from this table is that the total switch infrastructure — 3,072 nodes — is identical for both configurations. The only physical difference between Config A and Config B is the NIC generation (800G vs 400G) and the breakout used at the leaf downlink tier. This means the fabric can be deployed once and support either NIC generation, providing a natural hardware upgrade path from Config B to Config A by replacing NICs rather than switches.

It is also worth noting that Config B's 131,072 GPU count is a tradeoff of greater GPU scale and less bandwidth per GPU. Config A's 65,536 GPUs represents the higher-bandwidth-per-GPU variant appropriate for workloads where per-GPU bandwidth rather than total GPU count is the primary design constraint.

### 2.6 Traffic Distribution: Packet Spraying, ECMP, and SRv6 Path Pinning

With four plane uplinks per GPU, how traffic is distributed across those planes has significant implications for both performance and correctness. Two traditional approaches exist — packet spraying and flow-level ECMP — each with meaningful limitations. SRv6 deterministic path pinning resolves the key weaknesses of both.

**Packet Spraying**

Packet spraying distributes individual packets of a single flow across multiple planes, maximizing aggregate bandwidth utilization. However, in RoCEv2-based AI fabrics it introduces serious problems: out-of-order packet delivery causes RDMA errors and retransmissions, deep NIC resequencing buffers are required, and per-flow latency jitter degrades collective operation synchronization. Addressing these issues requires proprietary solutions — NVIDIA Spectrum-X with Adaptive Routing and ConnectX-7/8 NIC resequencing, or the emerging Ultra Ethernet Consortium (UEC) standard. Packet spraying remains an active area of development but is not the production baseline.

**Flow-Level ECMP**

Flow-level ECMP assigns entire flows to a single plane and spine path via a hash of the flow address tuple. NCCL performs this assignment at the host — selecting which plane uplink to use for each GPU-to-GPU flow — making it essentially host-driven ECMP. This avoids reordering risks and works on standard Clos hardware, making it the dominant production approach today.

Its key limitation is hash collision: large, long-lived All-Reduce flows from different training jobs can hash to the same spine node simultaneously, causing queue buildup, packet loss, and ML job stalls or failures. Standard ECMP has no mechanism to detect or avoid this — the hash function is oblivious to current spine utilization.

**SRv6 Deterministic Path Pinning**

SRv6 uSID traffic engineering resolves the core weakness of flow-level ECMP without requiring proprietary hardware. By explicitly encoding the target spine node in the uA SID of the uSID carrier, the SDN controller assigns flows to specific spine nodes based on real-time utilization data — proactively avoiding collision rather than reacting to it. A flow assigned to spine node 42 via uA SID 0xFE2A always traverses spine node 42, regardless of what any hash function would select.

This determinism enables the per-tenant and per-job spine isolation described in Sections 6 and 7: the controller partitions the spine tier among tenants and jobs by programming non-overlapping uA SID sets, ensuring that training jobs never collide on shared spine capacity. The plane boundary is enforced by the NIC egress port choice; the uA SID enforces the intra-plane path. Together they provide end-to-end deterministic forwarding from source NIC to destination NIC.

| Feature | Packet Spraying | Flow-Level ECMP | SRv6 Path Pinning |
| --- | --- | --- | --- |
| Out-of-order risk | High — requires NIC resequencing | Zero — single path per flow | Zero — single path per flow |
| Collision risk | None — spread across all paths | High — hash collisions on busy spines | None — controller assigns non-overlapping paths |
| Hardware required | Spectrum-X / UEC NIC + adaptive switches | Standard Clos | Standard Clos + SDN controller |
| Bandwidth per flow | Full multi-plane aggregate | Single plane | Single plane |
| Multi-tenancy | Harder — traffic crosses all planes | Statistical — collisions possible | Strong — non-overlapping spine allocation |
| Maturity | Emerging | Production standard | Production with SDN controller |

### 2.7 Collective Operations and Full Bandwidth Utilization

The 800G aggregate bandwidth per GPU is virtually never achieved via a single GPU-to-GPU flow consuming all four plane uplinks simultaneously. Rather, it is achieved through collective operations — synchronized multi-GPU communication patterns such as All-Reduce, All-to-All, and All-Gather — coordinated by NCCL (NVIDIA Collective Communications Library), which manages the mapping of collective operations to individual GPU-to-GPU flows across the fabric. For further detail on NCCL collective operations see the NVIDIA NCCL documentation at developer.nvidia.com/nccl.

During an All-Reduce operation — the most common collective in distributed AI training — a GPU sends different chunks of gradient data to multiple peers simultaneously. Each peer flow uses one plane uplink, so a GPU engaged in All-Reduce may use all four plane uplinks at once, but each uplink carries a different flow to a different destination. The 800G aggregate emerges from four independent 200G (or 100G) flows in parallel, not from a single bonded 800G pipe to one destination. This is the fundamental design insight of the planar architecture: the four planes provide four independent communication lanes, enabling the GPU to participate in cluster-wide collective operations in parallel without any single lane becoming a bottleneck.

### 2.8 Latency Properties and Hop Count Consistency

Every GPU-to-GPU flow within a plane traverses exactly three hops regardless of cluster size: source leaf to spine, spine to destination leaf. This flat, deterministic latency profile is critical for collective operations such as All-Reduce, where the slowest participant determines the completion time of the entire operation. Full detail is provided in Appendix D.

[ Diagram placeholder: four-plane fabric topology — planes, leaves, spines, NVL72 chassis connectivity ]

 
## 3. SRv6 uSID Framework

This section describes the SRv6 micro-SID (uSID) framework as it applies to the hyperscale AI factory fabric. It covers the F3216 uSID carrier format, locator allocation strategy, the Global and Local ID Block (GIB/LIB) design, the fundamental principle of identity versus path separation, the two addressing models available for GPU NIC endpoints, and the SDN control plane model that ties the framework together. The multi-tenancy encapsulation options that build on this framework are addressed in Section 4.

### 3.1 SRv6 uSID and the F3216 Carrier Format

SRv6 Segment Routing encodes a sequence of forwarding instructions — Segment Identifiers (SIDs) — in the destination address field of a standard IPv6 header. Each SID identifies an instruction to be executed by the node that processes it: forward out a specific interface (uA — micro-Adjacency), forward to a specific next node via ECMP (uN — micro-Node), decapsulate and perform a VRF lookup (uDT — micro-Decapsulation and Table lookup), or other endpoint behaviors.

The micro-SID (uSID) encoding compresses multiple SIDs into a single 128-bit IPv6 address by allocating 16-bit slots within the address. As each transit node processes its uSID, it shifts the active SID out of the most-significant slot and shifts the remaining SIDs left — the Shift-and-Forward operation — leaving the next instruction in position for the subsequent hop. This eliminates the need for a Segment Routing Header (SRH) extension header in most cases, preserving standard IPv6 header processing performance on merchant silicon.

This paper uses the F3216 uSID carrier format exclusively. F3216 allocates the 128-bit IPv6 destination address as follows:

| Field | Bits | Position | Purpose |
| --- | --- | --- | --- |
| uSID Block (Cluster ID) | 32 | bits 0–31 | Identifies the AI factory cluster; fixed per fabric deployment |
| uSID Slot 1 | 16 | bits 32–47 | First steering or endpoint uSID (uA, uN, Locator, or Function) |
| uSID Slot 2 | 16 | bits 48–63 | Second steering or endpoint uSID |
| uSID Slot 3 | 16 | bits 64–79 | Third steering or endpoint uSID |
| uSID Slot 4 | 16 | bits 80–95 | Fourth steering or endpoint uSID |
| uSID Slot 5 | 16 | bits 96–111 | Fifth steering or endpoint uSID |
| uSID Slot 6 | 16 | bits 112–127 | Sixth steering or endpoint uSID (Locator or Function in base case) |

In the base multi-tenancy case with no traffic engineering, the carrier encodes: uSID Block | Locator (host or chassis identifier) | Function (Tenant-ID or GPU-ID). The remaining four 16-bit slots are available for steering uSIDs. In a two-tier fabric with traffic engineering, two steering uSIDs are consumed (leaf-to-spine uA and spine-to-leaf uA), leaving two slots unused. In a three-tier fabric, four steering uSIDs are consumed, filling all available slots. This slot budget is a central design constraint examined throughout the multi-tenancy sections.

Note: The paper uses SRv6 uSID exclusively. Full SRH-based SRv6 could become relevant in future scale-across scenarios connecting multiple AI factories over WAN or metro fabrics but is outside the scope of this document.

### 3.2 Locator Allocation and GIB/LIB Design

SRv6 uSID locators are allocated from a structured namespace divided into a Global ID Block (GIB) — containing locator values assigned to fabric nodes and GPU chassis — and a Local ID Block (LIB) — containing locally significant function values such as uA adjacency SIDs, uDT tenant VRF identifiers, and other per-node behaviors. Locators are assigned as globally unique values within each AI factory, ensuring that WAN and scale-across scenarios can be accommodated without address collision. Per-plane locator reuse is noted as an option in Section 4.3.

Single-Cluster Fabric Node Locators (2-tier, 4 planes)

| Fabric Tier | Nodes per Plane | Planes | Locators Required |
| --- | --- | --- | --- |
| Spine | 512 | 4 | 2,048 |
| Leaf | 256 | 4 | 1024 |
| Total (2-tier) | | | 3072 |

Four-Cluster Fabric Node Locators (3-tier, 4 planes)

| Fabric Tier | Nodes per Plane | Clusters | Locators Required |
| --- | --- | --- | --- |
| Super-spine | 1,024 | 1 (shared) | 4,096 |
| Spine | 512 | 4 | 8,192 |
| Leaf | 512 | 4 | 8,192 |
| Total (3-tier) | | | 20,480 |

**GPU Chassis Locators**

Chassis-level locators are recommended — one locator per NVL72 chassis. Intra-chassis GPU addressing is handled by the inner IPv6 destination address after uSID processing, making per-GPU locators unnecessary. The locator counts across scales are:

•	Single cluster (Config B, ~1,820 NVL72 racks x 1 plane locator each): 1,820 chassis locators
•	Four clusters (~7,280 NVL72 racks total): 7,280 chassis locators

**Proposed uSID Block Allocation**

With a 32-bit uSID block, a single /32 prefix provides 65,536 possible 16-bit uSID values (0x0000 through 0xFFFF). The value 0x0000 is the end-of-carrier marker and is not available for allocation. The following allocation is proposed, incorporating a WAN/Scale-Across reservation at the low end of the GIB to support future multi-factory connectivity:

| Category | Range | Quantity | Purpose |
| --- | --- | --- | --- |
| End of Carrier | 0x0000 | 1 | Non-functional; marks end of active uSID list |
| GIB: Reserved WAN/Scale-Across | 0x0001–0x0FFF | 4,095 | Locators reserved for WAN/inter-factory scale-across nodes |
| GIB: 4-Cluster Fabric Locators | 0x1000–0x5FFF | 20,480 | Super-spine, spine, and leaf locators across 4 clusters and 4 planes |
| GIB: Host/GPU Chassis Locators | 0x6000–0x7C6F | 7,280 | NVL72 chassis locators across up to 4 clusters (1,820 per cluster x 4) |
| LIB: Dynamic Functions | N/A | 0 | No dynamic LIB allocation; all functions are explicit |
| GIB: Reserved (future expansion) | 0x7C70–0xCFFF | 21,392 | Reserved for additional clusters, WAN scale-across, or future GPU generations |
| LIB: Explicit Tenant-ID (uDT) | 0xD000–0xEFFF | 8,192 | Tenant VRF identifiers for uDT decapsulation lookups |
| LIB: Explicit uA Forwarding | 0xF000–0xFFFF | 4,096 | uA adjacency SIDs for fabric steering (covers 1,024-port next-gen switches) |
| Total | | 65,535 | |

The WAN/Scale-Across reservation (0x0001–0x0FFF) ensures that future multi-factory or metro/WAN connectivity can be accommodated without namespace redesign. A substantial GIB reserve (0x7C70–0xCFFF, 21,392 values) is available for additional cluster growth, future GPU generations, or expanded WAN connectivity. The uA LIB is allocated 4,096 values (0xF000–0xFFFF) to leave plenty of room for next-generation switches without further reallocation.

### 3.3 Identity Versus Path: The Core Design Principle

A fundamental principle of the SRv6 uSID design for the planar AI fabric is the clean separation between GPU identity and packet path. This separation is what makes the architecture both scalable and operationally flexible:

•	**The Locator is the Identity:** a locator such as Loc:Chassis-42 represents the entity "NVL72 chassis 42" (or a specific GPU within it, depending on locator granularity). It is stable across plane failover events, traffic engineering changes, and fabric maintenance operations. The locator never encodes which physical plane the traffic will traverse.

•	**The Plane Plus the Path:** when the SDN controller steers a packet toward Loc:Chassis-42, it selects the physical plane uplink on the source NIC as part of the egress port choice, and optionally encodes uA steering SIDs in the uSID carrier to pin the path through specific spine nodes within the chosen plane. The locator itself is unchanged regardless of which plane carries the traffic.

•	**Deterministic Arrival:** because each plane is a physically independent fabric, a packet for Loc:Chassis-42 traveling over Plane-2 arrives exclusively at Chassis-42's Plane-2 NIC interface. There is no cross-plane leakage or ambiguity.

This means plane selection and GPU/chassis addressing are orthogonal controls. The SDN controller can change plane assignments, re-pin flows after failures, or rebalance load across planes without renumbering or re-advertising any GPU or chassis locators. The locator space is stable; only the uSID steering stack changes.

Note on per-plane locator reuse: Because the four fabric planes never intersect, it is technically possible to assign identical locator values to corresponding nodes across all four planes (e.g., the first spine in each plane shares the same locator). This conserves locator namespace and may simplify addressing in isolated deployments. However, for deployments that anticipate multi-factory or WAN/scale-across connectivity — where a WAN segment may not operate across all four independent planes — globally unique locators as described in Section 3.2 are recommended to preserve per-plane steering determinism.

### 3.4 Addressing Options for GPU NIC Endpoints

Two addressing models are available for GPU NIC endpoints, corresponding to the two sub-options in the Host-Based multi-tenancy design (Section 4). Both models are valid; the choice depends on the chosen encapsulation/decapsulation option and operational preferences.

**Option A: IPv6-in-IPv6 Encapsulation with Explicit Locator**

In the standard SRv6 forwarding model, the outer IPv6 header carries the uSID carrier as the destination address, and the inner IPv6 header carries the GPU application addresses. The GPU NIC's real IPv6 address (its application-layer identity) appears in the inner header and is delivered to the workload after decapsulation at the destination NIC or egress leaf.

Example flow — GPU-A to GPU-B via Plane-0, Leaf-7, Spine-43, Leaf-255:

| Header Field | Value | Notes |
| --- | --- | --- |
| Outer Source | FC00:0:3001:: | Source chassis/NIC locator |
| Outer Destination | FC00:0:FE07:FE2B:3042:E001:: | uSID carrier: uA(leaf-to-spine) \| uA(spine-to-leaf) \| Locator \| Tenant-ID |
| Inner Source | 2001:DB8:GPU-A::1 | GPU-A application IPv6 address |
| Inner Destination | 2001:DB8:GPU-B::1 | GPU-B application IPv6 address |

After Shift-and-Forward processing removes the two steering uSIDs at the intermediate leaf and spine nodes, the outer destination resolves to FC00:0:3042:E001:: — the destination chassis locator with Tenant-ID function — which the egress leaf or destination NIC decapsulates and resolves via VRF lookup.

**Option B: Engineered Destination Address (No Inner Header)**

An innovative alternative eliminates the inner IPv6 header entirely. The GPU NIC's actual IPv6 address is engineered to coincide with the residual value of the uSID carrier after all steering uSIDs have been shifted off by fabric nodes. The transmitting NIC constructs the destination address with the steering uSIDs prepended; by the time the packet arrives at the destination NIC, Shift-and-Forward has consumed all steering uSIDs, leaving only the destination GPU's true IPv6 address in the destination field.

This approach eliminates the 40-byte inner IPv6 header overhead on every packet — significant for RDMA workloads where header efficiency directly affects effective bandwidth — and avoids the need for NIC-based decapsulation logic. The GPU NIC receives a standard IPv6 packet addressed to its own address with no encapsulation to remove.

The critical design constraint for Option B is that the GPU NIC must be assigned the all-zeros (::0) host address in its /48 or /64 subnet, since that is the value remaining in the destination field after uSID processing completes. The upstream leaf switch uses the ::1 address in the same subnet. The NIC's locator value and its interface address are unified — the locator prefix IS the NIC's IPv6 address.

Example — GPU-A to GPU-B, engineered destination, no inner header:
| Header Field | Value | Notes |
| --- | --- | --- |
| Source | FC00:0:3001:: | Source chassis locator (also NIC IPv6 address) |
| Destination | FC00:0:FE07:FE2B:3042:: | uSID carrier: uA \| uA \| Dest Locator — no inner header |

After Shift-and-Forward removes FE07 (leaf-to-spine uA) and FE2B (spine-to-leaf uA), the destination address resolves to FC00:0:3042:: — which is both the destination chassis' locator and its NIC IPv6 address. The packet is delivered as a standard IPv6 packet requiring no decapsulation.

**Security note:** Option B eliminates the encapsulation trust boundary between the application layer and the infrastructure owner. In single-tenant clusters or clusters where the operator controls all NIC programming, this is acceptable. In multi-tenant environments it is generally unsuitable, as there is no outer header under operator control to enforce tenant isolation. The multi-tenancy and security implications are discussed in detail in Sections 5 and 8.

### 3.5 uA SID Design for Planar Fabric Steering

Micro-Adjacency (uA) SIDs encode specific physical link adjacencies at each transit node. In the planar AI fabric, uA SIDs are used to pin traffic to specific spine nodes within a plane, providing deterministic path selection beyond what ECMP hash-based forwarding can guarantee.

In the two-tier fabric, a steered path requires two uA SIDs:

•	Leaf-to-Spine uA: instructs the ingress leaf to forward the packet out the specific port connected to the target spine node. This determines which of the 256 spine nodes in the plane the flow traverses.

•	Spine-to-Leaf uA: instructs the spine node to forward the packet out the specific port connected to the destination leaf. Since each spine connects to all 512 leaves in the plane, this uA identifies the correct egress leaf.

With 512x100G leaf uplinks and 512x100G spine downlinks, up to 512 uA SID values are needed per node for full adjacency coverage. The proposed LIB allocation of 4,096 uA SIDs (0xF000–0xFFFF) comfortably covers this, with room for future high radix switch generations.

Note that either uA or uN steering is viable in the base deployment. Because each leaf already has exactly one uplink to each spine node, uN-based forwarding within the plane can achieve per-flow path pinning in the same manner as explicit uA as ECMP will have a single path/link to choose from.

### 3.6 SDN Control Plane Model

The AI factory fabric operates under a centralized SDN control model. There is no assumption of BGP peering between fabric tiers for base operation, and no distributed routing protocol floods individual GPU or chassis locators through the fabric. Instead, a Fabric Controller — operating in close coordination with the AI workload scheduler — computes paths, manages tenant/SID mappings, and programs forwarding entries on leaf switches and GPU NICs.

The SDN controller maintains a mapping table of the form:

| Key | Value |
| --- | --- |
| Chassis-ID | NVL72 rack identifier |
| Rack-Leaf-Map | For each plane: which leaf switch serves this chassis, which port |
| uSID-Locator | The /48 locator prefix assigned to this chassis |
| Tenant-Map | Which GPU NICs are assigned to which tenant, with corresponding uDT function SIDs |
| Path-Map | Active uA steering SIDs for current flow placements |

This architecture enables adaptive routing at the source without any fabric reconvergence. If the controller detects congestion on a specific spine node or plane, it rewrites the uA steering SIDs in the affected source NIC's forwarding entries to redirect subsequent flows. The spine and leaf tiers require zero state change — all path intelligence is encoded in the uSID header applied at the source edge. Plane failover (for example, following a spine node failure) triggers a uSID rewrite at the source edge only, completing in the time it takes the controller to push the updated forwarding entry to the affected NICs or ingress leaves.

BGP peering between fabric tiers may become beneficial in scenarios beyond the scope of this paper's base design: clusters exceeding three tiers, inter-datacenter or metro scale-across connectivity, or environments where the SDN controller cannot guarantee fast enough convergence on failure events. In those scenarios, BGP can carry locator summary routes between tiers, reducing the controller's convergence responsibility while maintaining the overall SRv6 forwarding model.

 
## 4. Multi-Tenant Encapsulation Design Options

This section describes the three SRv6 multi-tenancy design options introduced in the Executive Summary, examining each in detail with respect to control plane requirements, addressing implications, hardware dependencies, operational tradeoffs, and the uSID carrier structure produced. The three options represent a spectrum from fully network-controlled to fully host-controlled encapsulation, with a practical hybrid in between.

A foundational assumption across all three options is that the AI factory operator — not the tenant workload — controls the SRv6 encapsulation programming. The controller programs forwarding and encapsulation entries on leaf switches and/or GPU NICs. Aside from very large customers operating dedicated single-tenant clusters, tenants do not self-encapsulate. This assumption can be enforced by virtualizing the physical NIC such that the GPU workload is presented with a vNIC with no visibility or control over the physical NIC's SRv6 programming. The trust boundary implications of this assumption are examined further in Section 7.

An additional assumption is that BGP VPNs (L3VPN/EVPN over SRv6) are not used. While BGP VPNs are well understood and have a long successful history in service provider networks, they do not natively include path computation and distribution of steering uSIDs. Since the key value of SRv6 in this design is the ability to schedule and steer the fabric via uSIDs — not merely to provide VPN connectivity — an SDN-programmed static forwarding model is used throughout. If an external controller must compute and distribute steering uSIDs regardless, BGP VPNs offer limited additional value and add BGP speaker complexity to the NIC or host.

### 4.1 Option 1: Network-Based SRv6 (Leaf Encapsulation and Decapsulation)

In the Network-Based SRv6 design, the ingress leaf switch performs SRv6 encapsulation on outbound traffic from the GPU NIC, and the egress leaf switch performs SRv6 decapsulation and VRF lookup on inbound traffic to the destination GPU NIC. The GPU NIC passes unencapsulated standard IPv6 packets to and from the leaf; all SRv6 processing is confined to the leaf tier. This model requires no SRv6 capability on the GPU NIC.

**Control Plane Operation**

The SDN controller programs each ingress leaf with VRF-based static routes that include SRv6 encapsulation instructions. Each GPU-facing leaf interface is assigned to a tenant VRF. When a packet arrives from a GPU NIC on a VRF-assigned interface, the leaf performs a VRF route lookup, finds the static route with encapsulation instruction, and applies the SRv6 outer header with the appropriate uSID carrier as the destination address.

On the egress side, the controller programs each leaf with static uDT (micro-Decapsulation and Table lookup) SID entries. When the outer destination address resolves to the leaf's own locator after Shift-and-Forward processing, the leaf performs a LPM lookup, identifies the uDT function in the next 16-bit slot, decapsulates the outer header, and forwards the inner packet via the tenant VRF to the destination GPU NIC interface.

**SONiC / FRR Configuration Example**

The following illustrates the SDN-programmed configuration on a leaf switch running SONiC with FRR 10.4.1. The ingress leaf encapsulates outbound traffic for tenant 'green' toward destination chassis locator FC00:0:3042:: via a steered path through spine node uA FE07 then leaf uA FE2B:

Ingress/encapsulating leaf:
```yaml
! Base SRv6 locator and encapsulation source
segment-routing
  srv6
    encapsulation
      source-address FC00:0:2801::1
    locators
      locator MAIN
        prefix FC00:0:2801::/48 block-len 32 node-len 16
        behavior usid
        format usid-f3216
```
```yaml
! VRF assignment for local GPU-facing interface
interface Ethernet16
  vrf Vrf-green
  ipv6 address 2001:DB8:A001:100::1/64

```
```yaml
! Outbound: VRF static route with SRv6 encapsulation and uA steering
vrf Vrf-green
  ipv6 route 2001:DB8:A001:200::/64 Ethernet0 \
    segments FC00:0:FE07:FE2B:3042:D001::
```

```yaml
! Inbound: static uDT SID for tenant VRF return traffic decapsulation
segment-routing
  srv6
    static-sids
      sid FC00:0:2801:D001::/64 locator MAIN behavior uDT6 vrf Vrf-green
```
```yaml
! uA SIDs for fabric steering
      sid FC00:0:FE07::/48 locator MAIN behavior uA \
        interface Ethernet0 nexthop 2001:DB8:1:1::9
      sid FC00:0:FE08::/48 locator MAIN behavior uA \
        interface Ethernet4 nexthop 2001:DB8:1:1::11
```

**uSID Carrier Structure**

The uSID carrier produced by the ingress leaf in a two-tier fabric with traffic engineering is:

| Bits 0–31 | Bits 32–47 | Bits 48–63 | Bits 64–79 | Bits 80–95 |
| --- | --- | --- | --- | --- |
| FC00:0 (uSID Block) | FE07 (Leaf→Spine uA) | FE2B (Spine→Leaf uA) | 3042 (Dest Leaf Locator) | D001 (uDT Tenant-ID) |

In a three-tier fabric with inter-cluster traffic, four steering uSIDs are required (leaf-to-spine, spine-to-super-spine, super-spine-to-spine, spine-to-leaf), consuming all four available steering slots with the Locator and Function occupying the remaining two slots. This fills the F3216 carrier completely and leaves no room for additional steering SIDs, making the uSID slot budget a hard constraint for explicit path steering at three-tier scale.

**Tradeoffs for Option 1 (Network-Based Encap/Decap)**

| Aspect | Assessment |
| --- | --- |
| GPU NIC requirement | None — standard IPv6 NIC, no SRv6 capability needed |
| Leaf state | Per-tenant VRF on every ingress and egress leaf; scales with tenant x leaf count |
| Trust boundary | Strong — leaf is under operator control; NIC/workload cannot influence SRv6 headers |
| Operational familiarity | Highest — analogous to MPLS L3VPN PE encapsulation model |
| Traffic engineering | Full — leaf applies uA steering SIDs per controller instruction |
| Migration path | Lowest barrier — no NIC changes required; operator controls all SRv6 programming |

### 4.2 Option 2: Host-Based SRv6 (NIC Encapsulation and Decapsulation)

In the Host-Based SRv6 design, the GPU NIC performs SRv6 encapsulation on outbound traffic and SRv6 decapsulation on inbound traffic. The leaf switches perform only standard IPv6 forwarding and uA (or uN) Shift-and-Forward operations — they carry no per-tenant VRF state. This model offers the highest traffic engineering flexibility and scale, and the lowest leaf state overhead, at the cost of requiring SRv6 encapsulation and decapsulation capability on the GPU NIC.

Two sub-variants exist, differentiated by how the destination GPU NIC is addressed after the uSID carrier has been processed by the fabric:

**Option 2a: uA-Based Last-Hop Delivery**

In Option 2a, the uSID carrier includes a uA SID instructing the egress leaf to forward the packet out the specific port connected to the destination GPU NIC. The NIC processes the received packet, performs decapsulation, and looks up the trailing uDT/Tenant-ID function in its local SID table. No explicit chassis or GPU locator needs to be assigned — the uA SID at the egress leaf uniquely identifies the destination port.

Linux iproute2 example of NIC-side encapsulation (source NIC) and decapsulation (destination NIC):

```yaml
! Source NIC: encapsulate outbound flow with uA steering and Tenant-ID
ip -6 route add 2001:DB8:A001:200::/64 \
  encap seg6 mode encap.red \
  segs FC00:0:FE07:FE2B:FEC8:D002:: dev eth0
```
```yaml
! Destination NIC: decapsulate and lookup in tenant routing table
ip -6 route add FC00:0:D002::/48 dev eth1 \
  encap seg6local action End.DT6 table 1
```

In this example, FE07 and FE2B are the leaf-to-spine and spine-to-leaf uA SIDs respectively, FEC8 is the egress-leaf-to-NIC uA SID instructing the egress leaf to forward to the specific NIC port, and D002 is the uDT Tenant-ID function processed by the destination NIC. The uSID carrier structure is:

| Bits 0–31 | Bits 32–47 | Bits 48–63 | Bits 64–79 | Bits 80–95 |
| --- | --- | --- | --- | --- |
| FC00:0 (uSID Block) | FE07 (Leaf→Spine uA) | FE2B (Spine→Leaf uA) | FEC8 (Leaf→NIC uA) | E001 (uDT Tenant-ID) |

With Host-Based SRv6 we’ve increased the diameter of the steering domain by a single hop, however, Option 2a does not actually consume more uSID slots than Network-Based Option 1. The Leaf-to-NIC uA in slot 5 replaces the Destination Leaf Locator that Option 1 uses in the same slot — the uA itself identifies both the leaf and the specific egress port, making a separate Locator redundant. This option is fully compatible with both two-tier and three-tier fabrics within a single F3216 carrier.

**Option 2b: NIC Addressing with ::0/48 “Anycast” Value**

Option 2b eliminates the need for a leaf-to-NIC uA SID by engineering the NIC's IPv6 address to be the ::0 (all-zeros host) value of a /48 subnet shared between the leaf and the NIC. The leaf uses the ::1 address in the same /48. When the uSID carrier's Locator resolves to the leaf's prefix after Shift-and-Forward, the leaf performs a standard IPv6 longest-prefix-match lookup and forwards the packet to the NIC via the directly connected /48 subnet — no uA SID required.

With Option 2b the packet arrives at the destination NIC, which sees its “Locator” (the ::0/48 value), followed by a 16-bit uDT function. The NIC processes the received packet, performs decapsulation, and looks up the trailing uDT/Tenant-ID function in its local SID table and passes the inner packet to the GPU workload.

This approach assigns a locator value to each GPU NIC (or NVL72 chassis), but these locator values can be reused across all NICs on the same chassis. Because each NIC in the NVL72 chassis uplinks to a different leaf switch, and each leaf has exactly one port toward that chassis, there is no ambiguity: traffic arriving at Leaf-N destined for the ::0/48 address can only egress toward one physical port — the port connected to the NIC served by that leaf. There is zero possibility of the packet reaching the wrong NIC.

This is analogous to anycast addressing — same address, multiple instances, disambiguation enforced by topology — except the disambiguation is physically enforced by the fabric rather than by routing. Intra-chassis GPU-to-GPU traffic traverses NVLink exclusively and never enters the Ethernet fabric, so the ::0/48 address reuse creates no intra-chassis ambiguity either.

The uSID carrier structure for Option 2b in a two-tier fabric is:

| Bits 0–31 | Bits 32–47 | Bits 48–63 | Bits 64–79 | Bits 80–95 |
| --- | --- | --- | --- | --- |
| FC00:0 (uSID Block) | FE07 (Leaf→Spine uA) | FE2B (Spine→Leaf uA) | 3042 (Chassis Locator) | E001 (uDT Tenant-ID) |

Option 2b has the same uSID slot budget as Option 1 — two or four steering uSIDs plus Locator plus Function — making it fully compatible with both two-tier and three-tier fabrics within the F3216 carrier.

**Host-Based Option Comparison**

| Aspect | Option 2a (uA Last-Hop) | Option 2b (::0/48 Anycast) |
| --- | --- | --- |
| Chassis/GPU locator required | No | Yes (chassis-level, reusable per NIC) |
| uSID slots consumed (2-tier) | 4 (2x uA + leaf-to-NIC uA + uDT) | 4 (2x uA + Locator + uDT) |
| uSID slots consumed (3-tier) | 6 (4x uA + leaf-to-NIC uA + uDT) | 6 (4x uA + Host Locator + uDT) |
| NIC address complexity | Standard unique IPv6 per NIC | ::0/48 per NIC; topology-disambiguated |
| Leaf state | uA SID per NIC-facing port | Directly connected /48 — zero explicit route state |
| SDN controller mapping | uA SID → port → NIC | Locator → chassis → leaf → port |

### 4.3 Option 3: Hybrid (Host Encapsulation, Leaf Decapsulation)

The Hybrid design places SRv6 encapsulation at the transmitting GPU NIC and SRv6 decapsulation at the egress leaf switch. The source NIC applies the full uSID carrier including steering uSIDs and the destination leaf's locator with Tenant-ID function. The fabric performs Shift-and-Forward at intermediate nodes. The egress leaf decapsulates the outer header, performs a uDT VRF lookup, and forwards the inner packet to the destination GPU NIC via the tenant VRF — exactly as in Option 1's egress processing.

This design is arguably the most practical near-term option for several reasons:

•	It places encapsulation intelligence at the transmitting NIC, where the SDN controller has the most direct visibility into the workload's communication pattern and can apply per-flow traffic engineering at the source.

•	It keeps decapsulation and VRF lookup at the egress leaf, where it is hardware-accelerated and does not require SRv6 decapsulation capability on the destination NIC.

•	It provides the strongest trust boundary on the receiving side — the egress leaf, under operator control, performs the final tenant demultiplexing rather than delegating it to the NIC.

•	It provides a clear benefit in ingress leaf scale as the leaf no longer needs to maintain n-number of VRFs and static routes with SRv6 encapsulation instructions.

**uSID Carrier Structure**

The uSID carrier in Option 3 is identical in structure to Option 1 — the source NIC produces the same outer header that the ingress leaf would have produced in Option 1. The difference is operational: the NIC applies the encapsulation rather than the ingress leaf, and the ingress leaf simply performs standard IPv6 forwarding based on the destination address rather than VRF lookup and encapsulation.

| Bits 0–31 | Bits 32–47 | Bits 48–63 | Bits 64–79 | Bits 80–95 |
| --- | --- | --- | --- | --- |
| FC00:0 (uSID Block) | FE07 (Leaf→Spine uA) | FE2B (Spine→Leaf uA) | 3042 (Dest Leaf Locator) | D001 (uDT Tenant-ID) |

**Control Plane Operation**

The SDN controller programs the source NIC with static routes that include SRv6 encapsulation instructions — the same information it would have programmed on the ingress leaf in Option 1 but delivered to the NIC instead. The egress leaf is programmed with uDT SID entries for tenant VRF decapsulation, identical to Option 1's egress leaf programming.

The ingress leaf in Option 3 carries no per-tenant state. It sees a standard IPv6 packet with a uSID destination address and performs pure destination-based forwarding (or uSID shift-and-forward). This significantly reduces the ingress leaf's TCAM requirements compared to Option 1, where the ingress leaf must maintain per-tenant VRF tables for every attached GPU NIC.

### 4.4 Encapsulation Option Comparison

The three options represent a capability and complexity tradeoff axis. The following table summarizes the key dimensions:

| Aspect | Option 1 Network-Based | Option 2a Host uA | Option 2b Host ::0/48 | Option 3 Hybrid |
| --- | --- | --- | --- | --- |
| Encapsulation point | Ingress leaf | Source NIC | Source NIC | Source NIC |
| Decapsulation point | Egress leaf | Dest NIC | Dest NIC | Egress leaf |
| NIC SRv6 requirement | None | Encap + Decap capability | Encap + Decap capability | Encap only |
| Ingress leaf state | Per-tenant VRF and routes | None | None | None |
| Egress leaf state | uDT per tenant | uA per port | Directly connected /48 | uDT per tenant |
| Trust boundary | Strongest | Additional enforcement needed | Additional enforcement needed | Strong (egress) |
| 2-tier uSID slots | 4 | 4 | 4 | 4 |
| 3-tier uSID slots | 6 (tight) | 6 (tight) | 6 (tight) | 6 (tight) |
| Operational familiarity | Highest | Lowest | Low-Medium | Medium |
| Recommended for | Initial deployment | Large tenants with dedicated clusters | Scale-optimized deployments | Near-term production |

A hyperscale operator would not necessarily offer all three options simultaneously to all tenants. A natural evolution path is to begin with Option 1 (Network-Based SRv6) as the initial deployment model — lowest barrier to entry, highest operational familiarity, no NIC requirements — and migrate specific large tenants or the entire cluster to Option 3 (Hybrid) or either Option 2a or 2b (Host-Based SRv6) as NIC SRv6 capabilities mature and operational confidence grows. 

### 4.5 Traffic Engineering a Three-Tier Multi-Tenant Fabric

The three-tier multi-cluster fabric described in the Appendix introduces an additional super-spine tier between the spine and the cluster boundary. A fully steered inter-cluster path traverses four switching hops: ingress leaf to spine, spine to super-spine, super-spine to egress spine, egress spine to egress leaf. This requires four steering uSIDs, consuming all four available steering slots in the F3216 carrier and leaving no room for a Locator and Function within the same 128-bit address.

This is the central uSID slot budget challenge for the three-tier Host-Based design. The slot occupancy for each option at three-tier scale is:

| uSID Slot | Network-Based (Opt 1) | Host-Based 2a | Host-Based 2b | Hybrid (Opt 3) |
| --- | --- | --- | --- | --- |
| Block (32b) | FC00:0 | FC00:0 | FC00:0 | FC00:0 |
| Slot 1 | Leaf→Spine uA | Leaf→Spine uA | Leaf→Spine uA | Leaf→Spine uA |
| Slot 2 | Spine→SS uA | Spine→SS uA | Spine→SS uA | Spine→SS uA |
| Slot 3 | SS→Spine uA | SS→Spine uA | SS→Spine uA | SS→Spine uA |
| Slot 4 | Spine→Leaf uA | Spine→Leaf uA | Spine→Leaf uA | Spine→Leaf uA |
| Slot 5 | Locator | Leaf→GPU uA | Locator | Locator |
| Slot 6 | Tenant-ID (uDT) | Tenant-ID (uDT) | Tenant-ID (uDT) | Tenant-ID (uDT) |
| Total slots | 6 of 6  ✓ | 6 of 6  ✓ | 6 of 6  ✓ | 6 of 6  ✓ |
| Notes | Locator = dest leaf | Leaf→GPU uA replaces Host Locator | Locator = chassis /48 | Same as Opt 1 |

All four options fit within the six available 16-bit slots of the F3216 carrier at three-tier scale. In Option 2a, the Leaf-to-GPU uA SID occupies slot 5 in place of a Dest Leaf Locator — the uA identifies both the leaf and the specific egress port to the NIC, making a separate Locator redundant. All four options are tight at six slots, but none overflow.

Note on explicit versus loose-path steering: The slot budget analysis above assumes full explicit-path pinning with a uA SID at every switching tier. In scale-across scenarios — for example, WAN or metro connectivity between AI factories — a loose-path technique using uN (ECMP node) SIDs rather than explicit uA adjacency SIDs can reduce slot consumption. A single uN SID can replace two explicit uA SIDs, freeing a slot for additional steering or endpoint information. This approach trades path determinism for slot efficiency and is worth considering in multi-factory deployments where WAN segments may not support per-adjacency pinning.

### 4.6 VRF-Based Segmentation: Scale Considerations

Options 1 and 3 both rely on VRF-based tenant segmentation at the egress leaf. The scale implications of per-tenant VRFs on DC-class leaf switches are worth examining explicitly.

In the two-tier planar fabric, each leaf serves 128 or 256 GPU NICs depending on configuration. If those NICs belong to a mix of tenants, the leaf must maintain one VRF per tenant, with static routes for each destination prefix within each VRF. The controller programs these entries dynamically as workloads are provisioned and decommissioned.

The key TCAM scaling question is: how many VRFs and routes per leaf does the design require? 

## 5. SRv6 Traffic Engineering and Fabric Steering

This section further examines how SRv6 uSID traffic engineering may benefit the AI factory, driving higher performance and availability.

### 5.1 SDN-Driven Adaptive Routing and Dynamic Flow Steering

One of the most powerful properties of the SRv6 uSID architecture in the AI factory context is the ability to implement adaptive routing at the source without any fabric reconvergence. Because all path intelligence is encoded in the uSID header applied at the source NIC or ingress leaf, the SDN controller can change the path of any flow by updating a single forwarding entry on the source node — no routing protocol convergence, no distributed state update, no impact on other flows.

**Congestion-Driven Flow Steering**

The SDN controller monitors fabric utilization via telemetry from leaf and spine nodes. When congestion is detected on a specific spine node within a plane — indicated by elevated queue depths, increased ECN marking rates, or explicit congestion notifications from the NIC — the controller identifies the flows traversing that spine node via their uA steering SIDs and selects an alternative spine node within the same plane.

The controller pushes updated forwarding entries to the affected source NICs or ingress leaves, replacing the congested spine's uA SID with the alternative spine's uA SID. Subsequent packets in affected flows are immediately steered around the congestion. In-flight packets already on the congested path complete their transit normally — there is no disruption to existing packets, only a change in path for new packets. The destination locator and Tenant-ID remain unchanged throughout; only the steering uSIDs are rewritten.

**Plane-Level Failover**

If a fabric plane experiences a significant failure — a spine node failure, a link failure affecting multiple leaves, or a control plane fault — the SDN controller can evacuate affected flows to healthy planes by updating the source NIC's egress port assignment and uSID carrier for those flows. Because plane selection is encoded in the NIC's egress port choice (not in the uSID carrier itself), evacuation to a different plane requires updating the NIC's route entry to use a different physical port. The locator, Tenant-ID, and intra-plane steering SIDs remain structurally identical — only the physical egress interface changes.

**Job-Aware Traffic Engineering**

The SDN controller operates in close coordination with the AI workload scheduler. When a new training job is provisioned, the scheduler communicates the GPU allocation — which GPUs, which NVL72 racks, which planes — to the controller. The controller computes an optimal plane and path assignment for the job's communication pattern, taking into account existing flow placements, current fabric utilization, and the collective operation topology (ring, tree, or all-to-all) that the job will use.

This job-aware path placement provides several benefits over reactive congestion response:

•	Proactive isolation: training jobs can be allocated to non-overlapping spine nodes within a plane, ensuring that All-Reduce traffic from Job A does not share spine capacity with Job B's traffic even if both jobs use the same plane.

•	Deterministic bandwidth: each job's flows are assigned to specific spine paths with known available capacity, providing bandwidth guarantees that reactive ECMP cannot offer.


## 6. Security Considerations

Security in a multi-tenant SRv6 AI factory revolves around a single central question: who controls the SRv6 encapsulation header, and what happens if that control is compromised or misused? This section examines the trust boundary models, the ingress and egress enforcement mechanisms available to the operator, the key hardware challenge of IPv6 ACL bitmask matching, and the security options matrix that operators must evaluate when finalizing their design.

### 6.1 The Trust Boundary

The SRv6 outer header — the uSID carrier — determines where a packet goes in the fabric. Whoever controls the uSID carrier controls the forwarding path, the tenant VRF lookup at the egress, and ultimately which GPU NIC receives the decapsulated inner packet. The trust boundary defines the line between infrastructure-controlled and tenant-controlled portions of the packet header.

**Infrastructure-Controlled Encapsulation**

When the infrastructure operator programs the SRv6 encapsulation — either via a vNIC abstraction that hides the physical NIC from the tenant workload, or via a DPU/SmartNIC whose SRv6 stack is managed by the operator — the trust boundary is cleanly established. The outer SRv6 header is entirely under operator control. The tenant controls only the inner IPv6 source and destination addresses and the application payload. A tenant workload cannot manipulate the uSID carrier, cannot steer traffic to another tenant's GPU NIC, and cannot inject arbitrary uA SIDs to hijack another tenant's bandwidth allocation.

This model is directly analogous to multi-tenancy in service provider MPLS networks or public cloud environments, where the label stack or outer header is always under provider control and tenants operate only within the inner address space. It provides the strongest possible tenant isolation guarantees and is the recommended model for all multi-tenant deployments.

**Tenant-Controlled Encapsulation**

When tenants control NIC SRv6 encapsulation programming, the appropriate security model depends on whether the cluster is single-tenant or multi-tenant:

•	Single-tenant dedicated cluster: the tenant can be granted full encapsulation control without cross-tenant risk. Source address validation and uSID block boundary enforcement are recommended as defense-in-depth but not strictly required for isolation.

•	Multi-tenant shared cluster: tenant encapsulation control requires mandatory ingress enforcement. A tenant with NIC programming access could inject uA SIDs targeting another tenant's spine allocation, construct locators pointing to another tenant's NICs, or craft uDT Function values matching another tenant's VRF. 

Even with ingress enforcement in shared clusters, a determined actor who defeats the ingress leaf ACL may consume another tenant's bandwidth via uA SID injection. This residual risk must be mitigated through monitoring and anomaly detection as described in Section 8.5.

### 6.2 Ingress and Egress Enforcement

Two enforcement points exist in the fabric: the ingress leaf (the first hop from the source NIC) and the egress leaf (the last hop before the destination NIC). Both serve distinct security functions and both are relevant regardless of the encapsulation option chosen.

**Ingress Leaf Enforcement**

The ingress leaf enforces the source identity and uSID legitimacy of packets entering the fabric from GPU NICs. Key checks at ingress include:

•	Source address validation: the outer IPv6 source address of the encapsulated packet must match the expected NIC address for the ingress port. An ACL that permits only the known source prefix for each NIC-facing port prevents address spoofing.

•	uSID block validation: the outer destination address must begin with the cluster's assigned uSID block prefix. Packets with uSID blocks not belonging to this cluster are dropped.

•	Tenant-to-port binding: in the Network-Based (Option 1) design, the GPU-facing interface is assigned to a VRF, which inherently limits the tenant's reachable destinations to the VRF's routing table. In Host-Based designs, an explicit ACL binding the source NIC's prefix to a specific tenant's allowed destination prefixes provides equivalent enforcement.

**Egress Leaf Enforcement**

The egress leaf enforces destination access control — ensuring that only authorized tenants can deliver traffic to a given GPU NIC. An ACL at the egress leaf's NIC-facing port permits inbound traffic only from the expected tenant's uSID Locator range or Tenant-ID function value. This prevents a packet — whether legitimately forwarded by the fabric or injected via an ingress ACL bypass — from reaching a GPU NIC that does not belong to the packet's tenant.

The egress ACL is the last line of defense and should be present regardless of the encapsulation option or trust model. Even in fully infrastructure-controlled deployments, an egress ACL provides defense-in-depth against controller misconfiguration or fabric forwarding anomalies.

### 6.3 IPv6 ACL Bitmask Matching

In the SRv6 encapsulated model, the outer IPv6 destination address carries a uSID carrier whose first 64 bits contain variable steering uSIDs (uA values that change per flow and per path assignment). The last 64 bits carry the fixed, well-known Locator and Tenant-ID fields that identify the destination chassis and tenant. An ACL that needs to enforce tenant isolation by matching on the Tenant-ID field must therefore:

•	Match exactly on bits 64–127 (Locator + Tenant-ID): these are fixed and known at ACL programming time.

•	Ignore bits 0–63 (uSID block + steering uSIDs): these vary per flow and cannot be known in advance.

The required TCAM entry is a non-contiguous bitmask match:

Value:  0000:0000:0000:0000:xxxx:xxxx:xxxx:xxxx
Mask:   0000:0000:0000:0000:FFFF:FFFF:FFFF:FFFF

This is a contiguous suffix match (bits 64–127 exact, bits 0–63 don't-care) — technically a single valid TCAM entry if the hardware supports arbitrary bitmask matching on IPv6 destination fields rather than prefix-only matching. With this capability, tenant isolation scales as O(Tenants) — one TCAM entry per tenant — regardless of how many leaf switches, chassis, or steering uSID combinations exist.

**Current NOS Support Status**

No mainstream network operating system currently supports non-contiguous bitmask matching for IPv6 ACL destination addresses in general deployment. The SAI (Switch Abstraction Interface) ACL API specification does support 128-bit value-plus-mask matching for IPv6 destination fields in its abstract model, which means the capability is architecturally possible through the SONiC/SAI stack. However, vendor SAI implementations and the underlying P4 pipeline definitions that map SAI ACL entries to hardware TCAM may constrain the match to prefix-only, making this an open implementation question.

The platform most relevant to this question is the Cisco 8000 series running Silicon One ASICs, where the P4 pipeline definition determines whether the SAI ACL layer can expose arbitrary bitmask matching to SONiC. This is identified as the highest-leverage hardware capability question for the design: if Silicon One's P4 pipeline can be configured to support the required bitmask matching via the SAI ACL API, the tenant isolation TCAM problem is solved with a single entry per tenant. If not, the alternative enforcement mechanisms described below must be used.
 
## 7. Recommendations and Open Questions

This section synthesizes the analysis from preceding sections into concrete design recommendations, identifies the hardware and platform capability validations that must be completed before finalizing the design, and consolidates the open questions that remain for resolution by the operator, platform vendors, and NIC ecosystem.

### 7.1 Design Selection Recommendations

**Fabric Architecture**

The four-plane two-tier Clos fabric described in Section 2 is recommended as the baseline architecture for new hyperscale AI factory deployments. The key selection criteria:

•	Deploy Config A (4x200G, ConnectX-8 NICs) when per-GPU bandwidth is the primary constraint — for example, for very large model training where gradient exchange volume per GPU is high. Config A supports 65,536 GPUs per cluster at 800G per GPU aggregate.

•	Deploy Config B (4x100G, ConnectX-7 NICs) when maximum GPU count per cluster is the primary constraint, or when NIC cost and availability favor the ConnectX-7 generation. Config B supports 131,072 GPUs per cluster at 400G per GPU aggregate and aligns with Oracle's published Acceleron cluster scale target.

•	The switch infrastructure is identical between Config A and Config B — 2,048 leaves and 1,024 spines per cluster — providing a natural in-place upgrade path from Config B to Config A by replacing NICs without touching the fabric.

**Encapsulation / Decapsulation Model**

The recommended deployment progression is:

1.	Begin with Option 1 (Network-Based) for initial cluster deployment. This requires no SRv6 capability on GPU NICs, is operationally familiar, provides the strongest trust boundary, and can be deployed immediately on current NOS platforms. The primary operational cost is per-tenant VRF state on ingress and egress leaves.

2.	Migrate high-value or latency-sensitive tenants to Option 3 (Hybrid) as NIC SRv6 encapsulation capability matures and operator confidence in host-programmed SRv6 grows. Option 3 eliminates ingress leaf VRF state while retaining hardware-accelerated decapsulation at the egress leaf.

3.	Evaluate Option 2b (Host-Based with ::0/48 addressing) for large tenants with dedicated cluster allocations where maximum traffic engineering flexibility and minimum leaf state are priorities. Option 2b's anycast addressing model provides an elegant addressing simplification but requires full bidirectional SRv6 NIC capability and careful trust boundary management.

**Fabric Slicing**

Implement TE slicing from day one using uA SID assignment to separate training and inference traffic at the spine tier. Even in initial deployments with a single large tenant, reserving a dedicated spine slice for inference workloads prevents training burst interference on latency-sensitive inference flows. As the cluster scales to multiple tenants, the SDN controller can dynamically adjust slice boundaries without any switch reconfiguration.

**Security Architecture**

Apply egress leaf ACLs universally regardless of encapsulation option. Engage target silicon vendors on IPv6 ACL bitmask matching support via the SAI API — this is the highest-leverage capability for scalable tenant isolation. Deploy vNIC or DPU abstraction on all GPU hosts in shared multi-tenant clusters to eliminate tenant-controlled NIC programming as an attack surface. For single-tenant dedicated clusters, tenant encapsulation control may be granted with source address validation as defense-in-depth.

### 7.2 Industry Context and Forward Look

The architecture described in this paper represents the design class being deployed in the current generation of hyperscale AI factories. At 65,536 to 131,072 GPUs per cluster and up to 524,288 GPUs in the three-tier multi-cluster configuration, this is the scale at which frontier large language model training and inference operates, and the scale at which conventional network design assumptions — static routing, per-flow ECMP, ACL-based isolation — break down under the combined pressure of traffic volume, tenant count, and operational complexity.

Several technology trends will shape the evolution of this architecture over the next two to three years:

•	NVLink scale-up domain growth: NVIDIA NVL576 extends the NVLink domain to 576 GPUs across multiple racks, dramatically raising the GPU count threshold at which a spine layer becomes necessary in the scale-out fabric. As NVLink domains grow, the Ethernet scale-out fabric sees fewer, larger logical endpoints — further reducing the locator count requirements and simplifying the addressing model.

•	800G and 1.6T fabric: as switch silicon advances to 102.4T (1024x100G or 512x200G), the per-plane GPU count doubles without adding fabric tiers. Config A's 65,536 GPU target becomes 131,072 on 102.4T leaf switches, and the three-tier multi-cluster design scales to over 2 million GPU-plane attachment points. The uSID addressing framework described in this paper accommodates this growth without namespace exhaustion.

•	Ultra Ethernet Consortium standardization: UEC's work on packet spraying with receiver-side reordering, if widely adopted, may reduce the importance of per-plane flow pinning for RoCEv2 workloads. However, the planar architecture's fault isolation and operational independence properties remain valuable regardless of whether packet spraying is used — the planes provide reliability and operational flexibility beyond their traffic engineering role.

•	SRv6 NIC ecosystem maturity: ConnectX-8 and successor NICs with native SRv6 uSID encapsulation support will progressively lower the barrier to Option 2 and Option 3 deployments. As NIC-side SRv6 becomes a standard feature rather than a specialty capability, the Hybrid and Host-Based models will become the natural default for new cluster deployments.

The SRv6 uSID framework is well-positioned for this evolution. Its source-routing model, SDN-friendly programmability, zero-core-state architecture, and natural fit with the planar fabric's identity-versus-path separation principle make it the most capable and operationally scalable traffic engineering and multi-tenancy framework available for hyperscale AI factory networks today. The open questions identified in this paper are engineering challenges, not architectural barriers — and their resolution through vendor engagement, platform development, and operational experience will define the next generation of AI infrastructure networking.

 
## Appendix A: Multi-Cluster AI Factory — Three-Tier Planar Fabric

While the two-tier planar Clos fabric scales elegantly to 65,536 GPUs (Config A) or 131,072 GPUs (Config B) within a single cluster, some AI factory deployments require capacity beyond what a single two-tier cluster can provide. This appendix describes the three-tier extension of the planar architecture that interconnects multiple clusters via a super-spine tier, scaling to 262,144 or 524,288 GPUs across four clusters while preserving the plane-independence and SRv6 traffic engineering properties of the base design.

A.1 Design Approach

The three-tier design treats each two-tier planar cluster as a self-contained building block. A super-spine tier is added above the existing spine tier, with one super-spine group per fabric plane. Each super-spine group interconnects the same-numbered plane across all four clusters — SuperSpine-Plane-0 interconnects Plane-0 from Clusters 1 through 4, SuperSpine-Plane-1 interconnects Plane-1 from Clusters 1 through 4, and so on. Plane independence is fully preserved: traffic in Plane-0 never crosses into Plane-1's super-spine group.

The spine tier is retained at full capacity from the two-tier design. Each spine node's 512x100G ports are split between downlinks to leaves (256 ports) and uplinks to super-spine (256 ports), maintaining non-blocking connectivity within the cluster while adding inter-cluster reachability through the super-spine tier.

A.2 Scale Calculations

Per-Cluster Spine Uplinks (per plane)

With 512 spine nodes per plane per cluster, each providing 256x100G uplinks to the super-spine tier:

| Parameter | Calculation | Value |
| --- | --- | --- |
| Spine uplinks per plane per cluster | 512 spines x 256 uplinks | 131,072 x 100G |
| Super-spine ports required (4 clusters) | 4 x 131,072 | 524,288 x 100G |
| Super-spine nodes per plane | 524,288 / 512 ports per node | 1,024 nodes |
| Super-spine port utilization | 512 / 512 | 100% |

Full Four-Cluster AI Factory

| Parameter | Config A (4x200G) | Config B (4x100G) |
| --- | --- | --- |
| GPUs per cluster | 65,536 | 131,072 |
| Total GPUs (4 clusters) | 262,144 | 524,288 |
| NVL72 racks per cluster | ~910 | ~1,820 |
| Total NVL72 racks | ~3,640 | ~7,280 |
| Leaves per plane per cluster | 512 | 512 |
| Spines per plane per cluster | 512 | 512 |
| Super-spine nodes per plane | 1,024 | 1,024 |
| Total leaves (4 clusters x 4 planes) | 8,192 | 8,192 |
| Total spines (4 clusters x 4 planes) | 8,192 | 8,192 |
| Total super-spines (4 planes) | 4,096 | 4,096 |
| Total fabric switches | 20,480 | 20,480 |

A.3 Key Observations

•	Plane independence scales cleanly: each super-spine group is as logically isolated as the spine and leaf tiers below it, preserving fault domain isolation and the ability to perform staged maintenance or upgrades per plane.

•	Super-spine is fully utilized: the 1,024-node super-spine per plane achieves 100% port utilization — a direct consequence of the clean 512-port radix matching the aggregate uplink count from four clusters.
•	Switch infrastructure is configuration-identical between Config A and Config B: the only difference is NIC generation and GPU density per leaf, not switch count or topology. The physical fabric can be deployed once and support either NIC generation.

•	SRv6 uSID extends naturally: the three-tier design adds one additional forwarding hop (spine to super-spine to spine) for inter-cluster flows, consuming two additional uSID slots in the F3216 carrier as analyzed in Section 6.2. Intra-cluster flows continue to traverse only leaf to spine to leaf, preserving the 3-hop latency guarantee for the majority of collective operation traffic.

•	At 524,288 GPUs, this architecture represents one of the largest AI factory designs currently contemplated by any hyperscaler or national AI infrastructure program.

 
## Appendix B: Fabric Slicing

Fabric slicing partitions the shared forwarding infrastructure into logically isolated segments, enabling traffic engineering guarantees, workload-level quality of service, and interference isolation without requiring dedicated physical hardware per tenant or workload type. In the planar AI factory context, slicing serves two primary purposes: isolating training traffic from inference traffic within the same cluster, and providing bandwidth guarantees to specific tenants or jobs within the shared fabric. Full implementation detail is provided in Appendix E; this section provides a concise overview of the three primary slicing mechanisms.

Three slicing approaches are available, ranging from coarse to fine granularity: plane-based slicing (entire fabric planes dedicated to a workload or tenant), TE slicing via uA SID assignment (non-overlapping spine node sets per tenant or job), and ECMP slicing via anycast uN SIDs or per-VRF next-hop sets. These can be used independently or in combination.

### B.1 TE Slicing via uA SID Assignment

TE slicing exploits the per-adjacency granularity of uA SIDs to assign non-overlapping sets of spine nodes to different traffic slices. Because the uA SID in the uSID carrier explicitly identifies which spine node a flow traverses, the SDN controller can enforce physical path separation between slices simply by programming different uA SID values at ingress points for different slices.

Consider a cluster with 256 spine nodes per plane. The controller partitions these into two slices:

•	Training slice: spine nodes 0–191 (75% of spine capacity). Flows assigned to this slice use uA SIDs from the range 0xC000–0xC0BF, steering them exclusively through the first 192 spine nodes.
•	Inference slice: spine nodes 192–255 (25% of spine capacity). Flows assigned to this slice use uA SIDs from the range 0xC0C0–0xC0FF, steering them exclusively through the remaining 64 spine nodes.

From the data plane perspective, training and inference traffic follow physically disjoint paths through the spine tier. A training All-Reduce storm that saturates its allocated spine nodes cannot affect inference latency, because inference traffic is physically routed through a separate set of spine nodes. The slice boundary is enforced purely by the uA SID values in the uSID carrier — no special switch configuration, no virtual circuit setup, no per-flow state in the spine.

The slice size ratio is fully programmable by the SDN controller and can be adjusted dynamically. If the cluster transitions from a mixed training-and-inference workload to a pure training workload, the controller can reassign all spine nodes to the training slice by updating the uA SID programming at ingress points. The spine switches themselves require no reconfiguration — the change is entirely in the source-applied uSID headers.

**TE Slice uSID Carrier Structure**

The uSID carrier for a training flow in the two-tier fabric with TE slicing uses a uA SID drawn from the training slice's spine range:

| Bits 0–31 | Bits 32–47 | Bits 48–63 | Bits 64–79 | Bits 80–95 |
| --- | --- | --- | --- | --- |
| FC00:0 (Block) | FE07 (Training Slice Leaf→Spine uA) | FE2B (Spine→Leaf uA) | 3042 (Locator) | D001 (Tenant-ID) |

The inference flow uses a uA SID from the inference spine range — for example 0xFEC2 rather than 0xFE07 — steering it to a different spine node. The remainder of the carrier is structurally identical. The slice assignment is invisible to the destination NIC or egress leaf; it affects only the transit path through the spine tier.

### B.2 ECMP Slicing

ECMP slicing operates at the flow hash level rather than the explicit adjacency level. In standard ECMP, the switch selects a next-hop from its equal-cost set by hashing the flow's 5-tuple (source address, destination address, source port, destination port, protocol). Different flows may hash to different spine nodes, providing statistical load balancing but no deterministic path assignment.

ECMP slicing constrains which ECMP next-hops are presented to specific traffic classes, effectively partitioning the spine tier's ECMP set into subsets. This can be implemented via:

•	Anycast uN SID across a spine group: a uN SID value is assigned that resolves at each leaf to a specific subset of spine nodes as ECMP next-hops. Traffic encoded with that uN value is ECMP-distributed exclusively across the designated spine group without per-flow uA precision. This is the recommended ECMP slicing approach — simple to program, SDN-controller-friendly, and does not require per-VRF ECMP configuration on the leaf.

•	DSCP-based next-hop selection: traffic class markings in the IPv6 Traffic Class field are used at transit nodes to select among slice-assigned ECMP groups. This approach does not consume a uSID slot but requires consistent DSCP marking across the fabric.

•	Per-VRF ECMP sets at the leaf: in the Network-Based (Option 1) and Hybrid (Option 3) designs, the ingress leaf's VRF can be configured with a restricted next-hop set for each tenant's traffic class, implementing ECMP slicing at the point of encapsulation.

ECMP slicing provides a lighter-weight alternative to TE slicing when exact path pinning is not required — for example, for inference workloads where flow-level load balancing is acceptable but training-traffic interference must be avoided. The tradeoff versus TE slicing is that ECMP slicing provides statistical rather than deterministic path separation: under hash collision conditions, flows from different slices could theoretically map to the same spine node. For most practical deployments this is acceptable; for workloads requiring guaranteed interference isolation, TE slicing with explicit uA SIDs provides the stronger guarantee.

### B.3 Plane-Based Slicing

The most coarse-grained slicing option is plane-based slicing: assigning different workload types or tenants to different fabric planes entirely. Because each plane is a physically independent Clos fabric with no shared switches, links, or control plane state, plane-based slicing provides absolute interference isolation — the strongest possible guarantee.

In a four-plane cluster, plane-based slicing might be implemented as:

Plane	Allocation	Bandwidth	Notes
Plane 0	AI Training — Tenant A	200G or 100G per GPU	Full plane dedicated to training collective operations
Plane 1	AI Training — Tenant B	200G or 100G per GPU	Separate plane for second large training tenant
Plane 2	Inference — Mixed Tenants	200G or 100G per GPU	Inference workloads ECMP across available spine
Plane 3	Management / Storage / Spare	200G or 100G per GPU	Fabric management, checkpoint storage I/O, failover reserve

The significant limitation of plane-based slicing is its coarseness: dedicating an entire plane to a single tenant or workload type consumes 25% of the cluster's total GPU bandwidth for that slice, regardless of actual utilization. For large tenants with dedicated clusters this is appropriate — NVIDIA's own reference architecture for the NVL72 uses dedicated planes per large customer — but for shared multi-tenant environments with many small tenants, plane-level dedication is wasteful.

Plane-based and TE-based slicing are complementary and can be used in combination. A common pattern is to use plane-based slicing to separate training from inference at the coarse level, and TE slicing within each plane to isolate individual tenants or jobs from each other:

•	Planes 0–2: training workloads, each plane further TE-sliced per tenant across the spine tier.
•	Plane 3: inference workloads and management, ECMP-sliced per tenant within the plane.

This hierarchical slicing approach provides the strongest guarantees at the training/inference boundary while efficiently sharing each plane's spine capacity among multiple training tenants via TE slicing.

### B.4 Training and Inference Separation

The question of whether AI training and inference workloads should share fabric planes or be separated is a fundamental operational design decision with significant implications for both performance and utilization efficiency.

The Case for Separation

AI training traffic is highly bursty and bandwidth-intensive. During All-Reduce operations, a training job simultaneously floods the fabric with large data transfers across all active GPU pairs. This traffic pattern is highly synchronized — all GPUs in a job inject traffic at nearly the same moment — producing sharp, correlated bursts that saturate switch buffers and create congestion in ways that statistical ECMP cannot fully mitigate.

Inference traffic, by contrast, is typically lower bandwidth per request but highly latency-sensitive. A burst of training traffic on a shared spine can introduce tens to hundreds of microseconds of queuing delay on inference flows — well above the latency budgets of real-time inference services. For this reason, co-locating training and inference on the same fabric plane without isolation is generally inadvisable at hyperscale.

Recommended Approach

The recommended approach is to provide inference workloads with a dedicated TE slice within one or more planes, using uA SID assignment to steer inference flows through a subset of spine nodes that training traffic is explicitly excluded from. This provides:

•	Guaranteed inference bandwidth: the inference spine slice is never shared with training traffic, providing predictable per-flow bandwidth regardless of training load.

•	Latency isolation: inference flows experience only inference-generated congestion within their spine slice, eliminating training burst interference.

•	Utilization efficiency: spine nodes not currently needed by inference can be temporarily reassigned to training by the SDN controller during periods of low inference load, maximizing overall fabric utilization.

A separate uSID block sub-range for inference flows is also worth considering. By assigning inference traffic a distinct uSID block prefix, the SDN controller can apply different path computation policies, QoS markings, and ACL treatments to inference flows at the ingress point without examining per-flow state. The controller identifies the flow type by the uSID block value in the destination address and applies the appropriate encapsulation policy.

### B.5 Slicing and the SDN Controller

All slicing constructs described in this section are implemented through SDN controller programming of uA SID values at ingress points — no special switch silicon capability is required beyond standard uSID Shift-and-Forward processing. This is a significant operational advantage: the slice topology is entirely defined in software, making it fully dynamic and reconfigurable without switch hardware changes or service interruptions.

The SDN controller maintains a slice allocation table that maps each active job or tenant to a set of uA SID values corresponding to its assigned spine nodes. As jobs start and finish, the controller updates the slice allocation and reprograms the affected ingress points. The spine switches themselves carry no slice-specific configuration — they simply forward packets based on the uA SID they encounter, which the controller has pre-programmed as a standard uA adjacency entry pointing to the correct egress interface.

This architecture makes the slicing model highly amenable to integration with the AI workload scheduler. The scheduler communicates job resource requirements — GPU count, communication pattern, bandwidth guarantee, latency sensitivity — to the controller, which translates these requirements into a spine node allocation and programs the corresponding uA SIDs at the job's ingress leaf switches or source NICs. When the job completes, the controller releases the spine allocation and makes it available for the next job. The fabric's physical configuration never changes; only the uSID values in forwarding tables are updated.

## Appendix C: Glossary

**Term	Definition**
ACL	Access Control List — a set of permit/deny rules applied to packet headers at a network device

DACL	Dynamic ACL — an ACL generated and applied automatically following 802.1X authentication and provisioning

DPU	Data Processing Unit — a SmartNIC with an embedded processor capable of offloading network functions including SRv6 encapsulation

ECMP	Equal-Cost Multi-Path — load balancing across multiple equal-cost forwarding paths, typically via flow hash

F3216	SRv6 uSID carrier format with 32-bit uSID Block and 16-bit Locator/Function slots; provides 6 available uSID slots

GIB/LIB	Global/Local ID Block — the SRv6 SID namespace allocation structure dividing globally significant locators from locally significant functions

GPU	Graphics Processing Unit — used as AI accelerator in training and inference clusters

NIC	Network Interface Card — the host network adapter; may be a DPU/SmartNIC in advanced deployments

NCCL	NVIDIA Collective Communications Library — the communication library that manages GPU-to-GPU data exchange in distributed AI training

NVLink	NVIDIA's proprietary high-bandwidth GPU-to-GPU interconnect; used within the NVL72 rack as the scale-up fabric

NVL72	NVIDIA GB200 NVL72 — a rack-scale system with 72 Blackwell GPUs unified by a NVLink Switch fabric

RoCEv2	RDMA over Converged Ethernet version 2 — the RDMA transport protocol used for GPU-to-GPU communication over the Ethernet scale-out fabric

SAI	Switch Abstraction Interface — the hardware abstraction layer used by SONiC to interface with merchant silicon ASICs

SDN	Software-Defined Networking — a control plane model in which a centralized controller programs forwarding entries on network devices

SGT	Security Group Tag — an identifier embedded in packet headers for policy enforcement and tenant isolation

SID	Segment Identifier — an IPv6 address encoding an SRv6 instruction to be executed by the processing node

SONiC	Software for Open Networking in the Cloud — an open-source network operating system used on merchant silicon switches

SRv6	Segment Routing over IPv6 — a source routing architecture that encodes a sequence of forwarding instructions in the IPv6 destination address field

TCAM	Ternary Content-Addressable Memory — the hardware used for ACL and routing table lookups in merchant silicon ASICs

TE	Traffic Engineering — the practice of explicitly controlling the path that traffic takes through the network

uA	micro-Adjacency SID — an SRv6 uSID encoding a specific physical link adjacency at a transit node

uDT	micro-Decapsulation and Table lookup — an SRv6 uSID endpoint behavior that decapsulates the outer header and forwards via a VRF table lookup

uED	uSID Point of Encapsulation-Decapsulation — the node (leaf switch or GPU NIC) at which SRv6 encapsulation or decapsulation is performed

uN	micro-Node SID — an SRv6 uSID encoding an ECMP-capable node transit, selecting among equal-cost next-hops

uSID	micro-SID — a compressed SRv6 SID encoded in a 16-bit slot within the IPv6 destination address, enabling multiple SIDs per packet without SRH extension headers

VRF	Virtual Routing and Forwarding — an isolated routing table instance used to separate tenant traffic within a shared network device

W-LIB	Wide Local Information Base — an SRv6 extension supporting 32-bit Function fields, providing a larger tenant-ID and local function namespace

