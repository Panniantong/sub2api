# ARM64 runtime smoke tests without installing binfmt handlers on the host.
FROM --platform=linux/arm64 python:3.12-alpine AS armroot
FROM --platform=linux/amd64 ubuntu:24.04
RUN apt-get update && apt-get install -y --no-install-recommends qemu-user-static proot ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=armroot / /armroot/
ENV MIHOMO_INSTALL_SMOKE=1
ENTRYPOINT ["proot", "-q", "/usr/bin/qemu-aarch64-static", "-R", "/armroot", "-b", "/tests:/tests", "-b", "/tmp:/tmp", "-w", "/tmp", "/tests"]
