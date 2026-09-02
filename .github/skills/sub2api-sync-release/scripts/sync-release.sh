#!/usr/bin/env bash

set -euo pipefail

MODE="plan"
REPO_DIR=""

usage() {
  cat <<'EOF'
Usage:
  sync-release.sh --plan [--repo-dir PATH]
  sync-release.sh --execute [--repo-dir PATH]
  sync-release.sh --self-test

--plan      Fetch remote refs and print the prospective synchronization/release.
--execute   Synchronize main, push it, publish the next release, and verify it.
--self-test Run deterministic version-suffix tests without network access.
EOF
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

max_eezd_suffix() {
  local base_version=$1
  shift
  local prefix="v${base_version}-eezd."
  local max=0
  local tag suffix number

  for tag in "$@"; do
    [[ "$tag" == "${prefix}"* ]] || continue
    suffix=${tag#"$prefix"}
    [[ -n "$suffix" && "$suffix" != *[!0-9]* ]] || continue
    number=$((10#$suffix))
    if (( number > max )); then
      max=$number
    fi
  done

  printf '%s\n' "$max"
}

validate_base_version() {
  local version=$1
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "invalid upstream VERSION: $version"
}

run_self_test() {
  local actual

  actual=$(max_eezd_suffix "0.1.185" \
    "v0.1.185-eezd.1" \
    "v0.1.185-eezd.9" \
    "v0.1.185-eezd.02" \
    "v0.1.185-eezd.bad" \
    "v0.1.186-eezd.20")
  [[ "$actual" == "9" ]] || fail "suffix test failed: expected 9, got $actual"

  actual=$(max_eezd_suffix "1.2.3")
  [[ "$actual" == "0" ]] || fail "empty suffix test failed: expected 0, got $actual"

  validate_base_version "0.1.185"
  printf 'self-test passed\n'
}

while (( $# > 0 )); do
  case "$1" in
    --plan)
      MODE="plan"
      ;;
    --execute)
      MODE="execute"
      ;;
    --repo-dir)
      shift
      (( $# > 0 )) || fail "--repo-dir requires a path"
      REPO_DIR=$1
      ;;
    --self-test)
      run_self_test
      exit 0
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      fail "unknown argument: $1"
      ;;
  esac
  shift
done

require_command git
require_command gh

if [[ -n "$REPO_DIR" ]]; then
  cd "$REPO_DIR"
else
  REPO_DIR=$(git rev-parse --show-toplevel 2>/dev/null) || fail "not inside a Git repository"
  cd "$REPO_DIR"
fi

REPO_DIR=$(git rev-parse --show-toplevel)
cd "$REPO_DIR"

[[ $(git branch --show-current) == "main" ]] || fail "current branch must be main"
[[ -z $(git status --porcelain) ]] || fail "worktree must be clean before synchronization"

gh auth status >/dev/null 2>&1 || fail "GitHub CLI is not authenticated"

ORIGIN_REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner)
[[ -n "$ORIGIN_REPO" ]] || fail "cannot determine origin GitHub repository"

PARENT_REPO=$(gh api "repos/${ORIGIN_REPO}" --jq '.parent.full_name // empty')
[[ -n "$PARENT_REPO" ]] || fail "${ORIGIN_REPO} is not a GitHub fork or has no discoverable parent"

DEFAULT_BRANCH=$(gh api "repos/${ORIGIN_REPO}" --jq .default_branch)
[[ "$DEFAULT_BRANCH" == "main" ]] || fail "fork default branch must be main, got $DEFAULT_BRANCH"

UPSTREAM_URL="https://github.com/${PARENT_REPO}.git"

git fetch origin "refs/heads/main:refs/remotes/origin/main" --tags --prune

git fetch "$UPSTREAM_URL" refs/heads/main
UPSTREAM_HEAD=$(git rev-parse 'FETCH_HEAD^{commit}')
ORIGIN_HEAD=$(git rev-parse 'refs/remotes/origin/main^{commit}')
LOCAL_HEAD=$(git rev-parse 'HEAD^{commit}')

if git merge-base --is-ancestor "$LOCAL_HEAD" "$ORIGIN_HEAD"; then
  ORIGIN_RELATION="local main can fast-forward to origin/main"
elif git merge-base --is-ancestor "$ORIGIN_HEAD" "$LOCAL_HEAD"; then
  ORIGIN_RELATION="local main contains origin/main"
else
  fail "local main and origin/main have diverged; reconcile them manually"
fi

UPSTREAM_RELEASE_TAG=$(gh api "repos/${PARENT_REPO}/releases/latest" --jq .tag_name)
[[ "$UPSTREAM_RELEASE_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "invalid upstream release tag: $UPSTREAM_RELEASE_TAG"
UPSTREAM_VERSION=${UPSTREAM_RELEASE_TAG#v}
validate_base_version "$UPSTREAM_VERSION"

mapfile -t VERSION_TAGS < <(git tag --list "v${UPSTREAM_VERSION}-eezd.*")
MAX_SUFFIX=$(max_eezd_suffix "$UPSTREAM_VERSION" "${VERSION_TAGS[@]}")
NEXT_TAG="v${UPSTREAM_VERSION}-eezd.$((MAX_SUFFIX + 1))"
PLAN_RELEASE=$NEXT_TAG

if (( MAX_SUFFIX > 0 )); then
  PLAN_LATEST_TAG="v${UPSTREAM_VERSION}-eezd.${MAX_SUFFIX}"
  PLAN_HEAD=""
  if git merge-base --is-ancestor "$UPSTREAM_HEAD" "$LOCAL_HEAD" && git merge-base --is-ancestor "$ORIGIN_HEAD" "$LOCAL_HEAD"; then
    PLAN_HEAD=$LOCAL_HEAD
  elif git merge-base --is-ancestor "$UPSTREAM_HEAD" "$ORIGIN_HEAD" && git merge-base --is-ancestor "$LOCAL_HEAD" "$ORIGIN_HEAD"; then
    PLAN_HEAD=$ORIGIN_HEAD
  fi

  if [[ -n "$PLAN_HEAD" && $(git rev-list -n 1 "$PLAN_LATEST_TAG") == "$PLAN_HEAD" ]] && gh release view "$PLAN_LATEST_TAG" --repo "$ORIGIN_REPO" >/dev/null 2>&1; then
    PLAN_RELEASE="none; ${PLAN_LATEST_TAG} already releases the synchronized HEAD"
  fi
fi

printf 'Fork:             %s\n' "$ORIGIN_REPO"
printf 'Upstream:         %s\n' "$PARENT_REPO"
printf 'Local HEAD:       %s\n' "$LOCAL_HEAD"
printf 'Origin HEAD:      %s\n' "$ORIGIN_HEAD"
printf 'Upstream HEAD:    %s\n' "$UPSTREAM_HEAD"
printf 'Origin relation:  %s\n' "$ORIGIN_RELATION"
printf 'Upstream version: %s\n' "$UPSTREAM_VERSION"
printf 'Planned release:  %s\n' "$PLAN_RELEASE"

if [[ "$MODE" == "plan" ]]; then
  printf 'Plan only: no merge, push, tag, or release was performed.\n'
  exit 0
fi

if git merge-base --is-ancestor HEAD refs/remotes/origin/main; then
  git merge --ff-only refs/remotes/origin/main
fi

if ! git merge --no-edit "$UPSTREAM_HEAD"; then
  if git rev-parse -q --verify MERGE_HEAD >/dev/null; then
    git merge --abort
  fi
  fail "upstream merge conflicted and was aborted"
fi

WORKFLOW_CONTENT=$(<.github/workflows/release.yml)
RELEASER_CONTENT=$(<.goreleaser.yaml)

[[ "$WORKFLOW_CONTENT" == *'DOCKERHUB_USERNAME: skip'* ]] || fail "release contract lost: Docker Hub is not disabled"
[[ "$WORKFLOW_CONTENT" != *'sync-version-file:'* ]] || fail "release contract lost: VERSION would be committed back to main"
[[ "$RELEASER_CONTENT" == *'use: github-native'* ]] || fail "release contract lost: GitHub-native notes are disabled"
[[ "$RELEASER_CONTENT" == *'prerelease: false'* ]] || fail "release contract lost: releases are not forced stable"
[[ "$RELEASER_CONTENT" == *'make_latest: true'* ]] || fail "release contract lost: releases are not marked latest"
[[ "$RELEASER_CONTENT" != *'ghcr.io/{{ .Env.GITHUB_REPO_OWNER_LOWER }}/sub2api:latest'* ]] || fail "release contract lost: GHCR latest tag is enabled"

BASE_VERSION=$UPSTREAM_VERSION

mapfile -t VERSION_TAGS < <(git tag --list "v${BASE_VERSION}-eezd.*")
MAX_SUFFIX=$(max_eezd_suffix "$BASE_VERSION" "${VERSION_TAGS[@]}")
LATEST_TAG=""
if (( MAX_SUFFIX > 0 )); then
  LATEST_TAG="v${BASE_VERSION}-eezd.${MAX_SUFFIX}"
fi

CURRENT_HEAD=$(git rev-parse 'HEAD^{commit}')
RELEASE_TAG=""

if [[ -n "$LATEST_TAG" && $(git rev-list -n 1 "$LATEST_TAG") == "$CURRENT_HEAD" ]]; then
  if gh release view "$LATEST_TAG" --repo "$ORIGIN_REPO" >/dev/null 2>&1; then
    printf 'Already released: %s points at current HEAD.\n' "$LATEST_TAG"
    exit 0
  fi
  RELEASE_TAG=$LATEST_TAG
  printf 'Reusing unreleased tag at current HEAD: %s\n' "$RELEASE_TAG"
else
  RELEASE_TAG="v${BASE_VERSION}-eezd.$((MAX_SUFFIX + 1))"
fi

if [[ $(git rev-parse 'HEAD^{commit}') != $(git rev-parse 'refs/remotes/origin/main^{commit}') ]]; then
  git push origin main
fi

if git rev-parse -q --verify "refs/tags/${RELEASE_TAG}" >/dev/null; then
  [[ $(git rev-list -n 1 "$RELEASE_TAG") == "$CURRENT_HEAD" ]] || fail "existing tag ${RELEASE_TAG} points at another commit"
else
  git tag -a "$RELEASE_TAG" -m "$RELEASE_TAG"
fi

TAG_WAS_PUSHED=false
if ! git ls-remote --exit-code --tags origin "refs/tags/${RELEASE_TAG}" >/dev/null 2>&1; then
  git push origin "refs/tags/${RELEASE_TAG}"
  TAG_WAS_PUSHED=true
fi

RUN_ID=""
if [[ "$TAG_WAS_PUSHED" == "true" ]]; then
  for _ in 1 2 3 4 5 6; do
    sleep 5
    RUN_ID=$(gh run list \
      --repo "$ORIGIN_REPO" \
      --workflow release.yml \
      --event push \
      --branch "$RELEASE_TAG" \
      --limit 1 \
      --json databaseId \
      --jq '.[0].databaseId // empty')
    [[ -z "$RUN_ID" ]] || break
  done
fi

if [[ -z "$RUN_ID" ]]; then
  printf 'No new tag-triggered run found; using workflow_dispatch fallback.\n'
  DISPATCHED_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  gh workflow run release.yml \
    --repo "$ORIGIN_REPO" \
    --ref main \
    -f "tag=${RELEASE_TAG}" \
    -f simple_release=false

  for _ in 1 2 3 4 5 6; do
    sleep 5
    RUN_ID=$(gh run list \
      --repo "$ORIGIN_REPO" \
      --workflow release.yml \
      --event workflow_dispatch \
      --commit "$CURRENT_HEAD" \
      --limit 10 \
      --json databaseId,createdAt \
      --jq "map(select(.createdAt >= \"${DISPATCHED_AT}\"))[0].databaseId // empty")
    [[ -z "$RUN_ID" ]] || break
  done
fi

[[ -n "$RUN_ID" ]] || fail "release workflow was not registered"

RUN_URL=$(gh run view "$RUN_ID" --repo "$ORIGIN_REPO" --json url --jq .url)
printf 'Watching workflow: %s\n' "$RUN_URL"
gh run watch "$RUN_ID" --repo "$ORIGIN_REPO" --exit-status --interval 10

RELEASE_STATE=$(gh release view "$RELEASE_TAG" \
  --repo "$ORIGIN_REPO" \
  --json tagName,isDraft,isPrerelease,url \
  --jq '[.tagName, (.isDraft|tostring), (.isPrerelease|tostring), .url] | @tsv')
IFS=$'\t' read -r VERIFIED_TAG IS_DRAFT IS_PRERELEASE RELEASE_URL <<<"$RELEASE_STATE"

[[ "$VERIFIED_TAG" == "$RELEASE_TAG" ]] || fail "published release tag mismatch"
[[ "$IS_DRAFT" == "false" ]] || fail "published release is still a draft"
[[ "$IS_PRERELEASE" == "false" ]] || fail "published release is marked prerelease"

IMAGE="ghcr.io/${ORIGIN_REPO,,}:${RELEASE_TAG#v}"

if command -v docker >/dev/null 2>&1 && docker buildx version >/dev/null 2>&1; then
  docker buildx imagetools inspect "$IMAGE"
  IMAGE_VERIFICATION="verified"
else
  IMAGE_VERIFICATION="skipped: docker buildx is unavailable"
fi

printf '\nRelease complete\n'
printf 'Tag:        %s\n' "$RELEASE_TAG"
printf 'Workflow:   %s\n' "$RUN_URL"
printf 'Release:    %s\n' "$RELEASE_URL"
printf 'Image:      %s\n' "$IMAGE"
printf 'GHCR check: %s\n' "$IMAGE_VERIFICATION"
