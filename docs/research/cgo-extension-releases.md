# Building and releasing a CGO `gh` extension per platform

Research for [#6](https://github.com/arteven/gh-runners-msb/issues/6), feeding decision [#12](https://github.com/arteven/gh-runners-msb/issues/12). Researched 2026-11-25.

Sources were read at these revisions. Every link below is pinned to them.

| Source | Revision |
| --- | --- |
| `cli/cli` | trunk `b546819fd547ef8c97da470bb00f2aaad4165c84` |
| `cli/gh-extension-precompile` | main `5c97d245f74e0f4447700ce0f894f1f518ffbd2a` (README pins `@v2`) |
| `superradcompany/microsandbox` | main `c75e80c1f2990d80a6ec00906fb65cd58afad92d`; Go module `sdk/go/v0.8.2` = `9978e2274a65bed837cbcd2ebb3c51bceb81576c` |
| `gerritlansing/willet` | main `e314f117c8a55fcedad3c644cdca620fee29cafa` |
| `actions/runner-images` README, docs.github.com | fetched 2026-11-25 |

Claims marked **(inferred)** are my reasoning, not something a source states. Claims marked **(tested)** come from a probe build I ran on Debian trixie (glibc 2.41) with Go 1.26.11 and zig 0.17.1, against `sdk/go` v0.8.2. The probe calls `SDKVersion()` and `RuntimeVersion()`, which forces the embedded FFI library to be extracted and `dlopen`ed.

## Short answers

**1. Can `cli/gh-extension-precompile` build CGO binaries? What asset names does gh expect?**
Not with its built-in loop. That loop builds 12 GOOS/GOARCH pairs on one runner, with `CGO_ENABLED` defaulting to `0` and `CC` empty for every non-Android target. If you set `CGO_ENABLED=1`, the darwin, windows and freebsd targets fail on a Linux runner because there is no cross C compiler, and so does linux-arm64. With `set -e`, the whole release then fails **(inferred from the script)**. The only CGO escape hatch is `build_script_override`. The action then runs your script and uploads whatever it puts in `dist/`. gh matches an asset only by suffix. The asset name must end in `<GOOS>-<GOARCH>`, plus `.exe` on Windows, so `gh-runners-msb-linux-amd64`, `gh-runners-msb-linux-arm64` and `gh-runners-msb-darwin-arm64` all work. The `x64` spelling does **not** match. It must be `amd64`.

**2. Cross-compile, or one native runner per platform? Which hosted runners exist?**
The SDK's cgo code is tiny: it only `dlopen`s the Rust library at runtime and never links it. So cross-compiling needs only a C compiler and libc for the target.
- **Linux, both arches: cross-compiling works.** I built the probe from one x86_64 host with `aarch64-linux-gnu-gcc`, and with `zig cc` for both linux targets **(tested)**.
- **darwin-arm64 does not cross-compile with zig.** The link step needs `CoreFoundation`, `Security` and `libresolv` from the macOS SDK, which zig does not ship **(tested)**. goreleaser-cross gets around this with osxcross. A `macos-15` or `macos-latest` runner is simpler.

Hosted runners exist for all three targets, in public and private repos alike: `ubuntu-24.04` / `ubuntu-latest` (x64), `ubuntu-24.04-arm` (arm64), and `macos-15` / `macos-latest` (arm64, M1).

**3. What does the msb SDK's embedded FFI library need at build time and at runtime?**
- **Build time:** CGO plus a C compiler. No Rust toolchain and no downloads. The tagged Go module already contains the five prebuilt libraries, 32–44 MB each, in `internal/bundle/bundles/`, and `//go:embed` picks the one for the target. Building from the SDK's `main` branch gives 0-byte sentinel files, so you must depend on a tagged version.
- **Runtime:**
  - Linux needs glibc. The library needs glibc ≥ 2.28. Our binary also carries the glibc floor of the machine that built it: ≥ 2.34 when built on any current Ubuntu runner, or about 2.17 with `zig cc -target …-gnu.2.28`. musl/Alpine is not supported.
  - macOS needs version ≥ 14.0, because the dylib's `minos` is 14.0.
  - On first use the library is extracted to `$MSB_HOME/lib/v<sdk>/` (default `~/.microsandbox`) and `dlopen`ed.
  - `msb` and `libkrunfw` are separate. `EnsureRuntime` downloads them from msb's GitHub releases. The VMM runs in a child `msb machine` process, so our binary needs no hypervisor entitlement **(inferred)**.

**4. How does willet build and release?**
- **Build:** a matrix of two **native** runners, `ubuntu-24.04` and `ubuntu-24.04-arm`. Each runs `make test` and then `CGO_ENABLED=1 go build`, with no cross-compiling, no zig, no goreleaser and no macOS.
- **Release:** a fan-in job downloads the artifacts. It then writes an SBOM and `SHA256SUMS` and adds build-provenance attestations. Finally it runs `gh release create --draft`, followed by `gh release edit --draft=false`, because immutable releases block adding assets after publishing.
- **Requirements:** willet's README asks for glibc 2.34+, which fits a build on Ubuntu 24.04.

**5. What does `gh extension install` do on a platform with no matching asset?**
It exits with an error: `gh-runners-msb unsupported for <os>-<arch>`, plus a ready-made `gh issue create` command for asking us to add that platform. One exception: on darwin-arm64 it falls back to a `darwin-amd64` asset under Rosetta, but we won't ship one. If the latest release has **no** platform asset at all, gh treats the repo as a script extension. Since the repo root has no `gh-runners-msb` script, that ends in "extension is not installable". gh only looks at the *latest* release, meaning the newest one that is neither a draft nor a prerelease.

## Recommended build matrix

**Recommendation (A): one native runner per platform, fanning in to a single release job. This is willet's pattern plus macOS.**

| Asset | Runner | Toolchain | Resulting floor |
| --- | --- | --- | --- |
| `gh-runners-msb-linux-amd64` | `ubuntu-24.04` | host gcc, `CGO_ENABLED=1` | glibc ≥ 2.34, KVM |
| `gh-runners-msb-linux-arm64` | `ubuntu-24.04-arm` | host gcc, `CGO_ENABLED=1` | glibc ≥ 2.34, KVM |
| `gh-runners-msb-darwin-arm64` | `macos-15` | Xcode clang, `CGO_ENABLED=1` | macOS ≥ 14 (from FFI dylib), Apple Silicon |

Workflow shape:
1. **Trigger:** a `v*` tag.
2. **Build matrix (one job per row):** `go vet`/`go test`, then `go build -trimpath`, renaming the output to the asset name. Run a smoke check that executes the binary on its own runner and forces the FFI `dlopen`, for example a hidden `version` command printing `RuntimeVersion()`. Then `upload-artifact`.
3. **Release job:** `download-artifact`, then checksums and `actions/attest`, then `gh release create --draft --verify-tag`, then `gh release edit --draft=false`.

Publishing as a draft first means no "latest" release ever exists without its assets. Without that, `gh extension install` would see a release with no assets and fail during the upload window.

Pin the runner labels: `ubuntu-latest` has already moved to 26.04 and `macos-latest` is on 26. That's cheap: all three runners are free for public repos, and this repo is public.

**Why A:**
- Every artifact is built and smoke-tested on the architecture it targets.
- There are no extra toolchains.
- It matches willet, which we may lift code from.
- The 2.34 floor is the same one willet accepts: Ubuntu 22.04, Debian 12, RHEL 9 or newer.

**Alternatives:**
- **(B) zig for Linux, macOS runner for darwin.** One `ubuntu-24.04` job builds both Linux assets with `CC="zig cc -target x86_64-linux-gnu.2.28"` and `aarch64-linux-gnu.2.28` **(tested)**. A `macos-15` job builds darwin.
  - Gain: the binary's glibc floor drops to the SDK's own 2.28, which adds RHEL 8, Debian 11 and Ubuntu 20.04 hosts.
  - Cost: we must download and pin a zig toolchain, and the arm64 binary is never run in CI unless we add an arm64 smoke job.
  - When to choose it: only if #12 wants pre-2.34 hosts.
- **(C) A manylinux_2_28 container on the native Linux runners.** This is how msb builds its own FFI library (`release-linux.yml` pins `LINUX_GLIBC_BASELINE: "2.28"`). It gives the same 2.28 floor as B but needs Go installed in the container. It is more moving parts than B for the same result.
- **(D) `cli/gh-extension-precompile` with `build_script_override`.** Possible, but it buys little. Calling it from each matrix job races on `gh release create`, and its default release is not a draft. The one sensible use is in the fan-in job, with an override script that just copies the downloaded artifacts into `dist/` to get its release and attestation steps. A plain `gh release` call is just as short.
- **(E) goreleaser-cross (osxcross) in one container for all three.** It is the only single-runner option that covers darwin. It is a heavy image, its glibc floor comes from its Debian base **(inferred)**, and it relies on a repackaged macOS SDK. Not worth it for three targets.
- **Not viable:** `cli/gh-extension-precompile` defaults, a CGO-free build, and zig for darwin.

**Optional size lever (not a build-matrix change):** the full SDK embeds the FFI library, so each binary is about 42–50 MB **(tested)**. The SDK also publishes "lean" modules (`sdk/go/core`, `sdk/go/local`, `sdk/go/remote`). These embed nothing and expect the library to be installed separately with a trusted SHA-256 (`ffi-setup`). Lean builds still need CGO. This trades download size for a second install step, so it is probably not worth it for v1 **(inferred)**.

## Details

### 1. `cli/gh-extension-precompile`

- **Inputs:** `action.yml` takes `build_script_override`, `go_version(_file)`, `go_build_options`, `generate_attestations`, `draft_release`, `release_tag`, `gpg_fingerprint` and Android options. It then runs `build_and_release.sh`. [action.yml](https://github.com/cli/gh-extension-precompile/blob/5c97d245f74e0f4447700ce0f894f1f518ffbd2a/action.yml)
- **Default platforms:** a hard-coded list of 12: darwin-amd64/arm64, freebsd-386/amd64/arm64, linux-386/amd64/arm/arm64, windows-386/amd64/arm64. [build_and_release.sh L4-17](https://github.com/cli/gh-extension-precompile/blob/5c97d245f74e0f4447700ce0f894f1f518ffbd2a/build_and_release.sh#L4-L17)
- **Build loop:** it sets `cgo_enabled="${CGO_ENABLED:-0}"`. It sets `CC` only for Android. Each target is built with `go build -trimpath -ldflags="-s -w" -o dist/${p}${ext}`, so the default asset names are bare `linux-amd64` and so on. [L53-76](https://github.com/cli/gh-extension-precompile/blob/5c97d245f74e0f4447700ce0f894f1f518ffbd2a/build_and_release.sh#L53-L76)
- **Failure with CGO:** the script runs under `set -e` [L2](https://github.com/cli/gh-extension-precompile/blob/5c97d245f74e0f4447700ce0f894f1f518ffbd2a/build_and_release.sh#L2). So `CGO_ENABLED=1` on an ubuntu runner fails on the first target with no C cross compiler, which is darwin-amd64. I confirmed separately that a darwin cgo build on Linux fails with `cgo: C compiler "clang" not found` **(tested)**.
- **CGO switch:** the README says the action "builds Go binaries with cgo disabled with the exception of Android", and that `CGO_ENABLED: 1` overrides that "for all build targets". [README](https://github.com/cli/gh-extension-precompile/blob/5c97d245f74e0f4447700ce0f894f1f518ffbd2a/README.md)
- **Override contract:** the script gets the tag as `$1` and must write executables to `dist/` with names "ending with `{os}-{arch}{ext}`". The action invokes it at [L47-49](https://github.com/cli/gh-extension-precompile/blob/5c97d245f74e0f4447700ce0f894f1f518ffbd2a/build_and_release.sh#L47-L49) and documents the contract in the README.
- **Release step:** if the release exists, it runs `gh release upload --clobber`. Otherwise it runs `gh release create` with `--prerelease` for tags containing `-`, and `--draft` only if `draft_release` is set. [L37-45, L97-103](https://github.com/cli/gh-extension-precompile/blob/5c97d245f74e0f4447700ce0f894f1f518ffbd2a/build_and_release.sh#L97-L103)
- **Not built for parallel use (inferred):** the action has no fan-in step. Running it in several matrix jobs means concurrent `gh release view`/`create` calls.
- **Generated workflow:** `gh extension create --precompiled=go` generates a workflow that runs this action on `ubuntu-latest` with attestations on. [goBinWorkflow.yml](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/ext_tmpls/goBinWorkflow.yml)

### 2. How `gh extension install` picks an asset

All references are to [`pkg/cmd/extension/manager.go`](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go).

- **Platform string:** `fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)`, plus `.exe` on Windows. This is gh's own platform, so the names are Go names (`amd64`, `arm64`). [L69-75](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L69-L75)
- **Binary or script extension:** `isBinExtension` fetches the **latest** release. It reports binary if any asset name ends with one of the `possibleDists()` entries (all `go tool dist list` pairs). [L749-771](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L749-L771), [L800-848](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L800-L848)
- **Script fallback:** if it is not a binary extension, gh checks for a file named after the repo at the repo root, via `hasScript` ([http.go L44](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/http.go#L44)). If there is none, it fails with `extension is not installable: no usable release artifact or script found`. [L255-281](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L255-L281)
- **Asset match:** `installBin` picks the first asset whose name has the suffix `platform+ext`. [L296-307](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L296-L307)
- **Rosetta fallback:** on darwin-arm64 with no arm64 asset, gh falls back to a `darwin-amd64` asset if Rosetta is installed, or errors if it is not. [L309-326](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L309-L326)
- **No match:** the error is `<repo> unsupported for <platform>.`, plus a `gh issue create -R owner/repo --title "Add support for the <platform> architecture" …` command. [L328-336](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L328-L336), [L385-393](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L385-L393)
- **Install location:** the asset is downloaded to `<dataDir>/extensions/gh-runners-msb/gh-runners-msb`. A true darwin-arm64 binary is then ad-hoc codesigned (`codesign --sign - --force --preserve-metadata=…`). We therefore don't need to sign the macOS binary for gh installs. [L349-363](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L349-L363), [L855](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L855)
- **Upgrades:** upgrade calls the same `installBin(repo, "")`, so the same matching rules apply. [L545, L576](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L545)
- **"Latest" release:** the REST API defines it as "the most recent non-prerelease, non-draft release". So prerelease tags (`v1.0.0-rc.1`) are invisible to a plain install, and a draft is invisible until it is published. [REST: Get the latest release](https://docs.github.com/en/rest/releases/releases#get-the-latest-release)
- **Docs:** the extension docs require binaries to "have a suffix of OS-ARCHITECTURE[EXTENSION]". [Creating GitHub CLI extensions](https://docs.github.com/en/github-cli/github-cli/creating-github-cli-extensions)

### 3. The microsandbox Go SDK's native library

Paths are in [`sdk/go`](https://github.com/superradcompany/microsandbox/tree/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go) unless noted.

**How it is loaded:**
- **dlopen, not link.** The cgo preamble only does `#cgo linux LDFLAGS: -ldl` and defines `dlopen`/`dlsym` trampolines. The package doc says: "The library is loaded at runtime via dlopen/dlsym rather than linked at build time. This means `go build` succeeds with no Rust toolchain on the host — the library bytes are embedded in the SDK … and extracted to disk automatically before the first dlopen." [internal/ffi/ffi.go L1-60](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/internal/ffi/ffi.go#L1-L60)
- **Embedding.** One `bundle_<os>_<arch>.go` per supported platform embeds `bundles/libmicrosandbox_go_ffi-<os>-<arch>.{so,dylib,dll}` via `//go:embed`. Other platforms compile, but `Bytes()` returns "no FFI library bundled for …". [internal/bundle/](https://github.com/superradcompany/microsandbox/tree/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/internal/bundle)

**What a release contains:**
- **Bundles are only real on tags.** On `main` the bundles are 0-byte sentinels. Using one gives "bundled FFI library is empty; this build is from a source tree without populated bundles (e.g. the main branch)". [bundle.go](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/internal/bundle/bundle.go)
- **Who fills them.** msb's release workflow job `go-sdk-tag` builds the libraries and commits them onto a tag-only commit, tagged `sdk/go/vX.Y.Z` (also `sdk/go/core/…`, `sdk/go/local/…`, `sdk/go/remote/…`). The workflow comment says: "proxy.golang.org fetches the tagged commit's source tree, so the per-platform FFI .so/.dylib must be committed". [.github/workflows/release.yml L1787-1866](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/release.yml#L1787-L1866)
- **The current module.** proxy.golang.org `@latest` is `v0.8.2`, from tag `refs/tags/sdk/go/v0.8.2`. Its zip is 91 MB, and the bundled libraries are darwin-arm64 33.7 MB, linux-amd64 45.6 MB, linux-arm64 37.6 MB, and windows amd64/arm64 40.1 and 34.0 MB. (Read from `https://proxy.golang.org/github.com/superradcompany/microsandbox/sdk/go/@latest` and the `@v/v0.8.2.zip` listing.)

**Where the libraries are built:**
- **Linux:** inside digest-pinned `quay.io/pypa/manylinux_2_28_{x86_64,aarch64}` containers, on `ubuntu-latest` and `ubuntu-24.04-arm`. [release.yml L332-370](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/release.yml#L332-L370)
- **glibc check:** the build is checked against `LINUX_GLIBC_BASELINE: "2.28"`. [release-linux.yml L74](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/release-linux.yml#L74)
- **macOS:** darwin is built on `macos-15`. [release.yml L372-390](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/release.yml#L372-L390)

**Requirements and platforms:**
- **README requirements:** "CGO enabled and a C compiler/toolchain available", plus "Linux with KVM, macOS with Apple Silicon, or Windows 11 with WHP enabled".
- **README platforms:** macOS ARM64, Linux x86_64, Linux ARM64, and Windows x86_64/ARM64 ("runtime support is in preview"). [README.md L28-43](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/README.md#L28-L43)

**What happens on first use:**
- **Extraction.** On the first SDK call, `materializeFFI` writes the embedded bytes to `<MSB_HOME or ~/.microsandbox>/lib/v<sdkVersion>/libmicrosandbox_go_ffi.<ext>`, atomically, and only rewrites it if the bytes differ. It then `dlopen`s that file. [setup.go L17, L76-110](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/setup.go#L76-L110) This costs about 45 MB in the user's home per SDK version, and each SDK upgrade leaves the old one behind **(inferred)**.
- **glibc error.** A load failure that mentions GLIBC gets the hint "this SDK release (v0.8.2) requires glibc >= 2.28". [setup.go L112-133](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/setup.go#L112-L133)
- **musl.** Native targets are `linux-{amd64,arm64}-gnu` only ("Linux requires a glibc target"). [provider_setup.go L43-55](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/provider_setup.go#L43-L55) Combined with the glibc-linked `.so`, I infer that musl hosts are unsupported **(inferred)**.

**What I measured in the v0.8.2 libraries:**
- **Linux `.so` files:** the highest symbol version referenced is `GLIBC_2.28`, and they need `libc`, `libdl`, `libpthread`, `librt`, `libm` and `libgcc_s` **(tested)**.
- **macOS dylib:** it links CoreFoundation, CoreServices, Hypervisor, Security, SystemConfiguration, `libSystem` and `libiconv`. Its `LC_BUILD_VERSION` is `minos 14.0.0, sdk 15.5.0`, so the macOS floor is 14 **(tested)**.

**msb runtime and entitlements:**
- **Runtime install.** "The `msb` runtime and `libkrunfw` are installed separately into `MSB_HOME` … by `EnsureRuntime`." [README.md L43-57](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/README.md#L43-L57) The download URL is `https://github.com/superradcompany/microsandbox/releases/download/v{version}/microsandbox-{os}-{arch}.tar.gz`. [crates/utils/lib/lib.rs L222-233](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/crates/utils/lib/lib.rs#L222-L233)
- **VMM process.** VMs run in a child process: "`spawn_sandbox` … fork+execs `msb machine` … The sandbox process runs the VMM". [sdk/rust/lib/runtime/spawn.rs L1-6](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/rust/lib/runtime/spawn.rs#L1-L6)
- **Entitlements.** The `com.apple.security.hypervisor` entitlement is applied only to the `msb` binary. [msb-entitlements.plist](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/msb-entitlements.plist), [release-macos.yml L145-147](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/.github/workflows/release-macos.yml#L145-L147) So our Go binary needs no entitlement, and gh's ad-hoc signature is enough **(inferred)**.
- **Lean modules.** `sdk/go/core`, `local` and `remote` embed no library. `ffi-setup` / `InstallNativeProvider` install one with a mandatory trusted SHA-256. [README.md "Lean distributions"](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/README.md), [scripts/stage-distributions.py](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/sdk/go/scripts/stage-distributions.py)

### 4. Probe build results **(tested)**

Probe program: `main.go` imports `sdk/go` v0.8.2 and prints `SDKVersion()` and `RuntimeVersion()`. Built with `go build -trimpath -ldflags="-s -w"`.

| Build | Result | Size | Max glibc symbol in our binary |
| --- | --- | --- | --- |
| linux/amd64, host gcc (glibc 2.41) | OK; runs, prints `ffi 0.8.2` | 49.8 MB | `GLIBC_2.34` (`dlopen`, `dlsym`, `pthread_create`, `__libc_start_main` …) |
| linux/arm64, `aarch64-linux-gnu-gcc` cross | OK | 41.5 MB | `GLIBC_2.34` |
| linux/amd64, `zig cc -target x86_64-linux-gnu.2.28` | OK; runs, prints `ffi 0.8.2` | 50.6 MB | ≤ 2.17, so the effective floor is the FFI's 2.28 |
| linux/arm64, `zig cc -target aarch64-linux-gnu.2.28` | OK | 42.3 MB | `GLIBC_2.17` |
| darwin/arm64, no CC on Linux | fails: `cgo: C compiler "clang" not found` | n/a | n/a |
| darwin/arm64, `zig cc -target aarch64-macos` | fails at link: `unable to find system framework "CoreFoundation"`, `"Security"`, `dynamic system library "resolv"` | n/a | n/a |

The `GLIBC_2.34` floor of a native build comes from glibc 2.34 moving `dlopen`/`pthread_*` into `libc.so.6` with new symbol versions. Any current Ubuntu runner (22.04, 24.04, 26.04) has glibc ≥ 2.35, so a native build there produces the same floor **(inferred; consistent with willet's README requiring glibc 2.34, below)**.

### 5. willet

- **Release workflow:** matrix `{amd64: ubuntu-24.04, arm64: ubuntu-24.04-arm}`. Each job runs `setup-go` from `go.mod`, `make test`, then `make build`, and packages `willet_<ver>_linux_<arch>.tar.gz`. A `release` job then runs `download-artifact`, `anchore/sbom-action`, `sha256sum > SHA256SUMS` and `actions/attest` (provenance plus SBOM). Finally it runs `gh release create --draft --verify-tag --generate-notes …` and then `gh release edit --draft=false`. Its comment explains: "with immutable releases, a published release can't gain or change assets". The same workflow runs as a dry run on PRs that touch it. [.github/workflows/release.yml](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/.github/workflows/release.yml)
- **Build command:** `CGO_ENABLED=1 go build -ldflags "-X main.version=… -X main.commit=…" -o bin/willet ./cmd/willet`. [Makefile](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/Makefile)
- **CI:** the same two-arch native matrix, plus an `integration` job on `ubuntu-24.04` that opens up `/dev/kvm` via udev and runs real microVMs. [.github/workflows/ci.yml](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/.github/workflows/ci.yml)
- **Requirements:** "Linux on amd64 or arm64 with KVM (`/dev/kvm`) and glibc 2.34 or later (Ubuntu 22.04, Debian 12, RHEL 9 or newer)." willet has no macOS build and no goreleaser config. [README.md L15](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/README.md#L15)
- **SDK version:** willet pins `github.com/superradcompany/microsandbox/sdk/go v0.7.12`. [go.mod](https://github.com/gerritlansing/willet/blob/e314f117c8a55fcedad3c644cdca620fee29cafa/go.mod)

### 6. GitHub-hosted runners

- **Public repos:**
  - Linux arm64: `ubuntu-24.04-arm`, `ubuntu-22.04-arm` and `ubuntu-26.04-arm` (4 CPU / 16 GB).
  - macOS arm64 (M1, 3 CPU / 7 GB): `macos-latest`, `macos-15`, `macos-26` and `xcode-27`.
  - macOS Intel: `macos-15-intel` and `macos-26-intel`.
- **Private repos:** the same arm64 Linux labels exist (2 CPU / 8 GB) and are billed per minute.
- Source: [GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners).
- **Label moves:** per [actions/runner-images README](https://github.com/actions/runner-images#available-images), `ubuntu-latest` is now Ubuntu 26.04, `macos-latest` is macOS 26 arm64, and macOS 14 images were retired on 2026-11-02. Pin explicit labels in the release workflow.
- **Visibility:** `arteven/gh-runners-msb` is public (`gh repo view --json visibility`), so standard runners cost nothing.

### 7. Release hygiene notes

- **Immutable releases:** assets and tag "cannot be changed after publication", and publishing "automatically generates a release attestation". [Immutable releases](https://docs.github.com/en/code-security/supply-chain-security/understanding-your-software-supply-chain/immutable-releases) This is why the fan-in job should create a draft, attach every asset, and only then publish. Publishing that way also closes the window in which gh would see a latest release with no assets **(inferred from §2)**.
- **Prerelease tags:** tags containing `-` (for example `v0.1.0-rc.1`) can be cut as GitHub prereleases. They are skipped by `gh extension install` unless the user pins them with `gh extension install arteven/gh-runners-msb --pin v0.1.0-rc.1` **(inferred from `fetchReleaseFromTag` when pinned, manager.go L287-292)**.
- **Pin the SDK to a tagged version, never a `main` pseudo-version.** A pseudo-version would embed 0-byte bundles and fail at runtime (§3).
