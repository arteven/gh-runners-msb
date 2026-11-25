# The Pool resolves the active msb profile itself and pins one explicit context

The Pool must know its Backend before its first create: Docker storage, image-refresh failure handling, cleanup and the remote quotas preflight all branch on it, and the quotas preflight also needs the remote API URL and key. The msb Go SDK (v0.8.2) has no public way to ask which Backend the active profile selects, and its package-level calls re-read the profile on every call, so a `msb context use` mid-run would send the next refill to the other Backend. So at start the Pool resolves the active profile with msb's own precedence (managed file > `MSB_BACKEND` > `MSB_PROFILE` > saved `active_profile` > local), builds one explicit context from the result (`NewLocalContext` or `NewRemoteContextWithURL`), and makes every SDK call through it until it stops. Anything it can't resolve stops the Pool before a sandbox is created.

## Considered Options

- **Attempt a local-only option and read the refusal as "remote".** Rejected: it string-matches an untyped error.
- **Create a throwaway sandbox and read `sb.Context()`.** Rejected: the quotas preflight must run before anything is created, a sandbox reveals no API key or URL, and on the Free plan the probe uses up the only concurrent sandbox.
- **Probe with `Image.List` or `GetDefaultVolume`.** Rejected: `Image.List` reports "remote" and "misconfigured" with the same error, and neither probe returns a context to pin or the key and URL.
- **Shell out to `msb context`.** Rejected: the extension never runs the `msb` CLI.

## Consequences

- We are coupled to msb's `config.json` and `managed.json` format (version 1). Other versions stop the Pool with an upgrade hint. Table-driven tests encode the precedence, and SDK-bump PRs diff the SDK's `config/backend.rs`, `persistence.rs`, `mod.rs` and `context/profile.rs` between tags.
- Only `env:` and `inline:` key references resolve. `keyring:` stops the Pool with msb's own wording, as msb itself does.
- If upstream re-exposes a public configured-backend getter (it existed as `DefaultBackendInfo()` until the v0.8 context refactor), the resolver is deleted.
