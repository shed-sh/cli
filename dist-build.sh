#!/bin/sh
# Build the shed binary for whatever target dist asked for.
#
# dist speaks in Rust target triples because that is what it was built around;
# Go speaks GOOS/GOARCH. This script is the translation layer, and it is the
# only place that mapping lives.
#
# dist runs this once per target and expects the binary to land in the
# directory named by `out-dir` (the project root by default), under the name
# declared in `binaries`.
set -eu

target="${CARGO_DIST_TARGET:?CARGO_DIST_TARGET is not set; dist normally provides it}"

case "$target" in
aarch64-apple-darwin) export GOOS=darwin GOARCH=arm64 ;;
x86_64-apple-darwin) export GOOS=darwin GOARCH=amd64 ;;
aarch64-unknown-linux-gnu | aarch64-unknown-linux-musl) export GOOS=linux GOARCH=arm64 ;;
x86_64-unknown-linux-gnu | x86_64-unknown-linux-musl) export GOOS=linux GOARCH=amd64 ;;
*)
	echo "dist-build.sh: unsupported target $target" >&2
	exit 1
	;;
esac

# Static by construction. This is why one linux build covers both glibc and
# musl: nothing is resolved from the host at runtime, so the same binary runs
# on Debian and on Alpine. Turning CGO on would break that and would mean
# shipping separate -gnu and -musl artifacts.
export CGO_ENABLED=0

# The version dist is releasing. It reads its own from dist-workspace.toml, so
# taking it from the same file keeps the binary's `shed version` from drifting
# away from the tag it was published under.
version="${CARGO_DIST_VERSION:-}"
if [ -z "$version" ]; then
	version="$(sed -n 's/^version[[:space:]]*=[[:space:]]*"\(.*\)"/\1/p' dist-workspace.toml | head -1)"
fi
[ -n "$version" ] || {
	echo "dist-build.sh: could not determine the version to build" >&2
	exit 1
}

echo "building shed $version for $target ($GOOS/$GOARCH)"
go build -trimpath -ldflags "-s -w -X main.version=${version}" -o shed ./cmd/shed
