#!/bin/sh
set -eu
cd "$(dirname "$0")"
output=$(mktemp -d "${TMPDIR:-/tmp}/meldnet-ios-tests.XXXXXX")
trap 'rm -rf "$output"' EXIT
xcrun swiftc Shared/Store.swift Shared/NetworkValidation.swift Tests/main.swift -o "$output/native-tests"
"$output/native-tests"
