#!/usr/bin/env bash
set -euo pipefail

IMAGE=${1:?usage: ./build_benzhi_docker.sh IMAGE PLATFORM}
PLATFORM=${2:?usage: ./build_benzhi_docker.sh IMAGE PLATFORM}
BUILDER="${BUILDER:-default}"

docker buildx build \
  --builder "${BUILDER}" \
  --platform "$PLATFORM" \
  --load \
  -f benzhi.Dockerfile \
  -t "$IMAGE" \
  .
