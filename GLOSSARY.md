# gh runners-msb

A `gh` CLI extension that keeps ephemeral GitHub Actions self-hosted runners alive in microsandbox (msb) microVMs, one job per VM.

## Language

**Pool**:
The foreground supervisor that keeps a fixed number of Slots filled for one repository and label, until stopped.
_Avoid_: Fleet, scale set, daemon

**Pool ID**:
The opaque identity of a Pool, the same across restarts for one user, repository and label; at most one Pool with a given ID runs at a time, and anything carrying its ID that no running Pool owns is a leftover.
_Avoid_: Owner ID, pool name, prefix

**Slot**:
A numbered position in a Pool, lasting the Pool's lifetime, refilled whenever its Runner exits.
_Avoid_: Worker

**Runner**:
One ephemeral msb sandbox registered with GitHub as a repository-level self-hosted runner; it takes exactly one job and is then destroyed.
_Avoid_: Worker, VM, box, agent

**Runner image**:
The OCI image every Runner boots from.
_Avoid_: Base image, template

**Image contract**:
What a Runner image must provide for the Pool to start a Runner in it; identified by a major number.
_Avoid_: Image spec, image requirements

**Backend**:
The kind of msb profile (`local` or `remote`) the Pool creates its Runners on, chosen once when the Pool starts and fixed until it stops.
_Avoid_: Context, provider, target

**Writable budget**:
The disk space one Runner may write, split into the Docker share and the workspace; the Runner image never counts against it.
_Avoid_: Disk size, root disk

**Self-test**:
One unregistered sandbox the Pool boots from the Runner image before filling any Slot, to prove a Runner can start there; if it fails, the Pool does not start.
_Avoid_: Warmup, smoke test, canary

**Drain**:
Stopping a Pool gently: no Slot is refilled, idle Runners are removed, and busy Runners finish their jobs first.
_Avoid_: Graceful shutdown, cordon

**Cleanup**:
Removing every leftover that carries a Pool's ID, both its sandboxes and its registrations, while no Pool with that ID is running.
_Avoid_: Purge, reset, prune, teardown
