# What does the msb Go SDK offer the Pool on each Backend?

Research for [#3](https://github.com/arteven/gh-runners-msb/issues/3). Feeds image delivery (#9) and the Pool lifecycle (#11).

Sources were read on 2026-11-25:

- **microsandbox** at tag `sdk/go/v0.8.2` (commit `9978e22`, the latest Go SDK release, published 2026-11-24). The Rust crates and `docs/` at that tag match `main` (`c75e80c`) byte for byte. Only lockfiles and the README differ.
- **microsandbox** at tag `sdk/go/v0.7.12` (commit `a66cf13`), which willet pins.
- **willet** at commit `e314f11`.

Abbreviations used in citations:

- `GO/` = `sdk/go/` at `sdk/go/v0.8.2`, for example `GO/core/context.go:186`. The released Go tree differs from `main`: the release commit splits it into `core/`, `local/` and `remote/`.
- `RS/` = `sdk/rust/lib/`.
- `DOCS/` = `docs/`, the source of docs.microsandbox.dev.
- `W/` = willet.

Full URLs have the form `https://github.com/superradcompany/microsandbox/blob/sdk/go/v0.8.2/<path>#L<n>` and `https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/<path>#L<n>`.

**(inferred)** marks a conclusion I drew from the code without seeing it stated or exercised. Everything else was read in the cited file.

## Short answers

| Pool need | Local Backend | Remote Backend |
|---|---|---|
| **Respect the active msb profile** | Yes. Package-level calls (`CreateSandbox`, `ListSandboxesWith`, `GetSandbox`, …) resolve the configured destination on every call. The order is managed `active_profile` > `MSB_BACKEND` > `MSB_PROFILE` > saved `active_profile` > local. | Same. Remote profiles need `api_key_ref` set to `env:` or `inline:`. `keyring:` is accepted by the parser but fails with "not yet wired". |
| **Know which Backend is active before creating anything** | **No public Go API in v0.8.2.** v0.7.12 had `DefaultBackendInfo()`; v0.8 made it internal. Workarounds are below. | Same. |
| **Create from a registry image** | `WithImage` + `WithPullPolicy(always / if-missing / never)`. Default is if-missing. | Same option. The pull happens server-side. |
| **Private registry auth** | `WithRegistryAuth`. Otherwise the host's credentials resolve in this order: msb keyring → `config.json` → `~/.docker/config.json` → anonymous. `WithRegistryAnonymous` forces anonymous. | `WithRegistryAuth` is sent as sandbox-scoped inline credentials. Without it the cloud uses credentials stored in the cloud for that registry. Host Docker credentials are not used **(inferred)**. Plain HTTP and custom CAs are rejected. |
| **Load a local OCI or Docker tarball (`msb load`)** | `microsandbox.Image.Load(ctx, path, tags...)`. It takes a file path, not stdin. | **Not possible.** Fails with "image ops require a local backend". The image must come from a registry the cloud can reach. |
| **Pre-pull an image (`msb pull`)** | **No Go API.** Create a throwaway sandbox with `PullPolicyAlways`, or use `CreateSandboxWithProgress`. | Not applicable. |
| **Memory / CPUs** | `WithMemory(MiB)` (default 512), `WithCPUs(n)` (default 1) | Same. `WithMaxCPUs`/`WithMaxMemory` (hotplug) are rejected. No per-sandbox maximum is documented. |
| **Root disk size** | `WithRootDisk(RootDisk.Managed(MiB))`. The default is 4 GiB. Flat, tmpfs and disk-image roots are also available. | Only `RootDisk.Managed(MiB)`, which is sent as `disk_size_mib`. Flat, tmpfs and disk-image roots are rejected. |
| **Extra disk-backed volume for `/var/lib/docker`** | `Mount.Owned{Kind: VolumeKindDisk, SizeMiB}`: an ext4 disk owned by the sandbox and removed with it. Named disk volumes also work. | **Not available.** Owned mounts, disk-kind volumes and inline volume creation are all rejected. Only directory named volumes, `Mount.Tmpfs` and `Mount.Disk` (a file in the org's host volume) remain. |
| **Run the runner process** | `ExecStream` + `Recv`/`Wait`. Willet does this. `ExecDefault[Stream]` runs the image entrypoint. `ExecDetached` (jobs) is also available. | `ExecStream` works. Retained jobs, job history and reattach are local-only. |
| **Stream logs / wait for exit** | `ExecHandle.Recv` events (Stdout, Stderr, Exited, Done) and `ExecHandle.Wait`. `Sandbox.Logs` / `LogStream` for console logs. | `ExecHandle` streaming works. `LogStream` is follow-only. Bounded or historical log reads are local-only. |
| **Labels + list/filter** | `WithLabel(s)` at create. `ListSandboxesWith(WithListLabels(...))` matches all given labels and is paginated. | Same. The labels filter is sent to `GET /v1/sandboxes`. Labels cannot be modified after creation. |
| **Stop / destroy** | `Stop`, `Kill`, `Destroy` (graceful by default, `WithDestroyForce`), `RemoveSandbox`. `WithEphemeral(true)` removes all state once the sandbox reaches a terminal status. | `Stop`, `Destroy` (graceful only). `Kill`, `RequestKill`, `RequestDrain` and `WithDestroyForce` are unsupported. `WithEphemeral`, `WithMaxDuration` and `WithIdleTimeout` are sent to the cloud. **`WithReplace` is rejected.** |
| **Do sandboxes outlive the Go process?** | Not by default. The runtime watches a pipe from the creator and stops attached sandboxes when the creator exits, even on a crash. Use `WithDetached()` to opt out. | **Always.** `Close` never stops a remote sandbox. Remote sandboxes run until stopped, or until `max_duration` / idle timeout. |
| **CGO / runtime** | CGO and a C toolchain are required. The full module embeds a ~34–46 MB FFI library per platform, extracts it to `$MSB_HOME/lib/v0.8.2/` and needs glibc ≥ 2.28. Local VMs also need the `msb` + `libkrunfw` pair in `MSB_HOME` (`EnsureRuntime` installs it). | CGO is still required. "Remote execution needs no host runtime", so no `msb`/`libkrunfw` and no KVM. |
| **dockerd inside a sandbox** | Supported and documented. No privilege flags are needed (guest root is real root inside the VM). Requirements: Docker data off the overlay root (flat root or an owned ext4 disk), `iptables` in the image, and not using the `restricted` security profile. | No documented path. The storage options that make Docker work locally are all rejected on remote. `Mount.Tmpfs` is the only candidate **(inferred)**. The `oom_score_adj` failure in the map's Notes is not explained anywhere in the source or docs. |

## Details

### 1. Active profile and Backend selection

- **Package-level calls follow the configured destination.** In the full module, `CreateSandbox` calls `configuredContext()` on every call (`GO/core/sandbox.go:111,134`). That goes through `ffi.ConfiguredContext()` (`GO/core/internal/ffi/context.go:159-182`) to the native `{"selection":"configured"}` handler, which calls `microsandbox::context::resolve_default_backend()` (`GO/native/src/context.rs:72-77`; `RS/context/profile.rs:123-125,170-208`).
- **Precedence** (`RS/config/backend.rs:76-102,145-275`; `DOCS/cloud/connect.mdx:291-306`):
  1. managed `active_profile`
  2. `MSB_BACKEND=local|remote`
  3. `MSB_PROFILE`
  4. the saved `active_profile` in `~/.microsandbox/config.json`, or `$MSB_CONFIG_PATH` (`RS/config/mod.rs:822-827`)
  5. local

  A bare `MSB_API_KEY` supplies credentials but never selects remote (`RS/config/backend.rs:271-273`). `msb context use` writes the saved preference (`RS/config/backend.rs:288-291`), so the SDK follows it.
- **The docs contradict each other.** `DOCS/configuration.mdx:521` says "SDK code loads profiles explicitly", while `DOCS/cloud/connect.mdx:143` says "New SDK calls honor the active profile". The code agrees with the second.
- **Remote profile credentials.** `api_key_ref` is `env:VAR`, `inline:KEY` or `keyring:svc:name`. The `keyring:` form returns "api_key_ref 'keyring:' resolution is not yet wired — use 'env:' or 'inline:' for now" (`RS/context/profile.rs:228-290`, at line 283). The official docs list only `env:` and `inline:` (`DOCS/configuration.mdx:529-533`). The CLI shares this code path, so this is not an SDK-only gap **(inferred)**.
- **The SDK cannot tell you the Backend before you act.** The v0.8.2 public Go surface has no equivalent of `msb context`. `GO/aliases.go` exports no configured-context getter, and `configuredContext()` is unexported (`GO/core/context.go:186`). `ffi.DefaultBackendInfo` exists only internally (`GO/core/internal/ffi/ffi.go:4848-4851`). v0.7.12 exported it as `microsandbox.DefaultBackendInfo()`, returning `Kind`, `Source` and `Profile` (`sdk/go/backend.go:30-47` at `sdk/go/v0.7.12`).

  What the Pool can do instead:

  - **(a)** Create the first Runner through the package-level call, read `sb.ContextKind()` (`GO/core/sandbox.go:97-102`), and keep `sb.Context()` (`GO/core/context.go:173`) to make later calls through `ctx.Sandboxes().Create(...)`. This pins the Backend for the Pool's lifetime.
  - **(b)** Call `microsandbox.Image.List(ctx)` as a probe. It errors on remote.
  - **(c)** Shell out to `msb context --format json`.
  - **(d)** Re-implement the precedence rules.

  All four are **(inferred)**. Note that without (a), a `msb context use` run while the Pool is up would send the next Slot refill to the other Backend, because selection is re-read on each call **(inferred, from `configuredContext()` being called per operation)**.
- **Explicit selectors exist.** `SandboxLocal()`, `SandboxRemote()` (reads `MSB_API_KEY`/`MSB_API_URL`), `RemoteContextFromProfile(name)` and `NewLocalContext` (`GO/core/context_selectors.go`; `GO/core/context.go:35-69`). The Pool doesn't need them if it follows the configured profile.

### 2. Registry images, pull policy, private registries

- **Pull policy.** `PullPolicyDefault` ("" = if-missing), `PullPolicyAlways`, `PullPolicyIfMissing` and `PullPolicyNever` (`GO/core/enums.go:95-104`), set with `WithPullPolicy` (`GO/core/options.go:905-908`). On remote the policy is part of the create body (`pull_policy` in `packages/microsandbox-types/rust/lib/cloud/mod.rs:80-95`).
- **Auth.** `WithRegistryAuth{Username, Password}` and `WithRegistryAnonymous()` (`GO/core/options.go:923-939`). Locally, without explicit auth, credentials resolve from the msb keyring (`msb registry login`), then `config.json` `registries.hosts.<host>.auth`, then the Docker credential store/config, then anonymous (`RS/config/registry.rs:180-212`; `DOCS/configuration.mdx:464-472`).

  On remote the SDK maps explicit auth to `CloudRegistrySelection::Inline` and anonymous to `Anonymous`. When neither is given, "the cloud picks the stored credential configured for the image's registry host" (`RS/context/cloud/sandbox.rs:722-738`). `WithRegistryInsecure` and CA certs are rejected on remote (`RS/context/cloud/sandbox.rs:683-694`; `DOCS/images/overview.mdx:118`).
- **Cached images are bypassed when credentials resolve (relevant to #9).** The early cache check returns "not cached" whenever the resolved auth is not anonymous (`crates/image/lib/registry/client.rs:1872-1883`). After that, `PullPolicyNever` fails with `NotCached` (`client.rs:651-655`). Otherwise msb goes to the registry. Archive-loaded entries are recorded as `LocalArchive` provenance, which only an *unauthenticated* lookup may serve (`crates/image/lib/cache/store.rs:107-121,221-223`).

  **(inferred)** So if the host has Docker Hub credentials (for example from `docker login` in `~/.docker/config.json`), an image loaded as `msb-actions-docker:local` (which normalizes to `docker.io/library/…`) will still be looked up on Docker Hub and fail with "Not authorized". `Never` fails too. That is the original local failure from the map's Notes, and it would happen even after a correct `msb load`. Two likely fixes are `WithRegistryAnonymous()` on Runner creation, or tagging the loaded image under a registry host the user has no credentials for. A prototype should confirm this.
- **No `Image.Pull` in Go.** The `Image` namespace has Get, List, Inspect, Remove, Prune, Load and Save only (`GO/core/image.go:10-96`). Willet pre-pulls by booting a temporary sandbox (`W/internal/microvm/image.go:36-48`, and `withTempVM` in `W/internal/microvm/microvm.go:281-310`). `CreateSandboxWithProgress` reports pull progress (`GO/aliases.go`; `CreationProgressPull`).
- **Mutable tags.** "Once microsandbox resolves an image reference, it pins the exact layers" (`DOCS/images/overview.mdx:60`). With if-missing, a cached tag is never re-checked. Willet therefore re-pulls on a schedule and pins by digest (`W/internal/microvm/image.go:14-22`).

### 3. Local tarball loading (`msb load` equivalent)

- `microsandbox.Image.Load(ctx, inputPath, tags...)` accepts a `docker save` archive or an OCI Image Layout archive. The tags apply to the first image (`GO/core/image.go:71-86`).
- **Local only.** The native side takes `configured_context()` and returns `"image ops require a local backend"` when it is remote. The remote-only native build returns `local_only(ImageLoad)` (`GO/native/src/lib.rs:6488-6527`). The docs say "Image-cache commands are local-only. On managed cloud, specify an OCI image on create" (`DOCS/images/overview.mdx:215`; `DOCS/sdk/go/images.mdx:8`; `DOCS/cloud/limitations.mdx` "Image management").
- **Gaps against the CLI:**
  - `msb load` reads stdin (`docker save x | msb load`); the Go API needs a file on disk (`DOCS/cli/image-commands.mdx:46-64`).
  - A loaded archive is regenerated, so its digests can differ from the registry's (`DOCS/images/overview.mdx:234`).

### 4. Sizing

- **Memory and CPUs.** `WithMemory(MiB)` (default 512) and `WithCPUs(n)` (default 1) (`GO/core/options.go:716-724`). On remote these become `vcpus`/`memory_mib` (`packages/microsandbox-types/rust/lib/cloud/mod.rs:190-213`). Remote rejects `max_cpus`/`max_memory` when they differ from the base values, and rejects `nested_virt` and `hostname` (`RS/context/cloud/sandbox.rs:897-916`).

  The OpenAPI schema has no upper bound beyond `minimum: 0` (`DOCS/api-reference/openapi.json`, `CloudSandboxResources`). No per-sandbox maximum is documented. Pricing is per vCPU-hour, per GiB-hour and per root-disk GiB-hour (`DOCS/cloud/pricing.mdx:26-28`).
- **Root disk.** `WithRootDisk(RootDisk.Managed(MiB))` (`GO/core/options.go:635,676`). The local default is 4 GiB (`RS/sandbox/config.rs:45`). On remote only the managed root disk is allowed: "non-managed root_disk" is rejected (`RS/context/cloud/sandbox.rs:862-877`), and the size is sent as `disk_size_mib` (`cloud/mod.rs:203-213`). Flat root is local-only (`DOCS/how-to/docker/docker-in-sandbox.mdx:17`).
- **Extra volume.**
  - **Local.** `Mount.Owned(OwnedVolumeOptions{Kind: VolumeKindDisk, SizeMiB})` is "allocated once and removed with the sandbox" (`GO/core/options.go:1897-1909,1995-2009,2065-2073`). Named volumes support `WithVolumeKind(VolumeKindDisk)` and `WithVolumeSize` (`GO/core/options.go:2079-2091`).
  - **Remote.** The SDK rejects `"sandbox-owned volumes"` and `"named volume inline create"` (`RS/context/cloud/sandbox.rs:982-998`), and rejects disk-kind volumes and capacity (`RS/context/cloud/volume.rs:108-126`). The docs say "Disk-backed named volumes … are local-only" (`DOCS/cloud/limitations.mdx`). What remains is directory named volumes, created before mounting, with a whole-GiB quota (`DOCS/sdk/go/volumes.mdx:56,565`), plus `Tmpfs` and `DiskImage` mounts (`packages/microsandbox-types/rust/lib/cloud/specs.rs:442-500`).

### 5. Running the runner process

- **Nothing runs automatically.** `WithEntrypoint`/`WithCmd` override the image's default workload, and only `ExecDefault`/`ExecDefaultStream`/`AttachDefault` run it. Literal `Exec`/`Shell` calls ignore it. `WithInit` replaces PID 1 (`GO/core/options.go:858-879`; `GO/core/exec.go:64-66,298-299`). Remote carries `entrypoint`/`cmd` in `CloudSandboxRuntimeOptions` (`packages/microsandbox-types/rust/lib/cloud/specs.rs:728-749`).
- **Streaming exec.** `sb.ExecStream(ctx, cmd, args, WithExecUser, WithExecCwd, WithExecEnv)` returns an `*ExecHandle` with `Recv` (event kinds Stdout, Stderr, Exited, Failed, Done), `Wait`, `Kill`, `Signal` and `TakeStdin` (`GO/core/exec.go`; `GO/README.md` "Streaming Execution").

  Willet's Runner launch is exactly this. It passes `ACTIONS_RUNNER_INPUT_JITCONFIG` via exec env "so it is never persisted in the microsandbox catalog", and pumps events to a logger (`W/internal/microvm/microvm.go:213-257,370-400`). The official guide pipes the JIT config to `run.sh --jitconfig` through stdin with `msb exec --stream` (`DOCS/how-to/ci-cd/github-actions-runner.mdx`).
- **Output flow control** is built in: 4 MiB combined output allowance per exec, with acks (`COMPATIBILITY.md` "Execution output backpressure").
- **Detached jobs** are local only (`ExecDetached`, `Job.Wait`/`Logs`/`Attach`). "Retained jobs, job history, and reconnectable job attachments are local-only" (`DOCS/cloud/limitations.mdx`).
- **Console logs.** `Sandbox.Logs` (bounded) is local-only. `LogStream` is follow-only on remote (`DOCS/sdk/go/sandbox.mdx:487,528`; `RS/context/cloud/sandbox.rs:594-607`).
- **Idle timeout.** "An attached exec session … keeps the sandbox active" (`DOCS/sandboxes/lifecycle.mdx:661`), so `WithIdleTimeout` won't kill a Runner waiting on a job while the Pool holds the stream.
- **Unknown:** what happens on remote to an exec'd `run.sh` when the client's stream drops (Pool crash or network loss). Neither the docs nor the code say.

### 6. Labels and cleanup of leftovers

- Labels are set with `WithLabel`/`WithLabels`. The prefixes `sandbox.`, `microsandbox.` and `service.` are reserved (`GO/core/options.go:791-813`; `DOCS/sandboxes/labels.mdx:9-11`). On remote they are `CloudSandboxSpec.labels` (`cloud/mod.rs:164`). Remote cannot modify labels after creation (`DOCS/sandboxes/labels.mdx:72`).
- **Listing.** `ListSandboxesWith(WithListLabels(map), WithListLimit, WithListCursor)` returns "every returned sandbox to carry all labels", with paging via `NextCursor` (`GO/core/sandbox.go:816-850`).
  - Local filters in the DB (`RS/context/local/sandbox/mod.rs:570-590`).
  - Remote sends `labels` as a JSON query parameter on `GET /v1/sandboxes` (`RS/context/cloud/http.rs:167-185`).
  - Label selection "works on our managed cloud except with ping and touch" (`DOCS/sandboxes/labels.mdx:136`).
- **Willet's pattern.** It labels every VM `willet.owner=<id>`, holds an owner lock, pages through `ListSandboxesWith(WithListLabels(owner))`, and calls `Destroy(WithDestroyForce())` on each (`W/internal/microvm/microvm.go:19-24,149-175`). The forced destroy would fail on remote (see §7).

### 7. Stop, destroy, lifetime

- **Local attached sandboxes die with the Pool.**
  - The runtime's parent watchdog reads a pipe from the creator. EOF means "creator process exited; stopping attached sandbox" (`crates/runtime/lib/runner/vm.rs:224-229,3745-3790`).
  - "By default, sandboxes created through a local SDK stop when your application exits" (`DOCS/sandboxes/lifecycle.mdx:12`).
  - `WithDetached()` "continues running after the Go process exits" (`GO/core/options.go:845-849`).
  - `Close()` on the final attached owner triggers shutdown; "Detached and remote runs keep running" (`GO/core/sandbox.go:1039-1044`).
- **Remote sandboxes always survive the client.** "Remote runs survive client process exit" (`DOCS/sandboxes/lifecycle.mdx:277`). `Close` "does not stop the sandbox; … call stop() or remove() explicitly" (`DOCS/sdk/go/sandbox.mdx:1121`). Billing continues while they run (`DOCS/cloud/pricing.mdx:102`).
- **Ephemeral.** `WithEphemeral(true)` makes the runtime remove the DB row, disk state and logs once the sandbox reaches a terminal status (`GO/core/options.go:851-855`; `packages/microsandbox-types/rust/lib/domain.rs:895-912`). This is also sent to remote via `lifecycle.ephemeral` (asserted in `RS/context/cloud/sandbox.rs:2272`).

  Known bug, still **open**: cancelling the `CreateSandbox` context leaves an ephemeral sandbox `stopped` instead of removed ([superradcompany/microsandbox#1687](https://github.com/superradcompany/microsandbox/issues/1687)). Willet works around it by never cancelling creation and tracking a "pending" teardown (`W/internal/microvm/create.go:14-75`).
- **Destroy.** `Destroy(ctx, WithDestroyTimeout, WithDestroyForce)` is graceful with a 10 s default and "refuses to remove a same-name replacement" (`GO/core/sandbox.go:1090-1095,806-815`). `Stop` has no built-in timeout (`GO/core/sandbox.go:896-901`).

  On remote, `Kill`, `RequestKill` and `RequestDrain` are "Not available on managed cloud; use a graceful stop" (`DOCS/sdk/go/sandbox.mdx:1004,1031,1041`; `RS/context/cloud/sandbox.rs:490-502`). Forced destroy goes through kill (`RS/sandbox/handle.rs` around line 1168), so it fails there too **(inferred)**. A stop timeout on remote "only limits how long the SDK waits" (`DOCS/sdk/go/sandbox.mdx:957`).
- **Name reuse.** `WithReplace` is rejected on remote ("Replace-on-create is not available on managed cloud; remove the existing sandbox first": `RS/context/cloud/sandbox.rs:677-682`; `DOCS/sdk/go/sandbox.mdx:1591`). Remote names are unique per org (`cloud/mod.rs:252`). After a Pool crash on remote, fixed Slot names such as `runner-3` would collide, so the Pool needs unique Runner names or remove-then-create **(inferred)**.
- **Safety nets.** `WithMaxDuration` and `WithIdleTimeout` work on both Backends (`SandboxPolicy` is sent as `lifecycle`; `domain.rs:907-911`; `DOCS/sandboxes/lifecycle.mdx:608-665`).

### 8. CGO, runtime, distribution

- **The full module (`github.com/superradcompany/microsandbox/sdk/go`) uses CGO.**
  - It `dlopen`s an embedded native library (`GO/core/internal/ffi/ffi.go:6-60`, `#cgo linux LDFLAGS: -ldl`).
  - It needs Go 1.22+, "CGO enabled and a C compiler/toolchain" (`GO/README.md` "Requirements").
  - Embedded payloads are linux-amd64 45.6 MB, linux-arm64 37.6 MB, darwin-arm64 33.7 MB, windows-amd64/arm64 ~34–40 MB (`GO/internal/bundle/bundles/`). There is no darwin-amd64.
  - On first use the library is extracted to `$MSB_HOME/lib/v0.8.2/` (default `~/.microsandbox`) and dlopened (`GO/setup.go:17-58,91-108`). It needs glibc ≥ 2.28 (`GO/setup.go:111-125`).
- **Lean modules (new in v0.8).**
  - `sdk/go/core` plus `sdk/go/local` or `sdk/go/remote` carry no payload. They "never download on import or use" (`GO/local/provider.go:1`, `GO/remote/provider.go:1`).
  - The FFI artifact must be provisioned with `go run …/core/cmd/ffi-setup --kind remote --source URL --sha256 …` (`GO/README.md` "Lean distributions"; `DOCS/migrations/v0.8.mdx:179-255`).
  - "Remote execution needs no host runtime" (`DOCS/migrations/v0.8.mdx:193`).
- **Runtime pair.**
  - `EnsureRuntime(ctx, RuntimeConfig{}, InstallOptions{})` resolves `msb` + `libkrunfw`, or downloads them if wholly absent. It "reuses any complete … pair in `MSB_HOME` … without … requiring its version to equal the SDK version" (`GO/README.md`; `GO/core/runtime_setup.go:76-80`).
  - Resolution order: explicit paths (`MSB_PATH`/`MSB_LIBKRUNFW_PATH`), then configured paths, then `home` → `MSB_HOME` → `~/.microsandbox` (`DOCS/sdk/setup.mdx:124-140`).
  - It is only needed for local sandboxes (`GO/README.md` "Installation").
- **Version coupling between SDK and runtime.**
  - **v0.7.12** (willet) accepts only a runtime whose version equals the SDK's. Otherwise it fails with "no tested sandbox launch contract" (`sdk/rust/lib/runtime/launch_contract.rs` at `v0.7.12`, `resolve` around lines 705-718). Willet upgrades the runtime in place to match (`W/internal/microvm/runtime.go`; `W/README.md:17,186`).
  - **v0.8.2** accepts 0.6.x, or any runtime that advertises launch protocol 2 (`RS/runtime/launch_contract.rs:469-505,731-768`).
  - Released v0.7 SDKs and CLIs refuse a v0.8 runtime (`DOCS/migrations/v0.8.mdx:265`).
  - "Older tools may refuse the catalog … written by a newer release" (`DOCS/migrations/v0.8.mdx:263`).
- **Shared state with the user's `msb`.** On the local Backend the Pool's SDK and the user's `msb` CLI share one `MSB_HOME` (DB, image cache, runtime). An SDK newer than the user's CLI can leave the CLI unable to read that state **(inferred from the two migration-doc lines above)**. For the same reason, images loaded with `msb load` are visible to the SDK and the reverse.

### 9. Running `dockerd` in a Runner

- **Documented recipe (local).** `docker:dind` with `--root-disk flat:10G`, or with `--mount-owned /var/lib/docker:kind=disk,size=10G`, then start `dockerd` from a script (`DOCS/how-to/docker/docker-in-sandbox.mdx`).

  The reason given: "Docker's default storage driver uses overlay layers; a normal managed root would place those layers on top of the sandbox's own OverlayFS." Flat root is badged local-only (line 17).
- **Willet's recipe (verified by willet on v0.7.7 with Docker 29).** It uses an owned ext4 disk at `/var/lib/docker` and starts `dockerd` as root via `sb.Exec` before launching the runner. The image must contain `dockerd` and `iptables` (`W/internal/microvm/docker.go:14-28,78-135`).
- **Privileges.** No special flag exists or is needed: guest root is root inside the VM (`DOCS/security/isolation.mdx:119`). The `restricted` security profile (no_new_privs, drops CAP_SYS_ADMIN) "is incompatible with … Docker-in-Docker" (`DOCS/security/isolation.mdx:64`; `crates/agentd/lib/session.rs:1761-1803`). It is opt-in, and the default is `default` (`GO/core/options.go:482-490`).

  agentd places exec'd processes in the cgroup `/sys/fs/cgroup/microsandbox-workload` (`crates/agentd/lib/workload.rs:13`; `session.rs:1346`). Docker still works locally despite this, according to the guide and willet.
- **Remote.** Every storage option that makes Docker work locally is rejected (§4). The cloud docs never mention Docker. `Mount.Tmpfs` at `/var/lib/docker` is the only candidate left; it isn't overlay, but it uses guest RAM **(inferred, untested)**. The map's Notes record a remote buildkit failure (`crun: write /proc/self/oom_score_adj: Permission denied`). Nothing in agentd or the docs explains it: agentd only drops capabilities under the `restricted` profile. It probably comes from the cloud's host or guest setup, which is not in this repository.

### 10. Gaps between the Go SDK and the `msb` CLI

| CLI | Go SDK v0.8.2 |
|---|---|
| `msb context` (show the selected profile) | No public equivalent (it was `DefaultBackendInfo()` in v0.7.12) |
| `msb pull` | No `Image.Pull`. Boot a throwaway sandbox with `PullPolicyAlways` |
| `docker save … \| msb load` (stdin) | `Image.Load(path)` only |
| `--remote` / `--local` per command | `SandboxRemote()` / `SandboxLocal()` / contexts |
| `msb context use` | Not available (not needed) |
| `msb registry login` (keyring) | No Go call. The SDK reads those keyring entries when resolving registry auth locally |

### 11. willet (v0.7.12) as a worked example

Willet is **local-only by construction**. It calls `EnsureRuntime`/`InstallRuntime` and runs `msb --version` (`W/internal/microvm/runtime.go`), uses the `Image` cache API (`W/internal/microvm/image.go`), and sets `WithHostname` and `Mount.Owned` disk (`W/internal/microvm/microvm.go:177-205`, `docker.go:78-86`). The remote Backend rejects all three of those options.

Patterns worth lifting (MIT):

- Create in the background and never cancel it (#1687) (`create.go`).
- Owner-label cleanup under an owner lock (`microvm.go:149-175`).
- Graceful-then-forced destroy. On remote, drop the forced step.
- JIT config passed via exec env, not sandbox env (`microvm.go:231-243`).
- Digest-pinned image refresh (`image.go`).
- `dockerd` start with a readiness probe and log-tail hints (`docker.go:90-170`).

## Open questions this research could not settle

- What happens on remote to an exec'd process whose client stream disconnects?
- Remote per-sandbox CPU, memory and disk ceilings, and concurrency or rate limits, are not documented.
- Whether `dockerd` can run on remote at all (tmpfs `/var/lib/docker`, cgroups, the `oom_score_adj` error). This needs a prototype.
- Confirm the authenticated-pull cache bypass (§2) with `msb load` + Docker Hub credentials present. It probably also explains the local failure in the map's Notes.
