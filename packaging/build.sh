#!/bin/sh
# Builds the release binaries and .deb packages into dist/, plus SHA256SUMS.
#
# Usage: packaging/build.sh <version>    (Debian version, e.g. 0.4.0)
# Needs Go and nfpm (https://nfpm.goreleaser.com) on PATH.
set -eu

version=$1
cd "$(dirname "$0")/.."
# The GitHub release this build is or builds on, for the web interface.
release=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || echo "v$version")
release=${release#v}
rm -rf dist
mkdir -p dist

# build <GOARCH> <GOARM> <asset suffix> <Debian architecture>
build() {
	echo "== linux/$3 ($4)"
	CGO_ENABLED=0 GOOS=linux GOARCH=$1 GOARM=$2 go build -trimpath \
		-ldflags "-s -w -X github.com/buxtronix/phev2mqtt/cmd.Version=$version -X github.com/buxtronix/phev2mqtt/cmd.Release=$release" \
		-o "dist/phev2mqtt-linux-$3" .
	# nfpm does not expand variables in contents, so use a fixed path.
	cp "dist/phev2mqtt-linux-$3" dist/phev2mqtt
	DEB_ARCH=$4 VERSION=$version \
		nfpm package --config packaging/nfpm.yaml --packager deb --target dist/
	rm dist/phev2mqtt
}

build amd64 "" amd64 amd64
build arm64 "" arm64 arm64
# ARMv6 code also runs on ARMv7, so this one package covers every 32-bit Pi.
build arm 6 armv6 armhf
build 386 "" 386 i386

cd dist
sha256sum phev2mqtt-linux-* ./*.deb | sed 's# \./# #' > SHA256SUMS
cat SHA256SUMS
