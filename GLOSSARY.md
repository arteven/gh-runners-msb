# gh runners-msb

A `gh` CLI extension that keeps ephemeral GitHub Actions self-hosted runners alive in microsandbox (msb) microVMs, one job per VM.

## Language

**Pool**:
The foreground supervisor that keeps a fixed number of Slots filled for one repository and label, until stopped.
_Avoid_: Fleet, scale set, daemon

**Slot**:
A numbered position in a Pool, lasting the Pool's lifetime, refilled whenever its Runner exits.
_Avoid_: Worker

**Runner**:
One ephemeral msb sandbox registered with GitHub as a repository-level self-hosted runner; it takes exactly one job and is then destroyed.
_Avoid_: Worker, VM, box, agent

**Runner image**:
The OCI image every Runner boots from.
_Avoid_: Base image, template

**Backend**:
The kind of msb profile (`local` or `remote`) the Pool creates its Runners on.
_Avoid_: Context, provider, target
