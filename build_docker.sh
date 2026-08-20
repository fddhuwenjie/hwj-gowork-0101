#!/usr/bin/env bash
set -euo pipefail

image_name="${1:-hwj-gowork-0101:local}"
platform="${2:-linux/amd64}"
BUILDER="${BUILDER:-default}"

docker buildx build \
  --builder "${BUILDER}" \
  --platform "${platform}" \
  --load \
  -f Dockerfile \
  --tag "${image_name}" \
  .
