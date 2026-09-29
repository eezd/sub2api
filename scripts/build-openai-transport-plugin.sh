#!/usr/bin/env bash
set -euo pipefail

VERSION=""
HOST_VERSION=""
OUTPUT=""

usage() {
  cat <<'EOF'
Usage: build-openai-transport-plugin.sh --version VERSION --host-version VERSION --output FILE

Requires SUB2API_PLUGIN_SIGNING_KEY as PKCS#8 Ed25519 PEM or Base64 raw seed/private key.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION=${2:-}
      shift 2
      ;;
    --host-version)
      HOST_VERSION=${2:-}
      shift 2
      ;;
    --output)
      OUTPUT=${2:-}
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      printf 'error: unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

[[ -n "$VERSION" && -n "$HOST_VERSION" && -n "$OUTPUT" ]] || {
  usage >&2
  exit 2
}
[[ -n "${SUB2API_PLUGIN_SIGNING_KEY:-}" ]] || {
  printf 'error: SUB2API_PLUGIN_SIGNING_KEY is required\n' >&2
  exit 1
}

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
if [[ "$OUTPUT" != /* ]]; then
  OUTPUT="$ROOT_DIR/$OUTPUT"
fi
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT
mkdir -p "$BUILD_DIR/runtimes" "$BUILD_DIR/ui"
cp -a "$ROOT_DIR/plugins/openai-transport/ui/." "$BUILD_DIR/ui/"

TARGETS=(
  linux-amd64
  linux-arm64
  darwin-amd64
  darwin-arm64
  windows-amd64
)

for target in "${TARGETS[@]}"; do
  GOOS=${target%-*}
  GOARCH=${target#*-}
  BINARY=openai-transport-plugin
  if [[ "$GOOS" == "windows" ]]; then
    BINARY+='.exe'
  fi
  mkdir -p "$BUILD_DIR/runtimes/$target"
  (
    cd "$ROOT_DIR/backend"
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" \
      go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X=main.version=${VERSION#v}" \
      -o "$BUILD_DIR/runtimes/$target/$BINARY" \
      ./cmd/openai-transport-plugin
  )
done

mkdir -p "$(dirname "$OUTPUT")"
(
  cd "$ROOT_DIR/backend"
  go run ./cmd/s2plugin-packager \
    --input "$BUILD_DIR" \
    --output "$OUTPUT" \
    --version "${VERSION#v}" \
    --host-version "${HOST_VERSION#v}"
)
printf 'Built signed plugin: %s\n' "$OUTPUT"
