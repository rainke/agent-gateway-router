#!/bin/bash
set -e

VERSION="${1:-v0.1.0-alpha}"
COMMIT=$(git rev-parse --short HEAD)
DATE=$(date +%Y-%m-%d)

GOPATH_BIN="$(go env GOPATH)/bin"
AGR_BIN="${GOPATH_BIN}/agr"

echo "Building $VERSION (commit=$COMMIT, date=$DATE)"

if [ -x "$AGR_BIN" ]; then
  "$AGR_BIN" stop || true
fi

go install -ldflags \
  "-X agr/version.Version=${VERSION} \
   -X agr/version.Commit=${COMMIT} \
   -X agr/version.Date=${DATE}" \
  .

"$AGR_BIN" start -d
