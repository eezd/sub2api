#!/usr/bin/env bash
# Compile and package exactly the six release targets from an already embedded frontend.
set -euo pipefail
(( $# == 1 )) || { printf 'Usage: bash %s OUTPUT_DIR\n' "$0" >&2; exit 2; }
output=$(realpath -e -- "$1")
: "${VERSION:?}" "${SOURCE_SHA:?}" "${BUILT_AT:?}"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
    os=${target%/*}
    arch=${target#*/}
    name="sub2api_${VERSION}_${os}_${arch}"
    suffix=''; [[ $os != windows ]] || suffix=.exe
    package=$(mktemp -d)
    mkdir "$package/$name"
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go -C backend build -tags embed -trimpath \
        -ldflags="-s -w -X main.Version=$VERSION -X main.Commit=$SOURCE_SHA -X main.Date=$BUILT_AT -X main.BuildType=release" \
        -o "$package/$name/sub2api$suffix" ./cmd/server
    cp -R LICENSE LICENSES THIRD_PARTY_NOTICES.md backend/resources "$package/$name/"
    if [[ $os == windows ]]; then
        (cd "$package" && python3 -m zipfile -c "$output/$name.zip" "$name")
    else
        tar -czf "$output/$name.tar.gz" -C "$package" "$name"
    fi
    if [[ $os == linux ]]; then
        cp "$package/$name/sub2api" "$output/sub2api-linux-$arch"
    fi
    printf 'Packaged %s\n' "$name"
done
