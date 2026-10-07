SHELL := /bin/bash
PATH := /usr/local/go/bin:$(PATH)

.PHONY: all help compile build-armv6 build-service-armv6 build-cli-armv6 build-hashpw-armv6 \
        build-arm64 build-service-arm64 build-cli-arm64 build-hashpw-arm64 \
        image image-armhf image-arm64 contrast smoke feature-test access-control check-quoting oled-sim oled-shots verify check-js check-rpc check-render mock dep install installkali remove lint test

all: compile

help:
	@echo "P4wnP1 A.L.O.A. -- Makefile targets"
	@echo
	@echo "  make build-armv6   Cross-compile P4wnP1_service + P4wnP1_cli + p4wnp1-hashpw"
	@echo "                     for Pi Zero W (linux/arm/6) into build/. THIS is what you"
	@echo "                     want before running install.sh on the Pi."
	@echo "  make compile       Cross-compile binaries + webapp.js into build/"
	@echo "                     (legacy; use build_support/build.sh for the GopherJS web app)"
	@echo "  make dep           Install Go toolchain helpers (gopherjs)"
	@echo "  make build-arm64   Same, for Pi Zero 2 W / 3 / 4 / 5 (linux/arm64)."
	@echo "  make image         Build flashable .img.xz for BOTH architectures"
	@echo "                     (see image/README.md). Needs Docker."
	@echo "  make contrast      Check the web console palette against WCAG AA"
	@echo "  make smoke         Run the service in a container and verify it works"
	@echo "  make feature-test  Exercise all 83 RPCs against the real binary and"
	@echo "                     report PASS / expected-without-hardware / FAIL"
	@echo "  make access-control  Attack the running service: every check is an"
	@echo "                     attack that must FAIL"
	@echo "  make check-quoting Values install.sh writes must survive being sourced"
	@echo "  make oled-sim      Drive the OLED interface in a browser -- no Pi,"
	@echo "                     no HAT and no SD card needed"
	@echo "  make oled-shots    Render every OLED screen to one PNG"
	@echo "  make verify        Run every gate: js, render, rpc shapes, contrast,"
	@echo "                     unit tests, vet (both arches), smoke, feature-test"
	@echo "  make check-js      Syntax-check the web console JavaScript"
	@echo "  make check-rpc     Check console RPC payloads against the .proto"
	@echo "  make check-render  Render every console view in jsdom"
	@echo "  make mock          Serve the console against a mock device (no Pi needed)"
	@echo "  make test          Run unit tests (auth + jsonbridge)"
	@echo "  make lint          Run shellcheck + golangci-lint (require both installed)"
	@echo "  make install       Install binaries + data into /usr/local on the current host"
	@echo "  make installkali   Same as install, plus systemd enable + Kali-only steps"
	@echo "  make remove        Uninstall service + binaries (keeps /usr/local/P4wnP1)"
	@echo
	@echo "Install / build documentation: INSTALL.md"

# Cross-compile each binary individually. `-trimpath -ldflags='-s -w'`
# strips debug info to keep the Pi Zero W's modest storage happy.
GO_ENV_ARMV6 := GOOS=linux GOARCH=arm GOARM=6
GO_BUILD_FLAGS := -trimpath -ldflags="-s -w"

build-service-armv6:
	$(GO_ENV_ARMV6) go build $(GO_BUILD_FLAGS) -o build/P4wnP1_service ./cmd/P4wnP1_service

build-cli-armv6:
	$(GO_ENV_ARMV6) go build $(GO_BUILD_FLAGS) -o build/P4wnP1_cli     ./cmd/P4wnP1_cli

build-hashpw-armv6:
	$(GO_ENV_ARMV6) go build $(GO_BUILD_FLAGS) -o build/p4wnp1-hashpw  ./cmd/p4wnp1-hashpw

# arm64 targets. The service used to be gated to 32-bit ARM by build tags
# (`+build linux,arm` on service.go and friends); that gate was incidental, not
# a real dependency, and removing it made Pi Zero 2 W / 3 / 4 / 5 buildable.
GO_ENV_ARM64 := GOOS=linux GOARCH=arm64

build-service-arm64:
	$(GO_ENV_ARM64) go build $(GO_BUILD_FLAGS) -o build/arm64/P4wnP1_service ./cmd/P4wnP1_service

build-cli-arm64:
	$(GO_ENV_ARM64) go build $(GO_BUILD_FLAGS) -o build/arm64/P4wnP1_cli     ./cmd/P4wnP1_cli

build-hashpw-arm64:
	$(GO_ENV_ARM64) go build $(GO_BUILD_FLAGS) -o build/arm64/p4wnp1-hashpw  ./cmd/p4wnp1-hashpw

build-arm64: build-service-arm64 build-cli-arm64 build-hashpw-arm64
	@echo
	@echo "Built for Pi Zero 2 W / 3 / 4 / 5 (linux/arm64):"
	@ls -la build/arm64/

image:
	./image/build.sh --arch all

image-armhf:
	./image/build.sh --arch armhf

image-arm64:
	./image/build.sh --arch arm64

contrast:
	python3 tools/check_contrast.py

# Parse the console JavaScript. A missing paren makes the WHOLE file fail to
# load, so the console renders nothing -- a total outage from one character,
# invisible to every other check here.
check-js:
	./tools/check-js.sh

# Check every RPC payload the console sends against proto/grpc.proto. The JSON
# bridge discards unknown fields, so a misspelled name is accepted and silently
# dropped -- which is how five real bugs shipped, including one that ERASED the
# boot configuration.
check-rpc:
	python3 tools/check-rpc-shapes.py

# Render every console view in jsdom. node --check only parses: it happily
# accepted a helper that called itself, which silently removed every table in
# the console. This catches runtime failures inside a view.
check-render:
	./tools/check-render.sh

# Serve the console against a mock device, for working on the UI with no Pi.
mock:
	python3 tools/mock-service.py

# End-to-end smoke test: runs the REAL service binary against the REAL dist
# tree in a container and checks that it comes up, serves, authenticates and
# shuts down. "It compiles" and "the unit tests pass" were both true while the
# service panicked on every cold boot -- only starting it catches that.
smoke:
	./tools/smoke-test.sh arm64

# Beyond "it starts": calls every unary RPC the service exposes and classifies
# each answer. A failure that is CORRECT without a Pi attached (no USB gadget,
# no WiFi) counts as expected; anything else is a real bug. This is the gate
# that found StoreDeployedWifiSettings marshalling a nil message.
feature-test:
	./tools/feature-test.sh arm64

# The other gates ask "does it work?". This one asks "can it be made to do
# something it should refuse?" -- every check is an attack that must fail.
# Several were written by first demonstrating the attack succeeding against
# the real binary, including a symlink in /tmp that got a root-owned cron job
# written through the file-IO allowlist.
access-control:
	./tools/access-control-test.sh arm64

# /etc/p4wnp1/initial.conf is SOURCED AS ROOT by the firstboot helper, which
# runs under `set -e` and is what creates the admin account. An apostrophe in
# an SSID or passphrase made that file unparseable, so firstboot aborted and
# the device came up with no account at all; a crafted value ran as root.
check-quoting:
	./tools/check-shell-quoting.sh

# The OLED interface, in a browser, against a fake device. The UI, fonts,
# menus and state machine are the same code the device runs; only the panel
# and the joystick are swapped for a PNG and some buttons.
oled-sim:
	go run ./cmd/p4wnp1-oled-sim

# Every OLED screen on one sheet, including the boot splash and an error
# state. The fastest way to review a display nobody has in front of them.
oled-shots:
	OLED_SHEET=$(CURDIR)/image/out/oled-screens.png go test -count=1 -run TestContactSheet ./oled/
	@echo "wrote image/out/oled-screens.png"

# The whole suite, in the order that fails cheapest-first.
verify:
	./tools/check-js.sh
	./tools/check-render.sh
	./tools/check-shell-quoting.sh
	python3 tools/check-rpc-shapes.py
	python3 tools/check_contrast.py
	$(MAKE) test
	GOOS=linux GOARCH=arm GOARM=6 go vet ./service/... ./cli_client/... ./oled/... ./cmd/...
	GOOS=linux GOARCH=arm64 go vet ./service/... ./cli_client/... ./oled/... ./cmd/...
	$(MAKE) test-linux
	$(MAKE) smoke
	$(MAKE) feature-test
	$(MAKE) access-control
	@echo
	@echo "All gates passed."

build-armv6: build-service-armv6 build-cli-armv6 build-hashpw-armv6
	@echo
	@echo "Built for Pi Zero W (linux/arm/6):"
	@ls -la build/P4wnP1_service build/P4wnP1_cli build/p4wnp1-hashpw

# The service package only builds for linux (USB gadget, netlink, HID), so its
# tests run in a container. jsonbridge and auth are portable and run anywhere.
test:
	go test -count=1 ./service/auth/... ./service/jsonbridge/... ./oled/...

test-linux:
	docker run --rm --platform linux/arm64 \
	  -v "$(CURDIR):/src" -v "$$(go env GOMODCACHE):/gomodcache" \
	  -e GOMODCACHE=/gomodcache -e GOFLAGS=-mod=mod -e CGO_ENABLED=0 \
	  -w /src golang:1.26-bookworm go test -count=1 ./service/...

lint:
	@command -v shellcheck >/dev/null || { echo "shellcheck not installed"; exit 1; }
	shellcheck --severity=warning install.sh \
	          dist/scripts/firstboot-secure-defaults.sh \
	          dist/scripts/p4wnp1-healthcheck.sh \
	          dist/scripts/wifi_covert_channel.sh dist/scripts/trigger-aware.sh \
	          build_support/build.sh \
	          image/build.sh image/lib/stage.sh image/lib/customize.sh image/lib/verify.sh \
	          tools/smoke-test.sh tools/check-js.sh tools/check-render.sh \
	          tools/feature-test.sh tools/access-control-test.sh tools/live-console.sh \
	          tools/check-shell-quoting.sh
	@# One shell, not two: each recipe line gets its own shell, so an `exit 0`
	@# on the guard line ended only that shell and golangci-lint ran anyway --
	@# which made `make lint` fail on every machine that does not have it.
	@if command -v golangci-lint >/dev/null; then \
	    golangci-lint run ./...; \
	else \
	    echo "golangci-lint not installed; skipping go lint"; \
	fi
	./tools/check-js.sh
	python3 tools/check-rpc-shapes.py
	./tools/check-render.sh

# make dep runs without sudo
dep:
	# sudo apt-get -y install git screen hostapd autossh bluez bluez-tools bridge-utils policykit-1 genisoimage iodine haveged
	# sudo apt-get -y install tcpdump
	# sudo apt-get -y install python-pip python-dev

	# before installing dnsmasq, the nameserver from /etc/resolv.conf should be saved
	# to restore after install (gets overwritten by dnsmasq package)
	# cp /etc/resolv.conf /tmp/backup_resolv.conf
	# sudo apt-get -y install dnsmasq
	# sudo /bin/bash -c 'cat /tmp/backup_resolv.conf > /etc/resolv.conf'

	# python dependencies for HIDbackdoor
	# sudo pip install pycrypto # already present on stretch
	# sudo pip install pydispatcher

	# install go
	# wget https://storage.googleapis.com/golang/go1.10.linux-armv6l.tar.gz
	# sudo tar -C /usr/local -xzf go1.10.linux-armv6l.tar.gz

	export PATH="$$PATH:/usr/local/go/bin"

	# put into ~/.profile
	# ToDo: check if already present
	# echo "export PATH=\$$PATH:/usr/local/go/bin" >> ~/.profile
	# sudo bash -c 'echo export PATH=\$$PATH:/usr/local/go/bin >> ~/.profile'

	# install gopherjs
	go install github.com/gopherjs/gopherjs

	# we don't need protoc + protoc-grpc-web, because the proto file is shipped pre-compiled

	# go dependencies for webapp (without my own)
	#go get google.golang.org/grpc
	#go get -u github.com/improbable-eng/grpc-web/go/grpcweb
	#go get -u github.com/gorilla/websocket

# This target probably needs to be run at least once to get the dependencies on
# the go path. But after that, you probably actually want to run:
# $ cd build_support && ./build.sh && cd ..
# instead, to build with the right GOOS and GOARCH settings.
compile:
	go get github.com/mame82/P4wnP1_aloa/... # partially downloads again, but we need the library packages in go path to build
	# <--- second compilation, maybe -d flag on go get above is better
	env GOBIN=$(CURDIR)/build go install ./cmd/... # compile all main packages to the build folder

	# compile the web app
	# ToDo: (check if dependencies have been fetched by 'go get', even with the build js tags)
	$(HOME)/go/bin/gopherjs get github.com/mame82/P4wnP1_aloa/web_client/...
	$(HOME)/go/bin/gopherjs build -m -o build/webapp.js web_client/*.go

installkali:
	#apt-get -y install git screen hostapd autossh bluez bluez-tools bridge-utils policykit-1 genisoimage iodine haveged
	#apt-get -y install tcpdump
	#apt-get -y install python-pip python-dev

	# before installing dnsmasq, the nameserver from /etc/resolv.conf should be saved
	# to restore after install (gets overwritten by dnsmasq package)
	#cp /etc/resolv.conf /tmp/backup_resolv.conf
	#apt-get -y install dnsmasq
	#/bin/bash -c 'cat /tmp/backup_resolv.conf > /etc/resolv.conf'

	# python dependencies for HIDbackdoor
	sudo pip install pydispatcher

	cp build/P4wnP1_service /usr/local/bin/
	cp build/P4wnP1_cli /usr/local/bin/
	cp build/p4wnp1-hashpw /usr/local/bin/
	cp dist/P4wnP1.service /etc/systemd/system/P4wnP1.service
	cp dist/p4wnp1-firstboot.service /etc/systemd/system/p4wnp1-firstboot.service
	# copy over keymaps, scripts and www data
	mkdir -p /usr/local/P4wnP1
	cp -R dist/keymaps /usr/local/P4wnP1/
	cp -R dist/scripts /usr/local/P4wnP1/
	cp -R dist/HIDScripts /usr/local/P4wnP1/
	cp -R dist/www /usr/local/P4wnP1/
	cp -R dist/db /usr/local/P4wnP1/
	cp -R dist/helper /usr/local/P4wnP1/
	cp -R dist/ums /usr/local/P4wnP1/
	cp -R dist/legacy /usr/local/P4wnP1/
	cp build/webapp.js /usr/local/P4wnP1/www
	cp build/webapp.js.map /usr/local/P4wnP1/www
	chmod 0755 /usr/local/P4wnP1/scripts/firstboot-secure-defaults.sh
	-chmod 0755 /usr/local/P4wnP1/scripts/p4wnp1-healthcheck.sh

	# careful testing
	#sudo update-rc.d dhcpcd disable
	#sudo update-rc.d dnsmasq disable
	systemctl disable networking.service # disable network service, relevant parts are wrapped by P4wnP1 (boottime below 20 seconds)

	# enable service
	systemctl enable haveged
	systemctl enable avahi-daemon
	systemctl enable P4wnP1.service
	systemctl enable p4wnp1-firstboot.service

install:
	cp build/P4wnP1_service /usr/local/bin/
	cp build/P4wnP1_cli /usr/local/bin/
	cp build/p4wnp1-hashpw /usr/local/bin/
	cp dist/P4wnP1.service /etc/systemd/system/P4wnP1.service
	cp dist/p4wnp1-firstboot.service /etc/systemd/system/p4wnp1-firstboot.service
	# copy over keymaps, scripts and www data
	mkdir -p /usr/local/P4wnP1
	cp -R dist/keymaps /usr/local/P4wnP1/
	cp -R dist/scripts /usr/local/P4wnP1/
	cp -R dist/HIDScripts /usr/local/P4wnP1/
	cp -R dist/www /usr/local/P4wnP1/
	cp -R dist/db /usr/local/P4wnP1/
	cp dist/bin/* /usr/local/bin/ 2>/dev/null || true
	cp build/webapp.js /usr/local/P4wnP1/www
	cp build/webapp.js.map /usr/local/P4wnP1/www
	chmod 0755 /usr/local/P4wnP1/scripts/firstboot-secure-defaults.sh
	-chmod 0755 /usr/local/P4wnP1/scripts/p4wnp1-healthcheck.sh

	# careful testing
	#sudo update-rc.d dhcpcd disable
	#sudo update-rc.d dnsmasq disable
	# systemctl disable networking.service # disable network service, relevant parts are wrapped by P4wnP1 (boottime below 20 seconds)

	# reinit service daemon
	# systemctl daemon-reload
	# enable service
	# systemctl enable haveged
	# systemctl enable P4wnP1.service
	# start service
	# service P4wnP1 start

remove:
	# stop service
	-systemctl stop P4wnP1.service
	-systemctl stop p4wnp1-firstboot.service
	# disable service
	-systemctl disable P4wnP1.service
	-systemctl disable p4wnp1-firstboot.service
	rm -f /usr/local/bin/P4wnP1_service
	rm -f /usr/local/bin/P4wnP1_cli
	rm -f /etc/systemd/system/P4wnP1.service
	rm -f /etc/systemd/system/p4wnp1-firstboot.service
	rm -R /usr/local/P4wnP1/    # this folder should be kept, if only an update should be applied
	# reinit service daemon
	systemctl daemon-reload

	#sudo update-rc.d dhcpcd enable

