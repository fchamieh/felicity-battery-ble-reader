#!/bin/sh
# build-mac.sh — build the fsolar CLI for macOS (Apple Silicon + Intel).
#
# Usage (on a Mac):
#   cd go-fsolar
#   sh build-mac.sh        # or: chmod +x build-mac.sh && ./build-mac.sh
#
# The macOS BLE driver (tinygo.org/x/bluetooth -> tinygo-org/cbgo) is cgo +
# Objective-C and links CoreBluetooth, so it can ONLY be built on macOS.
# The -via tcp path is platform-independent.

set -eu

# Always run from the directory containing this script (the go-fsolar module).
cd "$(dirname "$0")"

if ! command -v go >/dev/null 2>&1; then
    echo "error: Go not found. Install from https://go.dev/dl/  (or: brew install go)" >&2
    exit 1
fi

if ! command -v clang >/dev/null 2>&1; then
    echo "error: clang not found. Install Xcode Command Line Tools:" >&2
    echo "  xcode-select --install" >&2
    exit 1
fi

echo "==> $(go version)"
echo "==> $(clang --version | head -n1)"

mkdir -p bin
LDFLAGS="-s -w"

build() {
    arch="$1"
    out="bin/fsolar-darwin-${arch}"
    echo "==> building ${out}"
    GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 \
        go build -trimpath -ldflags "$LDFLAGS" -o "$out" ./cmd/fsolar
}

# Apple Silicon
build arm64
# Intel — cross-compiled in-process on an arm64 Mac (Xcode's clang supports
# both slices). Drop this call if you only need arm64.
build amd64

# Universal binary (runs on both; needs lipo, which ships with the CLT).
if command -v lipo >/dev/null 2>&1; then
    echo "==> creating universal binary bin/fsolar-darwin"
    lipo -create -output bin/fsolar-darwin bin/fsolar-darwin-arm64 bin/fsolar-darwin-amd64
fi

echo
echo "Done. Binaries in bin/:"
ls -lh bin/fsolar-darwin*
