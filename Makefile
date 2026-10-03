.PHONY: build test check cross demo integration

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
	@set -eu; for os in darwin linux; do \
	  for arch in amd64 arm64; do \
	    echo "Building $$os/$$arch"; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -o bin/$$os-$$arch/meldnet ./cmd/meldnet; \
	    CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -o bin/$$os-$$arch/meldnetd ./cmd/meldnetd; \
	  done; \
	done

demo: build
	./bin/meldnetd --simulate --data-dir "$(CURDIR)/.demo/state" --socket "$(CURDIR)/.demo/control.sock"

integration: cross
	./scripts/integration-linux.sh
	./scripts/integration-primary.sh
	./scripts/test-dns-linux.sh
