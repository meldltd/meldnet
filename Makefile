.DEFAULT_GOAL := build

.PHONY: build test check cross demo integration macos-app macos-test macos-installer macos-installer-test linux-gui linux-gui-test windows-package windows-gui windows-installer windows-installer-test mobile-native mobile-check mobile-android mobile-ios mobile-ios-simulator mobile-ios-test

mobile-native:
	python3 scripts/build-mobile-native.py

mobile-check:
	cd mobile && flutter pub get && flutter analyze && flutter test
	cd mobile/native && go vet ./... && go test -race ./...

mobile-android: mobile-native
	cd mobile && flutter build apk --debug --target-platform android-arm64,android-x64
	mkdir -p bin
	cp mobile/build/app/outputs/flutter-apk/app-debug.apk bin/Meldnet-android-debug.apk

mobile-ios:
	./mobile/ios/bootstrap-wireguard.sh
	cd mobile && flutter pub get && flutter build ios --release --no-codesign
	mkdir -p bin
	ditto -c -k --keepParent mobile/build/ios/iphoneos/Runner.app bin/Meldnet-ios-unsigned.zip

mobile-ios-test:
	./mobile/ios/test-native.sh
	./mobile/ios/test-https.sh

mobile-ios-simulator:
	./mobile/ios/bootstrap-wireguard.sh
	cd mobile && flutter pub get && flutter build ios --simulator --debug --no-codesign

linux-gui:
	python3 scripts/build-linux-gui.py

linux-gui-test:
	python3 -m unittest discover -s linux/tests -v

windows-gui:
	@set -eu; for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build -trimpath -ldflags="-H=windowsgui" -o bin/windows-$$arch/meldnet-gui.exe ./cmd/meldnet-gui; \
	done

windows-package: cross
	python3 scripts/build-windows-package.py

windows-installer: windows-package
	python3 scripts/build-windows-installer.py

windows-installer-test:
	python3 -m unittest discover -s windows/tests -v

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
	@if go list -deps ./cmd/meldnetd | grep -Eq '^(charm\.land/|meldnet/internal/(desktop|tui)$$)'; then echo 'Daemon must not depend on UI packages'; exit 1; fi
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
	$(MAKE) windows-gui

demo: build
	./bin/meldnetd --simulate --data-dir "$(CURDIR)/.demo/state" --socket "$(CURDIR)/.demo/control.sock"

integration: cross
	./scripts/integration-linux.sh
	./scripts/integration-primary.sh
	./scripts/integration-multinetwork.sh
	./scripts/test-dns-linux.sh
