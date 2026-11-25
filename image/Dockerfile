# Runner image, Image contract :1 (see GLOSSARY.md and docs/adr/0002).
#
# Contract :1: a `runner` user with actions-runner in /home/runner (run.sh),
# passwordless sudo, dockerd and docker (with buildx), iptables (legacy),
# bash, mkfs.ext4 and curl. Verified by image/check-contract.sh.
#
# RUNNER_VERSION has no default on purpose: CI always passes the latest
# actions-runner release, and an empty value fails at FROM (never `latest`).
# Local build:
#   docker build --build-arg RUNNER_VERSION=2.x.y -t grm:dev image/
ARG RUNNER_VERSION
FROM ghcr.io/actions/actions-runner:${RUNNER_VERSION}

USER root
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends iptables e2fsprogs curl \
    && update-alternatives --set iptables /usr/sbin/iptables-legacy \
    && update-alternatives --set ip6tables /usr/sbin/ip6tables-legacy \
    && command -v docker \
    && command -v dockerd \
    && docker buildx version \
    && rm -rf /var/lib/apt/lists/*

USER runner
WORKDIR /home/runner
