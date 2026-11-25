# Can CI run real msb microVMs on each Backend?

Research for [#21](https://github.com/arteven/gh-runners-msb/issues/21), the map's "Test strategy" fog. It builds on [#3](https://github.com/arteven/gh-runners-msb/issues/3) (SDK per Backend), [#6](https://github.com/arteven/gh-runners-msb/issues/6) and [#12](https://github.com/arteven/gh-runners-msb/issues/12) (release matrix; integration tests do not gate releases), [#13](https://github.com/arteven/gh-runners-msb/issues/13) (measured Free-plan quotas) and [#11](https://github.com/arteven/gh-runners-msb/issues/11) (Pool lifecycle and the Drain). Researched 2026-11-25.

Nothing was run for this research: no msb sandbox (local or remote), no runner, no repo, no workflow. Everything comes from documentation, source files, and the public logs of other projects' finished workflow runs.

Tags: **[read]** means a source states it. **[observed]** means it appears in a public CI log. **[inferred]** means it is my reasoning and no source states it.

Source pins:

- **willet**: `gerritlansing/willet` @ [`e314f11`](https://github.com/gerritlansing/willet/tree/e314f117c8a55fcedad3c644cdca620fee29cafa) (main). Its CI run [41310059334](https://github.com/gerritlansing/willet/actions/runs/41310059334) on that commit, job `Integration` [124858355012](https://github.com/gerritlansing/willet/actions/runs/41310059334/job/124858355012) (2026-11-01).
- **MS**: `superradcompany/microsandbox` @ [`c75e80c`](https://github.com/superradcompany/microsandbox/tree/c75e80c1f2990d80a6ec00906fb65cd58afad92d) (main).
- **ARC**: `actions/actions-runner-controller` @ [`eca1a62`](https://github.com/actions/actions-runner-controller/tree/eca1a62a7fbe50bb1b21ba55fe923bde653c6f9e) (master).
- **dracut**: `dracut-ng/dracut` @ [`e91daeb`](https://github.com/dracut-ng/dracut/tree/e91daebed9aeb516807c00bee18ae16220ff5eee). Its run [45604347680](https://github.com/dracut-ng/dracut/actions/runs/45604347680) (2026-11-25) is used as a KVM probe for both Linux architectures.
- **minimal**: `gominimal/minimal` @ [`7b9ec7d`](https://github.com/gominimal/minimal/tree/7b9ec7d493885a4ea4cbe66f5fe07cb98dbced64), a libkrun-based project, used for corroboration.
- docs.github.com (fetched 2026-11-25), docs.microsandbox.dev (fetched 2026-11-25), and `actions/runner-images` issues.

**Blocked hosts** (sandbox egress approval, HTTP 403): `github.blog` (the 2023 and 2024 changelog posts about KVM on hosted runners), `microsandbox.dev` (pricing page and plan table) and `developer.apple.com` (Virtualization framework nested-virtualization docs). Where these would have been the best source, a GitHub-staff statement, a public CI log, or our own #13 measurement stands in, and the text says so.

---

## Short answers

- **`ubuntu-24.04` (x64): yes, KVM works.** `/dev/kvm` exists but the `runner` user can't open it. One udev rule fixes that. willet boots real msb microVMs there on every PR. Its whole integration job takes **about 4.5 min**: runtime install about 2 s, a Runner-like VM from a cached image about 1 s, and a pull plus boot of `ghcr.io/actions/actions-runner` about 7 s **[observed]**.
- **`ubuntu-24.04-arm`: no usable KVM today.** A runner-images maintainer says the arm64 hosted fleet doesn't surface `/dev/kvm` **[read]**. A dracut run on 2026-11-25 fell back to software emulation on arm64 but used `-accel kvm` on x64, in the same workflow **[observed]**. One user report claims the opposite **[read]**, so a 10-second probe job would settle it for good. msb doesn't fall back to emulation, so the local Backend can't run there **[read + inferred]**.
- **Larger runners: not available to this repo.** They need an org on GitHub Team or Enterprise Cloud, and `arteven` is a user account **[read]**. Their x64 Linux SKUs probably have KVM like the standard ones **[inferred]**. arm64 larger runners are probably in the same situation as `ubuntu-24.04-arm` **[inferred]**.
- **`macos-15` (and every hosted macOS arm64 runner): no.** "Nested-virtualization is not supported due to the limitation of Apple's Virtualization Framework" **[read]**. msb on macOS needs Hypervisor.framework (`com.apple.security.hypervisor`) inside what is already a VM **[read]**. So macOS can only test the remote Backend, plus the FFI smoke test that #12 already runs.
- **Remote Backend: technically easy, but the quota limits it.** CI needs an `MSB_API_KEY` repo secret and `MSB_BACKEND=remote`. No KVM is needed, so any runner works, macOS included **[read]**. The Free plan caps each test at **1 running sandbox, 3 sandboxes total, 2 vCPU, 1 GiB, 60 min, ephemeral only, shared compute** (measured in #13). Tests must be serialized (`concurrency` with `queue: max`), skipped on fork PRs (secrets aren't passed), and must clean up by label, because remote sandboxes outlive a cancelled job (#3). Whether a real Runner with `dockerd` can run on remote at all is still open: the #13 probe hasn't run its Docker strategies.
- **Prior art.** willet runs **one** integration job on `ubuntu-24.04` (amd64 only), with `go test -tags integration` and a 25 min test timeout, and it has no remote tests and no real GitHub. microsandbox runs every VM test on a **self-hosted** x64 KVM machine (`self-hosted-ubuntu-2404-x64`). It uses `macos-15` and `ubuntu-24.04-arm` only to build. Its CI never creates a real cloud sandbox: the only remote check is `msb context` with a dummy key **[read]**.
- **Drain (422 on a busy runner): testable, without msb at all.** ARC's own code depends on it and quotes the error: `422 Bad request - Runner "…" is still running a job` **[read]**. The REST docs only list 422 as "Validation failed" **[read]**. The test needs a token with **Administration: write** (GITHUB_TOKEN can't have that), a repo where a job can be dispatched onto the JIT runner, and a `sleep` job to keep it busy. The runner binary can run directly on the hosted VM, so the test doesn't depend on the Backend **[inferred]**.

---

## 1. Local Backend on GitHub-hosted runners

### Linux x64 (`ubuntu-24.04`, `ubuntu-latest`)

- **Hardware.** Public repos get 4 vCPU / 16 GB / 14 GB SSD on `ubuntu-24.04`, and private repos 2 vCPU / 8 GB **[read]** ([docs: GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners#standard-github-hosted-runners-for-public-repositories)). This repo is public, and standard runners are "free and unlimited on public repositories" **[read]**.
- **KVM is present.** The current docs.github.com runner pages don't mention KVM or nested virtualization for Linux **[read]**. The official statements are:
  - The 2023-02-23 and 2024-04-02 changelog posts. `github.blog` was blocked, so they weren't read directly. A runner-images contributor cites the 2024 post as the hardware-acceleration source in [actions/runner-images#12933](https://github.com/actions/runner-images/issues/12933) **[read]**.
  - A GitHub staff comment in [community discussion #160591](https://github.com/orgs/community/discussions/160591) (2024-04-05): "This has now shipped :) All Linux runners are now on a SKU that supports nested virtualization" **[read]**.
- **The runner user can't open `/dev/kvm` by default.** The image adds no KVM rule: `images/ubuntu/scripts/build/` contains no `kvm` reference **[read]**. The fix GitHub itself suggested ([runner-images#7670](https://github.com/actions/runner-images/issues/7670), community #160591) is the step willet uses ([`ci.yml` L74-79](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/.github/workflows/ci.yml#L74-L79)):
  ```sh
  echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' | sudo tee /etc/udev/rules.d/99-kvm.rules
  sudo udevadm control --reload-rules
  sudo udevadm trigger --name-match=kvm
  ```
  In willet's log it takes under 1 s and leaves `crw-rw-rw- 1 root kvm 10, 232 /dev/kvm` **[observed]**. minimal's KVM lane does the same thing and fails fast if `/dev/kvm` is missing ([`ci-linux-kvm.yml` L267-280](https://github.com/gominimal/minimal/blob/7b9ec7d493885a4ea4cbe66f5fe07cb98dbced64/.github/workflows/ci-linux-kvm.yml#L267-L280)) **[read]**.
- **msb installs and runs there.** willet's `Integration` job ran on `ubuntu-24.04` image `20261025.353.1` (Azure, westus3). It installed msb **0.7.12** into `~/.microsandbox` through the SDK's `EnsureRuntime` and booted real microVMs. All tests passed **[observed]**.

**Cold-start numbers from that job** **[observed]** (timestamps from the job log; msb 0.7.12, 4 vCPU runner):

| What | Time |
|---|---|
| Whole job (checkout, setup-go, KVM rule, `make integration`) | 4 min 33 s |
| `make integration` step / `go test` package | 4 min 12 s / 246 s |
| From the first VM test's start to `microsandbox runtime ready` (runtime download and install on a fresh VM) | ~1.9 s |
| `TestRunnerExitIsObserved`: boot a VM from the cached `actions-runner` image, start `run.sh` with a bogus JIT config, and see it exit | 1.16 s |
| `TestRefreshWithRealRunnerImage`: resolve and pull `ghcr.io/actions/actions-runner`, then boot to check `run.sh` and the user | 7.03 s |
| `TestDestroyStopsRunningRunner`: boot, idle 3 s, destroy (destroy alone took 82 ms) | 3.79 s |
| `TestDockerInRunnerVM`: build a Docker-enabled runner image inside a microVM (~105 s), then boot it and run Docker (~10 s) | 117 s |
| `TestScalerWithRealVMs`: willet's scaler on several real VMs | 4.98 s |

- microsandbox claims "average boot times under 100 milliseconds", measured on an M1 ([README L55, L498](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/README.md#L55)) **[read]**. The docs give no boot-time figure **[read]**.
- **For us [inferred]:** a Self-test style boot of our Runner image is dominated by the image pull and `dockerd` start, not by the VM boot. A local-Backend integration job that pulls the Runner image once and boots a handful of Runners should fit in about 5–10 min, well inside a 30 min `timeout-minutes`. willet doesn't measure `dockerd` readiness on its own, so how much of our Self-test's 90 s budget a cold `dockerd` uses on a hosted runner is **unknown**.

### Linux arm64 (`ubuntu-24.04-arm`)

- The runners exist for public and private repos (4 vCPU / 16 GB for public repos) **[read]** ([docs](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)).
- **The maintainer's statement.** [runner-images#14062](https://github.com/actions/runner-images/issues/14062), "Please support KVM on ARM runners", was closed 2025-12 by a runner-images contributor: "`/dev/kvm` has to be exposed by the underlying host … and the ARM64 hosted fleet doesn't surface a nested KVM device … nested virtualization isn't officially supported on GitHub-hosted runners in general." It points to community #160591, where the arm64 request was still open on 2026-08-20 **[read]**.
- **A public CI log from today agrees.** dracut's `integration.yml` runs the same test on both architectures with `--device=/dev/kvm --privileged` ([L272-295](https://github.com/dracut-ng/dracut/blob/e91daebed9aeb516807c00bee18ae16220ff5eee/.github/workflows/integration.yml#L272-L295)). Its `run-qemu` adds `-accel kvm -cpu host` only when `/dev/kvm` is a readable and writable character device ([`test/run-qemu` L140-145](https://github.com/dracut-ng/dracut/blob/e91daebed9aeb516807c00bee18ae16220ff5eee/test/run-qemu#L140-L145)) **[read]**. In run 45604347680 (2026-11-25):
  - The `ubuntu-24.04` jobs launched `qemu-system-x86_64 -accel kvm -cpu host`.
  - The `ubuntu-24.04-arm` jobs (image `20261116.188.1`) launched `qemu-system-aarch64 -cpu neoverse-n1` with no KVM, which is TCG software emulation **[observed]**.
  - The container was privileged and ran as root, so the missing device can't be a permission problem **[inferred]**.
- **Corroboration.** minimal's KVM workflow says "GitHub-hosted arm64 runners (ubuntu-24.04-arm) do not expose /dev/kvm, so the Linux/KVM tests can only run on x86_64" ([L10-12](https://github.com/gominimal/minimal/blob/7b9ec7d493885a4ea4cbe66f5fe07cb98dbced64/.github/workflows/ci-linux-kvm.yml#L10-L12)) **[read]**.
- **One contrary report.** [runner-images#14549](https://github.com/actions/runner-images/issues/14549) (2026-07, about `ubuntu-26.04-arm`) says in passing that "on `ubuntu-24.04-arm`, `/dev/kvm` exists and KVM acceleration works". It was closed with no maintainer comment **[read]**. This conflicts with the evidence above. KVM may be available on only part of the fleet, or the report may be wrong. **Unresolved without a probe job** (see Unknowns).
- **msb needs KVM.** "microsandbox local sandboxes use hardware virtualization. On Linux, confirm … that KVM is available through `/dev/kvm`" … "If you are inside a cloud VM, CI runner, or another hypervisor, the outer environment must expose nested virtualization before `/dev/kvm` can appear" ([docs: Troubleshooting Linux](https://docs.microsandbox.dev/troubleshooting/linux)) **[read]**. The docs describe no software-emulation fallback **[read]**.
- **Consequence [inferred]:** the `linux-arm64` binary and the arm64 Runner image can't be booted on hosted CI. The arm64 local Backend stays covered only by the FFI-loading smoke test in #12's release gate. An arm64 Runner image can't be booted on an x64 KVM runner either, because KVM only accelerates a guest of the same architecture (runner-images#14062) **[read]**.

### Larger runners

- "Larger runners are available for organizations and enterprises on GitHub Team and GitHub Enterprise Cloud plans" ([docs: Larger runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners#larger-runners)) **[read]**. `arteven` is a `User` account (`gh api users/arteven`), so this repo can't use them **[observed]**.
- The Linux larger runners come in 2–64 vCPU, x64 and arm64 ([docs: Larger runners reference](https://docs.github.com/en/actions/reference/runners/larger-runners)) **[read]**. The reference page says nothing about KVM. The 2023-02-23 changelog announced hardware-accelerated Android virtualization on Linux larger runners first (cited in community #160591, but not read directly because `github.blog` was blocked). The KVM status of arm64 larger runners is **unknown**, and #14062's "ARM64 hosted fleet" wording suggests no **[inferred]**.

### macOS (`macos-15`, `macos-latest`, `-xlarge`)

- Standard `macos-15` is arm64 **M1**, 3 vCPU / 7 GB. `-xlarge` is **M2**, 5 vCPU / 14 GB **[read]**.
- Both runner docs say: "Nested-virtualization is not supported due to the limitation of Apple's Virtualization Framework" ([standard](https://docs.github.com/en/actions/reference/runners/github-hosted-runners#limitations-for-arm64-macos-runners), [larger](https://docs.github.com/en/actions/reference/runners/larger-runners#limitations-for-macos-larger-runners)) **[read]**.
- msb on macOS runs its VMM through Hypervisor.framework. `msb` is signed with `com.apple.security.hypervisor` ([`msb-entitlements.plist`](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/msb-entitlements.plist), [`check-platform.yml` L127-129](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/check-platform.yml#L127-L129)) **[read]**. Inside a runner that is itself a Virtualization-framework guest with no nested virtualization, Hypervisor.framework can't create a VM **[inferred]**.
- Neither microsandbox nor minimal boots a VM on hosted macOS:
  - microsandbox's `macos-15` lanes only build and check ([`check.yml` L475-499](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/check.yml#L475-L499)).
  - minimal boots on HVF only on `runs-on: [self-hosted, macOS, ARM64]` (a Mac mini) ([`ci-macos.yml` L269](https://github.com/gominimal/minimal/blob/7b9ec7d493885a4ea4cbe66f5fe07cb98dbced64/.github/workflows/ci-macos.yml#L269)) **[read]**.
- Apple's own docs (which say which chips support nested virtualization in the Virtualization framework) weren't reachable (`developer.apple.com` blocked). Hosted M1/M2 hardware probably couldn't support it even if GitHub enabled it **[inferred]**.
- **What macOS CI can do:** the FFI smoke test (#12) and **remote-Backend** tests, which need no host hypervisor ("You do not need a local runtime or hardware virtualization to use our managed cloud", [docs: Managed cloud](https://docs.microsandbox.dev/cloud/overview)) **[read]**.

---

## 2. Remote Backend in CI

- **Credentials and selection.** Create an API key (`msb_…`, organization-scoped) in the dashboard. `export MSB_API_KEY=…` supplies it, and `MSB_BACKEND=remote` switches unqualified SDK and CLI calls to remote. `MSB_API_KEY` alone does not switch ([docs: Connect](https://docs.microsandbox.dev/cloud/connect), [API overview](https://docs.microsandbox.dev/api-reference/overview)) **[read]**. In Go, `WithRemoteAPIKey(...)` / `NewRemoteContextWithOptions` can pin the Backend explicitly instead **[read]**. In CI this means one repository secret, `MSB_API_KEY`, plus `env: MSB_BACKEND: remote` on the job **[inferred]**.
- **Secrets and forks.** "With the exception of `GITHUB_TOKEN`, secrets are not passed to the runner when a workflow is triggered from a forked repository" ([docs: Using secrets](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets)) **[read]**. Remote tests must therefore run on `push` to `master`, `schedule` or `workflow_dispatch`, or skip themselves when the secret is empty **[inferred]**.
- **The plan must allow ephemeral sandboxes**, which the Free plan does, and *only* those. "The Free plan runs ephemeral sandboxes for up to one hour each and removes them when they stop" ([docs: Managed cloud](https://docs.microsandbox.dev/cloud/overview)) **[read]**. A non-ephemeral create returns `403 … the Free plan runs ephemeral sandboxes only` (#13) **[observed]**. The Pool already uses `WithEphemeral` on both Backends (#11).
- **Free-plan quotas** (`GET /v1/quotas`, measured in #13) **[observed]**:
  - `max_concurrent_running: 1`, `max_sandboxes: 3`, `ephemeral_only: true`
  - `max_vcpus_per_sandbox: 2`, `max_memory_mib_per_sandbox: 1024`, `max_disk_size_mib_per_sandbox: 32768`, `max_total_storage_gib: 1`
  - `max_uptime_minutes_per_sandbox: 60`, `compute_types: ["shared"]`, `included_vcpu_hours: 0`, `hard_cap: false`
- **What those limits allow [inferred]:**
  - **Concurrency 1.** Only one remote sandbox can run at a time across *all* jobs that use the key. So:
    - Remote tests run sequentially inside one job, never in a matrix.
    - The workflow sets `concurrency: {group: msb-remote, queue: max}`. Without `queue: max`, a third run cancels the pending one, and `queue: max` can't be combined with `cancel-in-progress: true` ([docs: Control concurrency](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency)) **[read]**.
    - A developer using the same key locally will collide with CI. Use a separate key, or ideally a separate org.
    - A Pool test with `--slots 2` is impossible on Free.
  - **60 min uptime.** Fine for tests, but it means a Drain or job test on remote must finish within an hour.
  - **1 GiB / 2 vCPU, shared compute.** Enough for lifecycle tests (create, exec, label list, destroy, a `run.sh` with a bogus JIT config, as willet does locally). It is probably too small for a Runner running `dockerd` plus a real job. #13 hasn't yet run its Docker strategies, so Docker-in-Runner on remote is **unproven on any plan**.
  - **Cleanup.** Remote sandboxes outlive the creating process. `WithEphemeral` alone can leak a stopped sandbox when a create is cancelled (microsandbox#1687, see #3 and #11). A cancelled CI job can leave sandboxes that hold the single concurrency slot or the 3-sandbox cap. Each remote job should start and end (`if: always()`) by listing sandboxes with a per-run label and destroying them, and should set a short `WithMaxDuration`.
  - **Cost.** `included_vcpu_hours: 0` with `hard_cap: false` doesn't say whether Free-plan usage is free or billed. The pricing page (`microsandbox.dev/pricing`) was blocked, and the docs give rates only for Builder ($20/month incl. $20 credit) and Teams ([docs: Pricing](https://docs.microsandbox.dev/cloud/pricing)) **[read]**. A short Shared sandbox (2 vCPU, 1 GiB) would cost about $0.015/h at Shared rates if it were billed **[read + arithmetic]**.
- **Cold start on remote: unknown.** No public log or doc gives remote create or boot latency, and this research ran nothing.
- **Runner choice for remote tests [inferred]:** `ubuntu-24.04`, because it is free on this public repo. `macos-15` also works, and it is the only way to run real sandbox tests from a darwin-arm64 build of our binary.

---

## 3. Prior art

### willet

- **Workflow** ([`ci.yml`](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/.github/workflows/ci.yml)): runs on `pull_request` and `push` to `main`.
  - Job `test` is a matrix of `ubuntu-24.04` and `ubuntu-24.04-arm`, running gofmt, `make test` (vet and race-enabled unit tests) and `make build`.
  - Job `integration` runs on **`ubuntu-24.04` only**, with `timeout-minutes: 30`: checkout, setup-go, the KVM udev rule, `make integration` ([L61-81](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/.github/workflows/ci.yml#L61-L81)) **[read]**.
- **Target**: `go test -tags integration -count=1 -timeout 25m -v ./internal/microvm/` ([`Makefile` L14-15](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/Makefile#L14-L15)) **[read]**. Integration files carry `//go:build integration` **[read]**.
- **Runtime**: no install step in the workflow. The tests call the SDK's `EnsureRuntime`, which downloads msb and libkrunfw matching the SDK (v0.7.12) into `~/.microsandbox`. CONTRIBUTING notes that 0.7.12+ refuses a runtime home with a group- or world-writable parent, and that `MSB_HOME` must be short (Unix socket paths are limited to 107 bytes) ([CONTRIBUTING L13-20](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/CONTRIBUTING.md#L13-L20)) **[read]**.
- **What the tests do** ([`microvm_integration_test.go`](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/internal/microvm/microvm_integration_test.go), [`scaler_integration_test.go`](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/internal/microvm/scaler_integration_test.go)) **[read]**:
  - They use the real `ghcr.io/actions/actions-runner` image with 1 vCPU / 1024 MiB.
  - A **bogus JIT config** makes the real `run.sh` start, reject the config and exit, which tests exit observation. That test notes that `run.sh` exits 0 even then.
  - A fake `run.sh` (`exec sleep 3600`) injected with `WithPatches` stands in for an idle runner.
  - An owner label gives orphan cleanup that spares other owners' VMs.
  - A cancelled-create test checks for leaks (microsandbox#1687).
  - The scaler test runs on real VMs with a **fake GitHub client**.
  - A local `crane` registry serves test images.
- **What willet does not do:** run integration on arm64 or macOS, touch the real GitHub API, or test against msb cloud **[read]**. Its commit adding CI gives no reason for leaving arm64 out ([`9071dd0`](https://github.com/gerritlansing/willet/commit/9071dd0)) **[read]**.

### microsandbox

- **All VM tests run on a self-hosted x64 KVM machine.** `cli-smoke-test`, `integration-suites`, `node-sdk-test`, `python-sdk-test` and `go-sdk-test` all use `runs-on: self-hosted-ubuntu-2404-x64` ([`check.yml` L1657-1663, L1764-1777, L2281-2293](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/check.yml#L1764-L1777)) **[read]**. They cap fan-out at `max-parallel: 2` because "their microVMs still share the physical KVM host and root disk", and they isolate `~/.microsandbox` per test with `MSB_TEST_ISOLATE_HOME=1` **[read]**. The maintainer set up that runner because "we need KVM" ([microsandbox#372](https://github.com/superradcompany/microsandbox/issues/372)) **[read]**.
- **Hosted runners only build.** `ubuntu-latest`, `ubuntu-24.04-arm`, `macos-15` and `windows-*` build, lint and run unit tests. The workflow says it outright: "VM smoke and integration coverage intentionally stays Linux/KVM-only" ([L520](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/check.yml#L520)) **[read]**. Windows VM tests are planned on custom Azure VMs with nested virtualization and ephemeral runners ([microsandbox#2015](https://github.com/superradcompany/microsandbox/issues/2015)) **[read]**.
- **The Go SDK's integration tests** run `go test -tags "integration microsandbox_ffi_path" ./integration` in 2 shards, against the FFI library and runtime built earlier in the same workflow ([L2281-2387](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/check.yml#L2281-L2293)) **[read]**.
- **No real cloud tests.** The only remote check runs `msb context` with `MSB_BACKEND=remote MSB_API_KEY=msb_test_ci` and asserts `kind == "remote"` ([L363-367](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/check.yml#L363-L367)). `check.yml` references no repository secrets **[read]**.

---

## 4. Testing the Drain's 422 assumption

**What the sources say.**

- The REST reference for `DELETE /repos/{owner}/{repo}/actions/runners/{runner_id}` lists `204` and `422 Validation failed, or the endpoint has been spammed`. It needs repo admin plus `repo` scope, or Administration: write ([docs](https://docs.github.com/en/rest/actions/self-hosted-runners#delete-a-self-hosted-runner-from-a-repository)) **[read]**. "Busy" isn't mentioned.
- **ARC (maintained by the `actions` org) relies on exactly this behaviour** in `controllers/actions.summerwind.net/runner_graceful_stop.go` **[read]**:
  - "RemoveRunner already has an ability to prevent stopping a busy runner … Trying to remove a busy runner can result in errors like … `422 Bad request - Runner \"example-runnerset-0\" is still running a job`" ([L433-452](https://github.com/actions/actions-runner-controller/blob/eca1a62a7fbe50bb1b21ba55fe923bde653c6f9e/controllers/actions.summerwind.net/runner_graceful_stop.go#L433-L452)).
  - It sets `runnerBusy = StatusCode == 422` ([L223](https://github.com/actions/actions-runner-controller/blob/eca1a62a7fbe50bb1b21ba55fe923bde653c6f9e/controllers/actions.summerwind.net/runner_graceful_stop.go#L223)).
  - For **ephemeral** runners it then waits for the job to finish instead of retrying. Static runners are retried after a delay, so as not to end up "spamming 422 errors" ([L238-260](https://github.com/actions/actions-runner-controller/blob/eca1a62a7fbe50bb1b21ba55fe923bde653c6f9e/controllers/actions.summerwind.net/runner_graceful_stop.go#L238-L260)).

  This is the same design as our Drain.
- ARC also notes that `busy` in `ListRunners` responses can be up to 60 s stale (`Cache-Control`) **[read]**. So a Pool should trust the DELETE's status code, not the list's `busy` field.
- msb-github-runner's comment that #11 cites: "Busy runners refuse deletion (422), which is fine" ([`controller/msb-runner.sh` L75-82](https://github.com/Darkflib/msb-github-runner/blob/cf9f77e2987fa6983e2648772bad9c820922e97d/controller/msb-runner.sh#L75-L82)) **[read]**.
- Not found in any source: whether a **JIT** runner (as opposed to a `config.sh --ephemeral` runner like ARC's) gets the same 422, and whether the message text is stable. Same endpoint and same runner object, so probably yes **[inferred]**.

**What a CI test would need [inferred].**

1. **A token with Administration: write** on the target repo, plus Actions: write if it dispatches a workflow there. Use a fine-grained PAT or a GitHub App installation token stored as a secret. `GITHUB_TOKEN` can't do it: its `permissions` keys have no `administration` ([docs: workflow syntax, `permissions`](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions)) **[read]**. As a secret it isn't available on fork PRs, so the test runs on `push`, `schedule` or `workflow_dispatch` only.
2. **A repo where a job can land on the test runner.** A throwaway **private** repo is safest. GitHub says self-hosted runners "should almost never be used for public repositories" ([docs: Secure use](https://docs.github.com/en/actions/reference/security/secure-use)) **[read]**. With a unique per-run label and a JIT runner, the exposure in this public repo would be small but not zero. Self-hosted jobs use no hosted minutes, so a private throwaway repo costs nothing.
3. **A job that holds the Runner busy:** a `workflow_dispatch` workflow in that repo with `runs-on: [self-hosted, <run-unique-label>]` and a single `sleep 120` step.
4. **The test itself**, running on `ubuntu-24.04`:
   1. Call `generate-jitconfig` with the unique label.
   2. Start the official runner with `run.sh --jitconfig` directly on the hosted VM, or inside a local-Backend Runner if the test should also cover the Runner image.
   3. Dispatch the workflow, poll the run until its job is `in_progress` on this runner, then `DELETE /runners/{id}` and expect `422` (and log the message).
   4. Let the job finish, then expect the runner to be gone (it is ephemeral).
   5. Repeat with no job and expect `204`, and that the idle listener exits by itself (see #5).
5. **Cleanup:** an `if: always()` step that cancels the dispatched run and deletes any runner whose name has the run's prefix.

The 422 check doesn't involve msb at all, so it can live in its own small workflow, separate from the Backend tests, and a one-off `workflow_dispatch` run is enough to confirm the assumption.

---

## What this means for the Test strategy

**[inferred]**, for the decision ticket to weigh:

1. **The local Backend gets real coverage on `ubuntu-24.04` (x64) only.** Copy willet's shape: a separate `integration` job, the udev KVM rule, `go test -tags integration` with a 25–30 min cap, and no secrets, so it can run on PRs, including forks. Per #12 it doesn't gate releases.
2. **arm64 and darwin get no hosted local-Backend coverage.** Their only checks are the existing FFI smoke test, or a self-hosted runner, which is what microsandbox and minimal do. That is out of scope for a user-account repo.
3. **Remote-Backend tests are optional and secret-gated.** Run them serially under one `concurrency` group with `queue: max`, with label-based cleanup before and after. They are limited to lifecycle checks until #13's Docker probe shows a Runner with `dockerd` can run on remote, and on Free to one sandbox at a time, ≤ 1 GiB and ≤ 60 min.
4. **The Drain's 422 is cheap to confirm once**, with a `workflow_dispatch` workflow plus an admin-scoped secret and a throwaway repo. It doesn't need to be part of every CI run.

## Unknowns

- **Does `ubuntu-24.04-arm` expose `/dev/kvm` on any part of the fleet?** A maintainer and today's dracut log say no, and one user report says yes. A 10-second probe job (`ls -l /dev/kvm; grep -c -E 'vmx|svm' /proc/cpuinfo; dmesg | grep -i kvm`) repeated a few times would settle it.
- **KVM on larger runners**, especially arm64. Not documented, and not testable from this user-account repo.
- **Cold-start time of our own Runner image** on a hosted runner: pull size, `dockerd` readiness against the Self-test's 90 s budget. willet's numbers are for `actions-runner` plus its own Docker image, on msb 0.7.12, not 0.8.x.
- **Remote create and boot latency**, and whether Free-plan usage is billed (`included_vcpu_hours: 0`, `hard_cap: false`; the pricing page was blocked).
- **Docker inside a Runner on remote** (the #13 probe strategies haven't run yet), and whether 1 GiB on Free is enough for any `dockerd`-based Runner.
- **Whether `DELETE` on a busy JIT runner returns the same `422 … is still running a job`** as ARC's `config.sh` runners, and whether a JIT runner that is mid-assignment (job accepted, not yet started) counts as busy.
- **Apple's exact nested-virtualization hardware requirement**, which would say whether hosted macOS could ever run msb. `developer.apple.com` was blocked.
