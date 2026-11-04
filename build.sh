#!/bin/sh
set -eu
cd "$(dirname "$0")"

VERSION="${VERSION:-1.0.0}"
mkdir -p dist

echo "==> 编译 linux/amd64"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" \
    -o dist/clipbox-linux-amd64 .

echo "==> 编译 linux/arm64"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" \
    -o dist/clipbox-linux-arm64 .

cd dist
if command -v sha256sum >/dev/null 2>&1; then
    sha256sum clipbox-linux-amd64 clipbox-linux-arm64 > checksums.txt
else
    shasum -a 256 clipbox-linux-amd64 clipbox-linux-arm64 > checksums.txt
fi

echo "==> 完成 ($VERSION)"
ls -lh clipbox-linux-amd64 clipbox-linux-arm64 checksums.txt
