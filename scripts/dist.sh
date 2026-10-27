#!/usr/bin/env bash
# Builds the release archive for one platform into dist/.
#
#   VERSION=v1.2.3 GOOS=linux GOARCH=arm64 scripts/dist.sh
#   VERSION=v1.2.3 GOOS=linux GOARCH=arm GOARM=7 scripts/dist.sh
#   VERSION=v1.2.3 scripts/dist.sh            # macOS, on a Mac of that architecture
#
# Linux builds are static and need no cgo; they use the serial transport
# (/dev/ttyUSB*). macOS builds need cgo and libusb (brew install libusb
# pkg-config), which is linked in statically so the binary runs without
# Homebrew.
set -euo pipefail
cd "$(dirname "$0")/.."

: "${VERSION:?set VERSION, e.g. v1.2.3}"
GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}
arch=$GOARCH
[ "$GOARCH" = arm ] && arch=armv${GOARM:-7}
name=haobot-agent-$VERSION-$GOOS-$arch
out=dist/$name
rm -rf "$out" && mkdir -p "$out"

ldflags="-s -w -X main.version=$VERSION"
case $GOOS in
linux)
	CGO_ENABLED=0 GOOS=linux GOARCH=$GOARCH GOARM=${GOARM:-} \
		go build -trimpath -ldflags "$ldflags" -o "$out/haobot-agent" ./cmd/haobot-agent
	;;
darwin)
	if [ "$GOARCH" != "$(go env GOHOSTARCH)" ] || [ "$(go env GOHOSTOS)" != darwin ]; then
		echo "macOS builds need a Mac of the same architecture (libusb is native code)" >&2
		exit 1
	fi
	# A pkg-config file that names the static libusb archive instead of the
	# dylib, plus the system frameworks libusb uses.
	prefix=$(brew --prefix libusb)
	pc=$(mktemp -d)
	trap 'rm -rf "$pc"' EXIT
	cat >"$pc/libusb-1.0.pc" <<PC
Name: libusb-1.0
Description: libusb, linked statically
Version: 1.0
Cflags: -I$prefix/include/libusb-1.0
Libs: $prefix/lib/libusb-1.0.a -framework IOKit -framework CoreFoundation -framework Security -lobjc
PC
	# -a: cgo results are cached without the pkg-config flags, which differ
	# from a normal build's.
	CGO_ENABLED=1 PKG_CONFIG_PATH=$pc \
		go build -a -trimpath -ldflags "$ldflags" -o "$out/haobot-agent" ./cmd/haobot-agent
	if otool -L "$out/haobot-agent" | grep -q libusb; then
		echo "libusb is still linked dynamically" >&2
		exit 1
	fi
	;;
*)
	echo "unsupported GOOS $GOOS" >&2
	exit 1
	;;
esac
cp LICENSE README.md README.en.md "$out/"
tar -C dist -czf "dist/$name.tar.gz" "$name"
rm -rf "$out"
echo "dist/$name.tar.gz"
