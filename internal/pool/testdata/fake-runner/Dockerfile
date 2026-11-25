# Test image: the Runner image with run.sh swapped for a stand-in that behaves
# like a JIT runner (idle until told to take a job, then exit).
ARG BASE
FROM ${BASE}
COPY --chmod=755 run.sh /home/runner/run.sh
