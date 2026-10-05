.DEFAULT_GOAL := build

.PHONY: build test check cross demo integration macos-app macos-test macos-installer macos-installer-test linux-gui linux-gui-test windows-package

linux-gui:
	python3 scripts/build-linux-gui.py

linux-gui-test:
	python3 -m unittest discover -s linux/tests -v

windows-package: cross
	python3 scripts/build-windows-package.py

macos-installer:
	./scripts/build-macos-installer.sh

macos-installer-test: macos-installer
	python3 scripts/test-macos-installer.py

macos-app:
	./scripts/build-macos-app.sh

macos-test: macos-app
	python3 scripts/test-macos-app.py

build:
	CGO_ENABLED=0 go build -trimpath -o bin/meldnet ./cmd/meldnet
	CGO_ENABLED=0 go build -trimpath -o bin/meldnetd ./cmd/meldnetd

test:
	go test -race ./...

check:
	@test -z "$$(gofmt -l cmd internal)" || (echo 'Run gofmt -w cmd internal'; exit 1)
	@if rg -n '"os/exec"' cmd internal --glob '!**/*_test.go'; then echo 'Application must not launch subprocesses'; exit 1; fi
	go vet ./...
	@if go list -deps ./cmd/meldnetd | grep -Eq '^charm\.land/'; then echo 'Daemon must not depend on UI packages'; exit 1; fi
	go test -race ./...

cross:
	@set -eu; for os in darwin linux windows; do \
	  for arch in amd64 arm64; do \
	    ext=""; if [ "$$os" = windows ]; then ext=.exe; fi; \
	    echo "Building $$os/$$arch"; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -o bin/$$os-$$arch/meldnet$$ext ./cmd/meldnet; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -o bin/$$os-$$arch/meldnetd$$ext ./cmd/meldnetd; \
	  done; \
	done

demo: build
	./bin/meldnetd --simulate --data-dir "$(CURDIR)/.demo/state" --socket "$(CURDIR)/.demo/control.sock"

integration: cross
	./scripts/integration-linux.sh
	./scripts/integration-primary.sh
	./scripts/integration-multinetwork.sh
	./scripts/test-dns-linux.sh
