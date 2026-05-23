FROM kalilinux/kali-rolling

# ----------------------------------------------------------------------------
# LEGACY GopherJS build image. Builds the web client (webapp.js) only.
#
# As of the May 2026 hardening pass, the service-side Go code uses os.ReadFile
# and friends (Go 1.16+), so this Go 1.12 toolchain CANNOT build the service
# binaries any more. Use build_support/build.sh on a host with Go 1.21+ for
# the service + CLI binaries, then bring webapp.js over from this container.
# Replacing GopherJS with a modern Vue 3 frontend is the larger deferred
# slice -- see KNOWN_ISSUES.md.
# ----------------------------------------------------------------------------

WORKDIR /root
RUN apt-get update && apt-get -y install git wget nano
# install Go 1.12 instead of Kali bundled Go (GopherJS v1.18.0-beta2 needs 1.12)
RUN wget https://dl.google.com/go/go1.12.16.linux-amd64.tar.gz
RUN tar -C /usr/local -xzf go1.12.16.linux-amd64.tar.gz
ENV PATH "$PATH:/usr/local/go/bin:/root/go/bin"
RUN go get -u github.com/gopherjs/gopherjs
# clone the at0m-b0mb fork
RUN git clone https://github.com/at0m-b0mb/P4wnP1_aloa

# P4wnP1 webclient dependencies
RUN go get -u github.com/johanbrandhorst/protobuf/...
# manual population of go source tree, with dependencies of P4wnP1 webclient
# using git clone.
# This is really messy, but unfortunately GopherJS has no module support (dependency tracking)
# At least this leaves room to modify "git clone" commands to grab proper branches,
# in case this is required
RUN mkdir -p /usr/local/go/src/github.com/mame82/
RUN git clone https://github.com/mame82/hvue /usr/local/go/src/github.com/mame82/hvue
RUN git clone https://github.com/mame82/mvuex /usr/local/go/src/github.com/mame82/mvuex
# copy already cloned repo of P4wnP1 instead of cloning (assures same branch in Go
# source tree)
RUN cp -R P4wnP1_aloa/ /usr/local/go/src/github.com/mame82/


# run a test build, otherwise the image could be used interactively 
# with build.sh as used below
WORKDIR /root/P4wnP1_aloa/build_support
RUN ./build.sh


