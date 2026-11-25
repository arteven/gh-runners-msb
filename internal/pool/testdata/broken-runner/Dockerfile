# Non-conforming Runner image: no Docker daemon, so the Self-test must fail
# ("docker ready" and "docker info as runner"), fast, with a readable log.
ARG BASE
FROM ${BASE}
USER root
RUN rm -f "$(command -v dockerd)"
USER runner
