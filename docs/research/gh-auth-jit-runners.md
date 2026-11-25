# Can gh's own auth create JIT runner configs, and what happens to unused ones?

Research for [#5](https://github.com/arteven/gh-runners-msb/issues/5), which feeds decision [#7](https://github.com/arteven/gh-runners-msb/issues/7) (JIT config vs registration token). Researched 2026-11-25.

No runner was registered, created or deleted for this research. The only live calls were read-only `GET /repos/{owner}/{repo}/actions/runners` requests made with a `gh` OAuth token. Claims marked **(inferred)** are my reasoning and were not read in a source. Claims marked **(unverified)** need one live test on a throwaway repo before the spec relies on them.

Source pins used below:

- `actions/runner` at [`aea9aaa`](https://github.com/actions/runner/tree/aea9aaa3a0b49933412c6f59d7f05be037207a28) (main, 2026-11-24; latest release v2.338.2)
- `cli/cli` at [`b546819`](https://github.com/cli/cli/tree/b546819fd547ef8c97da470bb00f2aaad4165c84) (trunk, 2026-11-24; latest release v2.105.0)
- `cli/go-gh` at [`4e83b33`](https://github.com/cli/go-gh/tree/4e83b33c301de3d1d4d30df34db06648f898c389) (trunk; latest release v2.17.0)
- docs.github.com, Free/Pro/Team version, fetched 2026-11-25

## Short answers

- **Can gh's token call `generate-jitconfig`?** Yes, if the user is a **repository admin**. The endpoint needs admin access to the repo plus the `repo` scope for OAuth and classic tokens. `gh auth login` always requests `repo`, `read:org` and `gist`, so a default gh token already has every scope it needs. A fine-grained PAT (for example in `GH_TOKEN`) needs **Administration: write** on the repo.
- **Error for a non-admin:** the docs list `404` (and `409`, `422`) for `generate-jitconfig`. A live read-only `GET .../actions/runners` against a repo where the caller is not an admin returned `403` with `"You must have repository read permissions or have the repository runners fine-grained permission."`. Expect a 403 or a 404, and treat both as "not an admin of this repo" (inferred).
- **Request fields:** `name` (required), `runner_group_id` (required, integer), `labels` (required, 1 to 100 strings), `work_folder` (optional, default `_work`). The response is `{runner: {id, name, labels, ephemeral, ...}, encoded_jit_config}`, so the Pool learns the runner's numeric `id` straight away.
- **`runner_group_id` for a repo-level runner:** use `1`. There is no repo-level runner-group API to look it up. Runners go into the "default group" when no group is given, and microsandbox's own GitHub-runner guide passes `runner_group_id=1` for a repo. The group ID being `1` comes from that guide and the docs' example, not from a reference page that says so **(unverified)**.
- **An unused JIT runner, or one whose VM was killed:** GitHub deletes it automatically. "If JIT runners never run a job, they will automatically be removed." No delay is documented for JIT. For ephemeral runners in general the docs say they are removed after **more than 1 day** without connecting to GitHub (14 days for persistent runners). Until then it stays listed as an offline runner, counts toward the **10,000 runners per runner group** limit, and blocks its name **(inferred)**. You can force removal with `DELETE /repos/{owner}/{repo}/actions/runners/{runner_id}`.
- **List or delete by name prefix:** no. `GET .../actions/runners` takes a `name` filter (documented only as "the name of a self-hosted runner", so it is presumably an exact match) plus `per_page` (max 100) and `page`. Deletion is by numeric `runner_id` only. To clean up after a crash, page through the list, filter by name prefix (and `ephemeral`/labels) on the client, and then delete each one by ID.
- **Rate limits that matter:** 5,000 REST requests/hour per user, shared with everything else the user does with gh. Secondary limits: 900 points/min per endpoint (POST/DELETE cost 5 points, GET 1), 100 concurrent requests, and 80 content-creating requests/min or 500/hour. Runner registrations are capped at 1,500 per 5 minutes per repository. A Pool making one POST per job (plus one DELETE on cleanup) is far below all of these. The only one that could bite is the 500/hour content-creation limit, *if* `generate-jitconfig` counts as content creation, which the docs don't say **(unverified)**.
- **Registration token + `config.sh --ephemeral`:** it needs the same permission and scope. The token lasts one hour and goes *into the VM*. The runner then registers itself from inside the VM, picks the default group, adds the default labels `self-hosted`, OS and arch, and fails on a duplicate name unless `--replace` is given. Cleanup is the same as for JIT.
- **How go-gh resolves repo and host:** `repository.Current()` reads `GH_REPO`, otherwise the git remotes. It keeps only remotes whose host gh knows about, prefers `upstream` > `github` > `origin`, and honours `gh repo set-default`. `repository.Parse("[HOST/]OWNER/REPO")` handles `-R`; with two parts it fills the host from `auth.DefaultHost()`. go-gh does **not** parse `-R` for you. `api.DefaultRESTClient()` uses `auth.DefaultHost()`, not the repo's host, so pass `ClientOptions{Host: repo.Host}`. GHES maps to `https://HOST/api/v3/`, ghe.com tenancies to `https://api.TENANT.ghe.com/`.

## JIT vs registration token, side by side

| Point | JIT (`generate-jitconfig` → `run.sh --jitconfig`) | Registration token (`registration-token` → `config.sh --ephemeral` → `run.sh`) |
|---|---|---|
| Who calls the GitHub API | The host (Pool) makes one REST call. The VM only runs `run.sh`. | The host makes one REST call for the token. The VM then calls `/actions/runner-registration` plus the runner-admin APIs itself. |
| Permission and scope | Repo admin. `repo` scope (OAuth/classic) or Administration: write (fine-grained) | Same |
| Works with gh's default token | Yes (gh always requests `repo`) | Yes |
| Secret that enters the VM | `encoded_jit_config`: this runner's own credentials (RSA key file and so on), good only for this runner | The registration token, valid for 1 hour and not tied to one runner (it can register others until it expires) **(inferred)** |
| Runner group | Must pass `runner_group_id` (use `1` for repo level, unverified) | Chosen automatically: the runner picks the default group |
| Labels | Exactly the `labels` you pass, 1 to 100. Whether `self-hosted`/OS/arch are added is **unverified**, so pass `self-hosted` explicitly | Default `self-hosted`, OS, arch, plus `--labels` (`--no-default-labels` turns them off) |
| Name collision | `409 Conflict` (the docs list the code, the meaning is inferred) | Unattended: error "A runner exists with the same name" unless `--replace` |
| Runner ID known to the Pool | Immediately, in the response (`runner.id`) | Only by listing runners by name afterwards |
| One job, then deregistered | Yes ("JIT runners can only run a single job") | Yes ("the service un-configure[s] the runner after the job finishes") |
| Never runs a job, or VM killed | Removed automatically. No delay given for JIT. The ephemeral rule is more than 1 day without connecting | Removed after more than 1 day without connecting (ephemeral rule) |
| Forced cleanup | `DELETE .../actions/runners/{id}` | Same. `config.sh remove` needs the VM's local config, which a dead VM no longer has |
| Startup work inside the VM | Decode the config and write files, then listen | Generate an RSA key, look up groups, check the name, register, then listen |
| GHES | Endpoint documented in GHES 3.19 (the oldest docs version that still renders) | Supported on GHES |

## Details

### 1. Permission, scope and gh's token

- **The endpoint's requirements.** `POST /repos/{owner}/{repo}/actions/runners/generate-jitconfig`: "The authenticated user must have admin access to the repository. OAuth tokens and personal access tokens (classic) need the `repo` scope." Fine-grained tokens (GitHub App user, GitHub App installation, fine-grained PAT) need **"Administration" repository permissions (write)**. [docs: Create configuration for a just-in-time runner for a repository](https://docs.github.com/en/rest/actions/self-hosted-runners#create-configuration-for-a-just-in-time-runner-for-a-repository)
- **The registration token's requirements are the same:** admin access, `repo` scope, Administration: write. The token "expires after one hour". [docs: Create a registration token for a repository](https://docs.github.com/en/rest/actions/self-hosted-runners#create-a-registration-token-for-a-repository)
- **List, get and delete** need admin plus `repo`. Fine-grained tokens need Administration **read** to list and **write** to delete. [docs: List self-hosted runners for a repository](https://docs.github.com/en/rest/actions/self-hosted-runners#list-self-hosted-runners-for-a-repository), [Delete a self-hosted runner from a repository](https://docs.github.com/en/rest/actions/self-hosted-runners#delete-a-self-hosted-runner-from-a-repository)
- **gh's OAuth scopes.** The web/device flow always requests `repo`, `read:org` and `gist`, plus any `--scopes`. [cli/cli `internal/authflow/flow.go` L46-47](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/internal/authflow/flow.go#L46-L47). If you paste a token, gh demands at least `repo` and `read:org` (`write:org`/`admin:org` also satisfy `read:org`). [`pkg/cmd/auth/shared/oauth_scopes.go` L72-101](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/auth/shared/oauth_scopes.go#L72-L101), [`pkg/cmd/auth/shared/login_flow.go` L218](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/auth/shared/login_flow.go#L218)
- **Live check (read-only).** A `GET /repos/arteven/gh-runners-msb/actions/runners` made with a gh OAuth token returned `200` with `X-Oauth-Scopes: gist, project, read:org, repo` and `X-Oauth-Client-Id: 178c6fc778ccc68e1d6a`. That client ID is gh's own OAuth app ([`internal/authflow/flow.go` L24](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/internal/authflow/flow.go#L24)). So gh's default token reaches the admin-only runner endpoints on a repo the user owns. I did not call the POST, by design. That it also works for POST follows from the docs above **(inferred)**.
- **Non-admin.** The docs list `404 Resource not found` for `generate-jitconfig`. A live read-only `GET /repos/cli/go-gh/actions/runners` (not an admin there) returned `403 {"message":"You must have repository read permissions or have the repository runners fine-grained permission."}`. The POST's status for a non-admin was not observed. The Pool should map both 403 and 404 to "you must be an admin of OWNER/REPO" **(inferred)**.
- **Org OAuth-app restrictions don't block gh.** GitHub CLI is on GitHub's list of privileged OAuth apps, which users can authorize "even if an organization owner has restricted access by OAuth apps". [docs: Privileged OAuth apps](https://docs.github.com/en/apps/oauth-apps/using-oauth-apps/privileged-oauth-apps). SAML SSO authorization of the token on an enterprise-managed org was not checked.
- **API version.** go-gh sends `X-GitHub-Api-Version: 2022-11-28` by default ([`pkg/api/http_client.go` L28](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/api/http_client.go#L28)). The docs now default to `2026-03-10`. GitHub's docs data lists the JIT endpoint under both versions (`src/github-apps/data/fpt-2022-11-28/fine-grained-pat.json` and `fpt-2026-03-10/...` in [github/docs](https://github.com/github/docs/tree/main/src/github-apps/data)).

### 2. Request and response fields

From [docs: generate-jitconfig (repo)](https://docs.github.com/en/rest/actions/self-hosted-runners#create-configuration-for-a-just-in-time-runner-for-a-repository):

- Body: `name` (string, required), `runner_group_id` (integer, required), `labels` (array of strings, required, min 1, max 100, described as "the names of the custom labels to add"), `work_folder` (string, default `_work`, relative to the runner install dir).
- Status codes: `201`, `404`, `409 Conflict`, `422` ("Validation failed, or the endpoint has been spammed").
- Response (`201`): `runner` (`id`, `runner_group_id`, `name`, `os`, `status`, `busy`, `labels[]`, `ephemeral`, `version`) and `encoded_jit_config` (string). [docs: org variant, which the repo variant shares](https://docs.github.com/en/rest/actions/self-hosted-runners#create-configuration-for-a-just-in-time-runner-for-an-organization)

**`runner_group_id` for repo-level runners.**

- The runner-groups REST API only has `/orgs/{org}/...` and enterprise paths. There is no endpoint to discover a repository's group. [docs: self-hosted runner groups](https://docs.github.com/en/rest/actions/self-hosted-runner-groups)
- New runners "are automatically assigned to the default group unless otherwise specified". [docs: Runner groups](https://docs.github.com/en/actions/concepts/runners/runner-groups)
- `config.sh` picks the group marked `IsInternal` as the default and only prompts if non-hosted groups exist. [actions/runner `ConfigurationManager.cs` L213-250](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Configuration/ConfigurationManager.cs#L213-L250)
- microsandbox's official GitHub-runner guide calls the repo endpoint through `gh api` with `-F runner_group_id=1 -f 'labels[]=self-hosted' -f 'labels[]=msb-ephemeral' -f work_folder=_work`. [superradcompany/microsandbox `docs/how-to/ci-cd/github-actions-runner.mdx` L88-90](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/docs/how-to/ci-cd/github-actions-runner.mdx#L88-L90)
- Conclusion: use `1` **(unverified, but consistent across these sources)**. Once a runner is registered, its `runner_group_id` shows up in the list response, so a first live run can confirm it.

**Labels.** The docs call `labels` "custom labels". With `config.sh` the runner itself adds the `self-hosted`, OS and arch system labels unless `--no-default-labels` ([`ConfigurationManager.cs` L652-700](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Configuration/ConfigurationManager.cs#L652-L700)). The JIT path skips that code, because the server builds the registration. Whether the server adds `self-hosted`/`Linux`/`X64` for JIT is **unverified**. microsandbox's guide passes `self-hosted` explicitly. The Pool should send `["self-hosted", "<label>"]` so that `runs-on: [self-hosted, msb-docker]` matches either way.

**What `encoded_jit_config` is.** `run.sh --jitconfig <b64>` base64-decodes it into a JSON map of *file name → base64 content*. It writes each entry into the runner root as a hidden file, one of which can be the RSA credentials file, then loads settings and runs. No further registration step happens. [actions/runner `Runner.cs` L234-267](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Runner.cs#L234-L267). Treat the string as a credential for that one runner. microsandbox's guide passes it on stdin, not argv or env, for that reason ([L100-115](https://github.com/superradcompany/microsandbox/blob/c75e80c1f2990d80a6ec00906fb65cd58afad92d/docs/how-to/ci-cd/github-actions-runner.mdx#L100-L115)). How long an unused config stays valid is not documented.

### 3. Unused, crashed and stale runners

- **JIT.** "Just-in-time (JIT) runners can only run a single job. If JIT runners never run a job, they will automatically be removed. To remove a JIT runner before automatic removal, you must remove the JIT runner on GitHub." No delay is given. [docs: Removing self-hosted runners](https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/remove-runners)
- **Ephemeral and persistent.** "A self-hosted runner is automatically removed from GitHub if it has not connected to GitHub Actions for more than 14 days. An ephemeral self-hosted runner is automatically removed from GitHub if it has not connected to GitHub Actions for more than 1 day." (same page). The JIT response carries an `ephemeral` flag, and the docs call them "ephemeral, just-in-time runners" ([docs: Ephemeral runners for autoscaling](https://docs.github.com/en/actions/reference/runners/self-hosted-runners#ephemeral-runners-for-autoscaling)). So assume the 1-day ceiling for a JIT runner whose VM died **(inferred)**.
- **One job, then deregistered.** With `--ephemeral`, "The GitHub Actions service will then automatically de-register the runner after it has processed one job" (same page). The runner's help text says the same: "let the service un-configure the runner after the job finishes" ([`Runner.cs` L1170](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Runner.cs#L1170)). On exit the runner only deletes its *local* config ([`Runner.cs` L880-898](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Runner.cs#L880-L898)). It ignores errors when deleting its session, "since the runner might already be deleted from the server side".
- **Deleting an idle runner through the API makes the listener exit.** If the registration disappears while the runner polls, it prints "The runner no longer exists on the server. Cleaning up local configuration." and exits ([`Runner.cs` L841-846](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Runner.cs#L841-L846)). A revoked token also shuts it down ([L908-911](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Runner.cs#L908-L911)). So on shutdown the Pool can `DELETE` idle runners and their VMs will exit by themselves **(inferred from the source, not tested)**.
- **Limits that stale runners count toward** ([docs: Actions limits](https://docs.github.com/en/actions/reference/limits)): "Runners per runner group: 10,000 runners registered at the same time per runner group" and "Runner registrations: 1500 runners / 5 minutes / repository/org/enterprise". Offline-but-registered runners presumably count toward the 10,000 until they are removed **(inferred)**. They also keep their name, so reusing a fixed Slot name before cleanup would hit `409` with JIT **(inferred)**. Unique names per Runner avoid that.
- **Queueing.** A job with no matching runner "will remain queued until the 24 hour timeout period expires" ([docs](https://docs.github.com/en/actions/reference/runners/self-hosted-runners#ephemeral-runners-for-autoscaling); [limits](https://docs.github.com/en/actions/reference/limits): job queue time 24 h).

### 4. Listing, deleting and rate limits

- `GET /repos/{owner}/{repo}/actions/runners?name=&per_page=&page=` returns `total_count` and `runners[]`, each with `id`, `name`, `os`, `status`, `busy`, `labels`, `ephemeral`. There is no prefix, wildcard or label filter. [docs](https://docs.github.com/en/rest/actions/self-hosted-runners#list-self-hosted-runners-for-a-repository)
- `DELETE /repos/{owner}/{repo}/actions/runners/{runner_id}` "forces the removal … when the machine you were using no longer exists". It returns `204` or `422`. [docs](https://docs.github.com/en/rest/actions/self-hosted-runners#delete-a-self-hosted-runner-from-a-repository). That a `422` means the runner is busy is **(inferred)**.
- **Primary limit:** 5,000 requests/hour for a user. All OAuth apps and PATs acting for that user share it. [docs: REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api)
- **Secondary limits** (same page): at most 100 concurrent requests and 900 points/min per REST endpoint (GET = 1 point, POST/DELETE = 5). Content creation is capped at "no more than 80 content-generating requests per minute and no more than 500 content-generating requests per hour". On a breach you get `403`/`429`; honour `retry-after` and `x-ratelimit-reset`.
- **What this means for the Pool (inferred):** with N Slots, each Runner costs 1 POST, plus at most 1 DELETE and occasional list GETs. Even 100 jobs/hour is a few hundred points/hour. Refill sequentially or with small concurrency, and back off on 403/429/422 ("endpoint has been spammed").

### 5. How go-gh resolves the token, repo and host

**Token.**

- `auth.TokenForHost(host)` checks, in order:
  1. `GH_TOKEN` / `GITHUB_TOKEN` for github.com, `*.ghe.com` and `github.localhost`, or `GH_ENTERPRISE_TOKEN` / `GITHUB_ENTERPRISE_TOKEN` for other hosts.
  2. `hosts.<host>.oauth_token` in gh's `hosts.yml`.
  3. Shelling out to `gh auth token --secure-storage --hostname <host>`, using `GH_PATH` or `gh` on PATH.

  [go-gh `pkg/auth/auth.go` L36-113](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/auth/auth.go#L36-L113). gh sets `GH_PATH` when it dispatches an extension ([cli/cli `pkg/cmd/extension/manager.go` L134](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/extension/manager.go#L134)). It does not inject a token.
- `api.NewRESTClient` resolves the token **once, at construction**, and bakes it into a header round-tripper (`Authorization: token …`). [`pkg/api/client_options.go` L120-135](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/api/client_options.go#L120-L135), [`pkg/api/http_client.go` L203](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/api/http_client.go#L203)
- **New: gh's expiring tokens.** gh v2.103.0 (2026-10-21) added opt-in expiring, auto-refreshing OAuth tokens: `gh auth login --expiring` or `gh config set expiring_credentials enabled` ([release notes](https://github.com/cli/cli/releases/tag/v2.103.0); [`pkg/cmd/auth/login/login.go` L107-114](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/auth/login/login.go#L107-L114)).
  - These are stored under `refreshable_oauth_token`, "kept separate from the non-expiring oauth_token so that neither old readers nor go-gh mistake the record for a bare token string" ([`internal/config/refreshable.go` L22-25](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/internal/config/refreshable.go#L22-L25)).
  - go-gh therefore falls through to `gh auth token --secure-storage`, which refreshes the token if needed and returns it ([`pkg/cmd/auth/token/token.go` L90-111](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/auth/token/token.go#L90-L111)).
  - gh warns that such a token "will expire, or may be revoked at any time as a result of an expected refresh" ([L156-163](https://github.com/cli/cli/blob/b546819fd547ef8c97da470bb00f2aaad4165c84/pkg/cmd/auth/token/token.go#L156-L163)).
  - The lifetime comes from the server's `expires_in`.
  - **Implication (inferred):** a long-running Pool that builds one `RESTClient` at startup will start getting `401` once an expiring token rotates. Rebuild the client (re-resolving the token) per refill, or on `401`.
- **Repo.** `repository.Current()` works like this ([`pkg/repository/repository.go` L119-165](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/repository/repository.go#L119-L166)):
  - It honours `GH_REPO` first.
  - Otherwise it lists git remotes, translates SSH aliases, and keeps only remotes whose host is in `auth.KnownHosts()` (`GH_HOST`, github.com if a token exists, `hosts.yml` keys).
  - It sorts them `upstream` > `github` > `origin` > others ([`internal/git/remote.go` L26-41](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/internal/git/remote.go#L26-L41)). A `remote.<name>.gh-resolved` set by `gh repo set-default` wins.
  - In a fork with an `upstream` remote and no set-default, `Current()` returns the **upstream** repo, where the user is often not an admin **(inferred consequence)**. Print the resolved repo before registering.
- **`-R`.** go-gh has no flag handling. `repository.Parse(s)` accepts `OWNER/REPO` (host from `auth.DefaultHost()`), `HOST/OWNER/REPO`, or a git URL ([L26-63](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/repository/repository.go#L26-L63)).
- **Host.** `auth.DefaultHost()` returns `GH_HOST`, else the only host in `hosts.yml`, else `github.com` ([`auth.go` L141-159](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/auth/auth.go#L141-L159)). `api.DefaultRESTClient()` = `NewRESTClient(ClientOptions{})` uses that default host, **not** the repo's ([`rest_client.go` L22-24](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/api/rest_client.go#L22-L24)). Build the client with `ClientOptions{Host: repo.Host}`. Base URL ([`restPrefix`, L178-190](https://github.com/cli/go-gh/blob/4e83b33c301de3d1d4d30df34db06648f898c389/pkg/api/rest_client.go#L178-L190)):
  - GHES: `https://HOST/api/v3/`
  - github.com: `https://api.github.com/`
  - ghe.com tenancy: `https://api.TENANT.ghe.com/`
  - An `api_host` override in `hosts.yml` replaces any of these.
- **GHES support for JIT.** The repo `generate-jitconfig` endpoint is documented for GHES 3.19 ([docs](https://docs.github.com/en/enterprise-server@3.19/rest/actions/self-hosted-runners#create-configuration-for-a-just-in-time-runner-for-a-repository)). Older GHES docs are archived and could not be fetched in markdown, so the minimum GHES version was not determined. The runner registers through `https://HOST/api/v3/actions/runner-registration` on GHES, versus `https://api.github.com/actions/runner-registration` on github.com ([`ConfigurationManager.cs` L830-841](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Configuration/ConfigurationManager.cs#L830-L841)).

### 6. Registration-token flow details (for comparison)

- `config.sh --url <repo> --token <reg-token> --ephemeral --unattended [--name] [--labels] [--replace] [--disableupdate]`. [`Runner.cs` L1156-1170](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Runner.cs#L1156-L1170)
- What happens inside the VM:
  1. Exchange the token at `/actions/runner-registration` ([`ConfigurationManager.cs` L830-841](https://github.com/actions/runner/blob/aea9aaa3a0b49933412c6f59d7f05be037207a28/src/Runner.Listener/Configuration/ConfigurationManager.cs#L830-L841)).
  2. Generate an RSA key and pick the default group (L200-250).
  3. Look for a runner with the same name. If one exists and `--replace` was not given, unattended mode throws "A runner exists with the same name" (L264-332).
  4. Create the runner with default labels (L676-700).
- The docs' guidance on ephemeral `config.sh` runners and autoscaling also points to the JIT REST API as the alternative. [docs](https://docs.github.com/en/actions/reference/runners/self-hosted-runners#ephemeral-runners-for-autoscaling)
- If `generate-jitconfig` isn't available (older GHES), this is the fallback. It needs the same token and the same admin rights.

## Open items for #7 / the spec (need one live test on a throwaway repo)

1. Does JIT add `self-hosted`/`Linux`/`X64` on top of the passed `labels`?
2. Is `runner_group_id: 1` always the repo default group? Read it back from `runner.runner_group_id`.
3. What exact status and body does a non-admin get from the POST (403 or 404)? What does `409` look like for a duplicate name?
4. How soon does a never-started JIT runner disappear (minutes, or the 1-day ephemeral rule)?
5. Does `generate-jitconfig` count toward the 500/hour content-creation secondary limit?
