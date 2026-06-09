#!/bin/bash
set -e

if [ -z "$1" ]; then
  echo "Usage: ./build.sh <version>"
  echo "Example: ./build.sh v0.0.2"
  exit 1
fi

VERSION="$1"
COMMIT=$(git rev-parse --short HEAD)
DATE=$(date +%Y-%m-%d)

echo "Building $VERSION (commit=$COMMIT, date=$DATE)"

go build -ldflags \
  "-X agr/version.Version=${VERSION} \
   -X agr/version.Commit=${COMMIT} \
   -X agr/version.Date=${DATE}" \
  -o agr .
