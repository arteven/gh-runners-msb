# Docker data lives on a loop-mounted ext4 file on remote, an owned disk on local

`--disk` is a Runner's Writable budget and `--docker-disk` is Docker's share of it, on both Backends. Local keeps the original script's owned ext4 disk at `/var/lib/docker`, because the local root is a layered overlay that Docker's overlay storage can't sit on. The remote Backend has no owned or disk volumes. There, the start-up script creates a sparse ext4 file of `--docker-disk` on the root and loop-mounts it at `/var/lib/docker`. The remote root disk is `--disk` plus a fixed 2G allowance for the unpacked Runner image, so the budget means the same as on local.

## Considered Options

- **Docker on the remote root as-is.** It works today: a probe on 2026-11-25 found the cloud root is plain ext4, and dockerd's overlayfs storage passed. It was rejected because the layout is undocumented, and the root was an overlay until about 2026-09, where Docker fails. It would also make `--docker-disk` meaningless on remote.
- **tmpfs at `/var/lib/docker`.** Rejected because it spends RAM, and the Free plan has 1 GiB.
- **A loop file on both Backends.** This would spare the Pool from knowing its Backend before the first create. It was rejected because the Pool needs to know the Backend for other reasons anyway, and on local it would put ext4 inside a file on the overlay root for no gain.

## Consequences

- The Image contract requires `mkfs.ext4`.
- The probe output is on [the ticket](https://github.com/arteven/gh-runners-msb/issues/10).
