#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

version="${HUI_POC_VERSION:-v2.13.0-hui.2}"
commit="$(git rev-parse HEAD)"
build_date="${SOURCE_DATE_EPOCH:-0}"
mkdir -p dist

ldflags=(
  -s -w
  -X "github.com/apernet/hysteria/app/v2/cmd.appVersion=${version}"
  -X "github.com/apernet/hysteria/app/v2/cmd.appDate=${build_date}"
  -X "github.com/apernet/hysteria/app/v2/cmd.appType=h-ui-poc"
  -X "github.com/apernet/hysteria/app/v2/cmd.appCommit=${commit}"
  -X "github.com/apernet/hysteria/app/v2/cmd.appPlatform=linux"
  -X "github.com/apernet/hysteria/app/v2/cmd.appArch=amd64"
)

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -buildvcs=false -trimpath -ldflags "${ldflags[*]}" \
  -o "dist/hysteria-linux-amd64-${version}" ./app

(
  cd dist
  sha256sum "hysteria-linux-amd64-${version}" > "SHA256SUMS-hui-poc"
)
