# Builder image for P4wnP1 A.L.O.A. flashable Raspberry Pi images.
#
# Runs on any Docker host whose kernel exposes loop devices and has binfmt
# handlers registered for the foreign ARM architecture (see image/build.sh,
# which installs them via tonistiigi/binfmt). The container itself only needs
# to be the HOST architecture -- the foreign-arch chroot runs under QEMU.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        util-linux parted fdisk dosfstools e2fsprogs \
        xz-utils zstd ca-certificates curl \
        file rsync kmod \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /work
ENTRYPOINT ["/bin/bash"]
