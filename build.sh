#!/usr/bin/env bash
# Cross-Compile von qdrantcli für alle Zielplattformen.
set -euo pipefail
cd "$(dirname "$0")"
VERSION="${1:-${VERSION:-0.1.0}}"
TARGETS="windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"
rm -rf dist && mkdir -p dist
for t in $TARGETS; do
  os="${t%/*}"; arch="${t#*/}"
  name="qdrantcli_${VERSION}_${os}_${arch}"
  stage="dist/.stage/$name"; mkdir -p "$stage"
  bin=qdrantcli; [ "$os" = windows ] && bin=qdrantcli.exe
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" -o "$stage/$bin" .
  cp README.md LICENSE "$stage/"
  if [ "$os" = windows ]; then
    (cd "$stage" && zip -q -X "../../$name.zip" "$bin" README.md LICENSE)
  else
    tar -C "$stage" -czf "dist/$name.tar.gz" "$bin" README.md LICENSE
  fi
  echo "gebaut: $name"
done
rm -rf dist/.stage
(cd dist && sha256sum qdrantcli_* > SHA256SUMS)
echo "fertig: dist/ (Version $VERSION)"
