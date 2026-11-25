# Can the remote Backend run Docker inside a Runner?

Research for [#4](https://github.com/arteven/gh-runners-msb/issues/4), which feeds decision [#10](https://github.com/arteven/gh-runners-msb/issues/10). Researched 2026-11-25.

Sources are pinned to the commits that were read:

- **MS**: `superradcompany/microsandbox` @ [`c75e80c`](https://github.com/superradcompany/microsandbox/tree/c75e80c1f2990d80a6ec00906fb65cd58afad92d) (2026-11-24). `docs/` here is what docs.microsandbox.dev publishes.
- **KF**: `superradcompany/libkrunfw` @ [`f0493b6`](https://github.com/superradcompany/libkrunfw/tree/f0493b6f1873ed6031bc0bb87568db0095a6d3b6), the submodule commit MS pins at `vendor/libkrunfw`. This is the guest kernel bundle.
- **HB**: `superradcompany/hpc-sandbox-benchmarks` @ [`cfe6fd8`](https://github.com/superradcompany/hpc-sandbox-benchmarks/tree/cfe6fd82d2f0433ad681746ffaffd6ca94feb5d0). This is microsandbox's own benchmark repo. Its dataset records an in-guest probe of real `microsandbox-cloud` sandboxes.

docs.microsandbox.dev and microsandbox.dev returned HTTP 403 (sandbox egress approval), so everything below comes from the repos. The cloud control plane and host runtime are **not public**: no `superradcompany` repo contains them. Claims about cloud behaviour therefore rest on the docs, the OpenAPI schema, the SDK's client-side validation, and the HB probe data.

Tags: **[read]** means stated in a source. **[observed]** means measured by HB on real cloud sandboxes. **[inferred]** means my reasoning, not stated anywhere.

---

## Short answers

**Overall: yes, probably, but not the way the local Backend does it.** The cloud guest kernel has everything `dockerd` needs, and the guest runs as real root with `CAP_SYS_ADMIN`. The only real obstacle is storage. Cloud refuses both of the local Docker recipes, the flat root disk and the owned or disk-kind volume at `/var/lib/docker`. On the default layered root, Docker's overlay storage fails on overlay-on-overlay. The workarounds are a tmpfs mount, an in-guest loop-mounted ext4 file, or a disk-image mount, and none of them has been tested on cloud by anyone in public. One cheap experiment (end of this file) settles it.

- **Maximum root disk size; per sandbox?** It can be set per sandbox: `resources.disk_size_mib`, `--root-disk 20G`, or Go `WithRootDisk(RootDisk.Managed(mib))` **[read]**. If you omit it, the default equals requested RAM, clamped to 4–16 GiB **[read]**. The maximum is a per-plan quota, `max_disk_size_mib_per_sandbox`, readable at `GET /v1/quotas`. No public source gives the number **[read]**. HB got 40 GiB on cloud (39.3 GiB visible) **[observed]**. The pricing docs use 64 GiB as their example **[read]**. Cloud cannot grow a disk after creation: you have to recreate the sandbox **[read]**.
- **Are flat root disks and disk-backed volumes unavailable on cloud?** Yes. The docs say so in four places, and the SDK refuses them before any request is sent (`"non-managed root_disk"`, `"sandbox-owned volumes"`, `"named volume inline create"`). Named cloud volumes are directory volumes only: "disk-kind volumes are not available" **[read]**. Roadmap: one generic line, "Expect this list to shrink" **[read]**. No issue or discussion tracks flat roots or disk volumes on cloud (searched issues and discussions for `flat`, `cloud`, `remote`, `dind`, `docker`). What cloud *does* accept: `tmpfs` mounts and `disk_image` mounts (a raw/qcow2/vmdk file in the org's host volume, attached as virtio-blk) **[read]**.
- **Kernel features exposed to the guest?** Cloud runs the libkrunfw kernel. HB saw `Linux version 6.12.99 (root@libkrunfw)` in Sep 2026, while the pinned libkrunfw is now at 6.12.118 **[observed]**. That config has, built in (no modules): overlayfs, cgroup v2 with memory/pids/cpuset/device/freezer/bpf controllers, all namespaces, bridge + br_netfilter, veth, vxlan, nftables and legacy iptables with NAT, MASQUERADE, conntrack and addrtype, ip6 NAT, FUSE, loop devices, and tmpfs xattrs **[read]**. Missing: IPVS, macvlan, ipvlan, `/proc/config.gz` **[read]**. On cloud, HB saw cgroup2 mounted and writable, `/dev/fuse` present, overlay in `/proc/filesystems`, and `loop0`–`loop7` present **[observed]**. Whether today's cloud kernel matches the pinned config is **not verifiable** from public sources (no `/proc/config.gz`). The experiment below probes it.
- **Limits on long-lived runner sandboxes?** Billing is per running second: Standard $0.048/vCPU-h + $0.003/GiB-h, Shared $0.007 + $0.000375, root disk $0.0000625/GiB-h. That puts a 4 vCPU / 8 GiB Slot waiting 24/7 at about **$160/month on Standard and $26/month on Shared** **[read + arithmetic]**. Free plan: ephemeral sandboxes only, **1 hour max each**, so no job longer than 1 h **[read]**. Paid plans have per-plan caps that the public schema lists but does not quantify: `max_uptime_minutes_per_sandbox`, `max_memory_mib_per_sandbox`, `max_vcpus_per_sandbox`, `max_concurrent_ephemeral_running`, `max_concurrent_persistent_running`, `max_sandboxes` **[read]**. No idle timeout applies unless you set one, and an attached exec session (the Runner's `run.sh` stream) counts as activity **[read]**. Cloud has no pause, force kill, drain or touch, and only graceful stop **[read]**.
- **Known examples of Docker-in-sandbox on cloud?** None, working or failing. Every Docker guide (`docker-in-sandbox`, `compose-tests`) is badged **Local-only** because it uses a flat root **[read]**. The only Docker issue and discussion threads are local: [#908](https://github.com/superradcompany/microsandbox/issues/908) and [discussion #598](https://github.com/superradcompany/microsandbox/discussions/598) **[read]**. Both show the exact failure we should expect on a layered cloud root: `overlay: filesystem on …/snapshots/3/work not supported as upperdir` **[read]**. HB's own DinD probe on cloud reported `blocked` only because its image had no `dockerd` binary. Every other prerequisite passed **[observed]**.

## What matters for #10

1. **The remote Backend cannot use `--docker-disk`.** The local design gives `/var/lib/docker` its own ext4 disk through an owned or disk-kind volume. Cloud rejects both, so the remote Backend needs a different `/var/lib/docker` strategy. "Same flags on both Backends" is not achievable today.
2. **Candidate strategies for the remote Backend**, cheapest first. All are **[inferred]** until the experiment runs.
   - **A. Do nothing extra**, if the cloud root is now plain ext4. HB's newest cloud run (2026-09-10) saw `/` as **ext4 with no overlay mount at all and a single `vda`**. Every earlier run (2026-08-02 to 2026-09-03) saw an **overlay root over `vda`+`vdb`** **[observed]**. If that change stuck, Docker's overlay storage works on the managed root as it is, and only the disk size matters. Nothing in the changelog documents the change, so it may also be temporary or limited to some images.
   - **B. Loop-mounted ext4 inside the guest.** Run `truncate -s 20G /docker.img && mkfs.ext4 -q /docker.img && mount -o loop /docker.img /var/lib/docker` before starting `dockerd`. Needs root, `CAP_SYS_ADMIN` and loop devices, all present on cloud **[observed]**, plus `e2fsprogs` in the Runner image. Size it from the root disk. This is the closest analogue to `--docker-disk`, and it works the same on both Backends.
   - **C. tmpfs at `/var/lib/docker`.** Cloud accepts tmpfs mounts **[read]**, and overlay on tmpfs works (the maintainer's pre-0.4.4 local recipe used a tmpfs data-root, discussion #598 **[read]**). It costs RAM, so a Slot needs memory for both the job and its images. It is capped by `max_memory_mib_per_sandbox`.
   - **D. `disk_image` mount** from the org host volume. The schema allows it **[read]**. But each Runner needs its own writable image file. Locally, a lock is taken at launch on each disk-image mount (`docs/configuration.mdx` L340 **[read]**). I assume cloud behaves the same **[inferred]**. The Pool would have to create and delete one file per Slot (the volume files API has `copy`). It is also the only option that touches persistent, billed storage. Most moving parts.
   - **E. `vfs` or `fuse-overlayfs` storage driver.** Always works, but `vfs` is slow and disk-hungry. Last resort.
3. **Run `dockerd` as root, not rootless.** The cloud default `security_profile` is `default`, which keeps full guest root. `restricted` drops `CAP_SYS_ADMIN` and breaks DinD (PR [#911](https://github.com/superradcompany/microsandbox/pull/911)) **[read]**. The Pool must never request `restricted`.
4. **Plan quotas decide whether the remote Backend is usable for real jobs.** Free plan: 1 h max per sandbox. Paid plans: per-sandbox uptime, disk and memory caps, and concurrent-sandbox caps. The values are only in `GET /v1/quotas`. `up` could read it at start and refuse a `--slots` count or disk size that exceeds the plan **[inferred design option]**.

## Surprise relevant to the map's Notes

- **The Notes' "Background" remote failure probably isn't a cloud limitation.** `crun: write /proc/self/oom_score_adj: Permission denied` happens when a process tries to *lower* its OOM score without `CAP_SYS_RESOURCE`. That is typical of **rootless podman** run as the `runner` user. On cloud, HB measured `is_root=true` and `cap_sys_admin=true` for the default exec user **[observed]**. Rootful `dockerd` started as root should not hit this **[inferred]**. This fits the Notes' conclusion that "neither is a bug in msb", and it points at how Docker is started (rootful `dockerd`, not podman) as much as at image delivery.
- Nothing found says the remote Backend **can't** run Docker, so the fixed decision "both msb Backends are in scope" stands. But `--docker-disk` cannot mean the same thing on remote (see point 1), which constrains the "faithful port" decision for that flag.

---

## Details

### 1. Root filesystem layout and why Docker breaks on it

- A managed (layered) OCI root is an overlayfs. The lower layer is a read-only EROFS image, and the upper is a writable ext4 block device: MS `crates/agentd/lib/init.rs` L314–383 ("Mount merged EROFS lower + writable upper + overlayfs at /newroot") **[read]**. The Go SDK calls it "a sparse ext4 created and owned by microsandbox": MS `sdk/go/options.go` L633–641 **[read]**.
- Docker's overlay storage (overlay2, or containerd's overlayfs snapshotter) needs an upperdir on a non-overlay filesystem. The docs say why the flat root exists: "Docker's overlay storage is not nested on the default sandbox OverlayFS" (MS `docs/how-to/docker/docker-in-sandbox.mdx` L56, L108) **[read]**. Users saw the actual error, `overlay: filesystem on /var/lib/docker/containerd/daemon/io.containerd.snapshotter.v1.overlayfs/snapshots/3/work not supported as upperdir`, in [discussion #598](https://github.com/superradcompany/microsandbox/discussions/598) (ddelbondio, 2026-04-23) **[read]**.
- Local fixes (all local-only): `--root-disk flat:10G` (MS `docs/how-to/docker/docker-in-sandbox.mdx` L24), `--mount-owned /var/lib/docker:kind=disk,size=10G` (L118), `--mount-named docker-data:/var/lib/docker:kind=disk,size=10G` (L150) **[read]**.
- **HB cloud observations** (field `root_fstype`, `mount_fstypes`, `block_devices` in `hostMetadata`):
  - Runs from 2026-08-02 to 2026-09-03 (e.g. HB `data/dataset/runs/33804295335.json`): `root_fstype=overlay`, fstypes `cgroup2,devpts,devtmpfs,overlay,proc,sysfs,tmpfs,virtiofs`, block devices `loop0..7,vda,vdb` **[observed]**.
  - Run from 2026-09-10 (HB `data/dataset/runs/34437329093.json`): `root_fstype=ext4`, fstypes `cgroup2,devpts,devtmpfs,ext4,proc,sysfs,tmpfs,virtiofs` (no overlay), block devices `loop0..7,vda` **[observed]**.
  - The probe reads the last `/` entry in `/proc/self/mountinfo` (HB `lib/probe/isolation/20-collect.sh` L205–245). HB requested a managed root of `diskGb*1024` MiB (HB `packages/microsandbox-cloud/src/index.ts` L176–177), SDK `microsandbox` 0.6.8 (HB `package.json` L33) **[read]**.
  - So the cloud host seems to have switched managed roots to a single ext4 disk, a "flat-like" layout, between 3 and 10 September 2026. No changelog entry or doc mentions it, and the docs still say flat roots are unavailable on cloud. **This is the most decision-relevant unknown.**

### 2. What cloud accepts and rejects (storage)

- Docs: limitations table, "Disk-backed named volumes and creating a named volume while mounting it are local-only" (MS `docs/cloud/limitations.mdx` L16). "Flat root disks are not yet available on our managed cloud" (MS `docs/how-to/docker/docker-in-sandbox.mdx` L18, `docs/how-to/ci-cd/compose-tests.mdx` L17). "Flat OCI roots use host-local image artifacts and are not available on our managed cloud" (MS `docs/cli/sandbox-commands.mdx` L163). "our managed cloud accepts managed root disks only" (MS `docs/sdk/python/images.mdx` L16). Cloud create requests "accept only `kind: \"managed\"`" (MS `docs/configuration.mdx` L313). "Owned volumes are currently local-only" (MS `docs/sandboxes/volumes.mdx` L154). Named volumes on cloud: "Inline creation settings and disk-kind volumes are not available" (same file L205) **[read]**.
- SDK client-side rejection: `validate_fresh_cloud_rootfs` refuses tmpfs, disk-image and flat root disks as `"non-managed root_disk"` (MS `sdk/rust/lib/context/cloud/sandbox.rs` L866–876). `reject_dropped_cloud_create_fields` refuses `"named volume inline create"` and `"sandbox-owned volumes"` (L985–998), plus `"nested_virt (local-only)"` (L905). The wire type says Owned is "currently rejected by cloud sandbox creation" (MS `packages/microsandbox-types/rust/lib/cloud/specs.rs` L443) **[read]**.
- Accepted on cloud, per the public API schema `CloudVolumeMount` (MS `docs/api-reference/openapi.json`, schema `CloudVolumeMount`): `bind` (paths in the org host volume), `named`, `tmpfs` (with `size_mib`), and `disk_image` ("Mount a disk image file as a virtio-blk device at a guest path", formats `qcow2|raw|vmdk`). Paths "refer to files in your organization's host volume" (MS `docs/sandboxes/volumes.mdx` L9, L393–397) **[read]**. Cloud volumes are `block`-backed internally (`VolumeType`), but they are exposed and managed as directory volumes (MS `docs/sandboxes/volumes.mdx` L351) **[read]**.
- Root disk size: `CloudSandboxResources.disk_size_mib`, "Writable disk size in MiB. Applies only to OCI root filesystems" (openapi.json L5603). The default "matches requested RAM, bounded to 4–16 GiB" (MS `docs/sandboxes/volumes.mdx` L24–26). On cloud, "Recreate the sandbox to change CPU, memory, mounts, or other settings" (MS `docs/cloud/limitations.mdx` L22) **[read]**.

### 3. Guest kernel

- Kernel version: `KERNEL_VERSION = linux-6.12.118` (KF `Makefile` L1) **[read]**. On cloud, HB recorded `kernel_build: Linux version 6.12.99 (root@libkrunfw) … Fri Jul 24 2026` (2026-09-10 run) and 6.12.98 earlier **[observed]**. Cloud uses libkrunfw builds, but its version can lag the pinned one.
- KF `config-libkrunfw_x86_64` (aarch64 matches for every item checked) **[read]**:
  - Storage: `OVERLAY_FS=y` L2258, `EXT4_FS=y` L2206, `FUSE_FS=y` L2253, `BLK_DEV_LOOP=y` L1294, `TMPFS_XATTR=y` L2305.
  - cgroups: `CGROUPS` L188, `MEMCG` L191, `CGROUP_PIDS` L201, `CGROUP_FREEZER` L203, `CPUSETS` L205, `CGROUP_DEVICE` L208, `CGROUP_BPF` L211. agentd mounts cgroup2 at `/sys/fs/cgroup` (MS `crates/agentd/lib/init.rs` L188–193).
  - Namespaces: `USER_NS` L219, `NET_NS` L221 (PID/IPC/UTS also `=y`).
  - Networking: `BRIDGE` L1129, `BRIDGE_NETFILTER` L975, `VETH` L1415, `VXLAN` L1405, `NF_TABLES` L1018, `NFT_NAT` L1031, `NFT_COMPAT` L1034, `IP_NF_IPTABLES` L1086, `IP_NF_NAT` L1090, `IP_NF_TARGET_MASQUERADE` L1091, `NETFILTER_XT_MATCH_ADDRTYPE` L1054, `NETFILTER_XT_MATCH_CONNTRACK` L1056, `IP6_NF_NAT` L1112.
  - Absent: `IP_VS` L1069 (Swarm service load balancing only), `MACVLAN`/`IPVLAN` L1403–1404, `MODULES` L715 (everything is built in, so `modprobe` is a no-op), `IKCONFIG` L167.
- Netfilter, bridge, VLAN and VXLAN were added for container networking in [libkrunfw PR #6](https://github.com/superradcompany/libkrunfw/pull/6) (closed 2026-04-26), prompted by discussion #598 **[read]**.
- Cloud in-guest probe (HB 2026-09-10): `cap_sys_admin=true`, `is_root=true`, `cgroup_root_writable=true`, `has_fuse=true`, `overlayfs_supported=true`, `userns_mapped=false`, `isolation_runtime=libkrun` **[observed]**. Docker bridge networking itself (creating `docker0`, NAT) was **not** exercised there.

### 4. Privileges

- `SecurityProfile::Default` preserves normal guest-root behaviour. `Restricted` sets `no_new_privs`, drops `CAP_SYS_ADMIN`, and forces `nosuid,nodev` (MS `crates/agentd/lib/config.rs` L141–147, implementation `crates/agentd/lib/session.rs` L1756–1805) **[read]**. Cloud's `CloudSandboxSpec.security_profile` defaults to `"default"` (openapi.json L5805) **[read]**.
- PR [#911](https://github.com/superradcompany/microsandbox/pull/911) "restore default guest privileges" made `default` the default again "so sudo and Docker-in-Docker work". It fixed #908, where the old hardening broke containerd's layer extraction (`failed to mount … fstype: bind … operation not permitted`) **[read]**.
- `--deployment-profile multi-tenant` is a host-side floor (network, DNS, host files). It does not reduce guest capabilities (MS `docs/configuration.mdx` L182, L330–331, L486, L502) **[read]**.

### 5. Lifecycle, quotas and pricing

- Free plan: "ephemeral sandboxes for up to one hour each and removes them when they stop" (MS `docs/cloud/overview.mdx` L49) **[read]**. Cloud is "private beta" (L11–13) **[read]**.
- Plan quota fields (`BillingPlan` and `QuotaUsage` in openapi.json L4505–4559, L7605–7661): `max_disk_size_mib_per_sandbox`, `max_memory_mib_per_sandbox`, `max_vcpus_per_sandbox`, `max_uptime_minutes_per_sandbox`, `max_concurrent_ephemeral_running`, `max_concurrent_persistent_running`, `max_sandboxes`. "On an unpriced (free) plan the compute pool is a hard wall." The values are **not public**. Read them with `GET https://api.microsandbox.dev/v1/quotas` (bearer org API key) **[read]**.
- Lifecycle: `SandboxPolicy.max_duration_secs` (`None` = run forever) and `idle_timeout_secs` (`None` = no idle detection) (openapi.json `SandboxPolicy`). "Cloud sandboxes keep running until stopped or removed, subject to their lifetime limits" (MS `docs/sandboxes/lifecycle.mdx` L12). "An attached exec session or an unexpired job attachment keeps the sandbox active" (L662). Restart timeouts on cloud are capped at 60 s (L142) **[read]**.
- Pricing (MS `docs/cloud/pricing.mdx` L24–47): rates as quoted above. The example "4 vCPU, 8 GiB RAM, and 64 GiB root disk" costs $0.22/h on Standard and $0.035/h on Shared. "Shared requires access on your account" (L18–20). Plans: Builder $20/month with $20 credit, Teams $200/month with $200 credit (L94) **[read]**. The ×730 h monthly figures are my arithmetic.

### 6. Known examples

- Docs: only local Docker guides exist (MS `docs/how-to/docker/docker-in-sandbox.mdx`, `docs/how-to/ci-cd/compose-tests.mdx`). The GitHub runner guide (MS `docs/how-to/ci-cd/github-actions-runner.mdx`) carries no cloud badge and uses no Docker: `ghcr.io/actions/actions-runner`, `--cpus 2 --memory 4G --max-duration 1h`, a JIT config piped into `run.sh` as user `runner` **[read]**.
- Issues and discussions: [#908](https://github.com/superradcompany/microsandbox/issues/908) (local DinD mount failure, fixed by #911). [Discussion #598](https://github.com/superradcompany/microsandbox/discussions/598) (local, with kernel netfilter and overlay-on-overlay history and the maintainer's tmpfs data-root workaround). [#1604](https://github.com/superradcompany/microsandbox/issues/1604) (exposing the dockerd port, closed). [#2020](https://github.com/superradcompany/microsandbox/issues/2020) (open: `/dev/fuse` is 0600 in the guest, which only matters for non-root fuse-overlayfs). **None mention cloud** **[read]**.
- HB is the only public cloud data point. Its DinD probe (HB `lib/probe/isolation/40-dind.sh`) reported `dind_readiness=blocked` with the sole reason "no a dockerd or podman binary" **[observed]**.

---

## Open questions and the experiment that settles them

Unknowable from public sources: the current cloud root layout (overlay or ext4), the current cloud kernel config, and this account's plan quotas. Run this against the remote profile (`msb context use <remote>`). It takes under 30 minutes and about $0.10 on Standard.

```sh
# 0. Plan limits (disk, memory, uptime, concurrency)
curl -s -H "Authorization: Bearer $MSB_API_KEY" https://api.microsandbox.dev/v1/quotas | jq .

# 1. Boot a DinD probe on a plain managed root
msb create docker:dind --name dind-probe --cpus 2 --memory 4G --root-disk 20G --max-duration 30m

# 2. Layout, privileges, kernel features
msb exec dind-probe -- sh -c '
  uname -r; awk "\$5==\"/\"" /proc/self/mountinfo | tail -1; df -hT / ; ls /sys/block
  grep CapEff /proc/self/status; grep -E "overlay|fuse" /proc/filesystems
  ip link add br-probe type bridge && ip link add v0 type veth peer name v1 && echo bridge+veth OK
  iptables -t nat -L -n >/dev/null && echo iptables-nat OK; nft list ruleset >/dev/null && echo nft OK'

# 3. Docker as-is (strategy A): which storage driver did dockerd pick?
msb exec dind-probe -- sh -c '
  dockerd >/tmp/dockerd.log 2>&1 &
  timeout 60 sh -c "until docker info >/dev/null 2>&1; do sleep 1; done" || { tail -50 /tmp/dockerd.log; exit 1; }
  docker info --format "{{.Driver}} cgroup={{.CgroupVersion}}"
  docker run --rm hello-world
  printf "FROM alpine\nRUN echo built > /x\n" | docker build -q -
  docker run -d --name svc -p 8080:80 nginx:alpine && sleep 3 && wget -qO- localhost:8080 | head -3'

# 4. If step 3 shows vfs/fuse-overlayfs or an "upperdir" error, try strategy B (loop ext4):
msb exec dind-probe -- sh -c '
  pkill dockerd; sleep 2; rm -rf /var/lib/docker; mkdir -p /var/lib/docker
  truncate -s 12G /docker.img && mkfs.ext4 -q /docker.img && mount -o loop /docker.img /var/lib/docker
  dockerd >/tmp/dockerd.log 2>&1 & sleep 10; docker info --format "{{.Driver}}"; docker run --rm hello-world'

# 5. And strategy C (tmpfs) in a fresh sandbox:
msb create docker:dind --name dind-tmpfs --cpus 2 --memory 6G --tmpfs /var/lib/docker:4096 --max-duration 30m
msb exec dind-tmpfs -- sh -c 'dockerd >/tmp/d.log 2>&1 & sleep 10; docker info --format "{{.Driver}}"; docker run --rm hello-world'

msb rm -f dind-probe dind-tmpfs
```

How to read the results. If step 3 reports `overlay2` (or the containerd `overlayfs` snapshotter) and the build and service container work, strategy A holds: the remote Backend needs no Docker disk at all, only a large enough `--root-disk`. If step 3 fails with `not supported as upperdir` or falls back to `vfs`, choose between B and C.
