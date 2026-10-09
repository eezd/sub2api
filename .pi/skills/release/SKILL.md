---
name: release
description: Release the eezd/sub2api fork through its existing tag-driven GitHub Actions pipeline. Use when the user asks to publish, cut, tag, preview, verify, or recover a fork release using the vX.Y.Z-custom.N version line, including choosing the next custom revision, preparing annotated tag notes, pushing the release tag, and checking the Release workflow.
---

# Release eezd/sub2api

Release this repository as a maintained fork of `Wei-Shaw/sub2api`.

Preserve the repository's established version model:

- Keep `backend/cmd/server/VERSION` on the upstream/base version such as `0.2.11`.
- Publish fork releases as `vX.Y.Z-custom.N`.
- Reset to `custom.1` when the base `X.Y.Z` changes.
- Increment `N` for additional fork releases on the same base version.
- Never rewrite `VERSION` to `X.Y.Z-custom.N` just to cut a release. The release workflow derives the full artifact version from the pushed tag.
- Never create or move a `latest` tag unless the user explicitly requests a repository policy change.

A pushed `v*` tag triggers `.github/workflows/release.yml`. Normal tag releases are full releases; `simple_release` is only for explicit manual workflow dispatches.

## Workflow

### 1. Preflight the repository

Run from the repository root and inspect before changing anything:

```bash
git rev-parse --show-toplevel
git status --porcelain=v1
git branch --show-current
git remote -v
git log -1 --oneline --decorate
```

Require all of the following before publishing:

- Repository is the `eezd/sub2api` fork.
- Current branch is `main`, unless the user explicitly approves another ref.
- Working tree is clean. Do not tag a commit while intended release changes are only uncommitted.
- No unresolved merge or rebase is in progress.

Expected remotes are:

- `origin` -> `eezd/sub2api`
- `upstream` -> `Wei-Shaw/sub2api`

Do not silently rewrite remotes if they differ. Explain the mismatch first.

Refresh remote state:

```bash
git fetch origin --tags --prune
git fetch upstream --prune
```

Do not merge upstream as part of the release workflow unless the user explicitly asked to synchronize upstream first. Upstream synchronization is a separate operation.

### 2. Determine the next fork version

Read the base version:

```bash
BASE_VERSION="$(tr -d '\r\n[:space:]' < backend/cmd/server/VERSION)"
```

Validate that it is an upstream/base version and not already a `custom.N` release string.

Inspect existing tags for this base:

```bash
git tag -l "v${BASE_VERSION}-custom.*" --sort=-version:refname
```

Derive the next tag:

- No matching tag -> `v${BASE_VERSION}-custom.1`
- Existing highest `custom.N` -> `v${BASE_VERSION}-custom.$((N+1))`

Also identify the previous fork release across all bases:

```bash
PREVIOUS_TAG="$(git tag -l 'v*-custom.*' --sort=-version:refname | head -n 1)"
```

If the user supplied an explicit release tag, validate it against this scheme and against the current base version. Do not reuse an existing local or remote tag.

### 3. Review exactly what will ship

Inspect the commits and diff since the previous fork release:

```bash
git log --oneline --decorate "${PREVIOUS_TAG}..HEAD"
git diff --stat "${PREVIOUS_TAG}..HEAD"
git diff --check "${PREVIOUS_TAG}..HEAD"
```

If there is no previous fork tag, review the current release range manually.

Classify changes into:

- upstream synchronization / upstream version movement;
- fork-specific features;
- bug fixes and compatibility fixes;
- release/build/security changes.

Do not invent release notes from filenames alone. Read important commits or diffs when their intent is unclear.

### 4. Prepare annotated tag notes

Write concise Chinese release notes from the actual release range. Prefer 3-8 high-signal bullets.

When relevant, call out:

- the upstream version now incorporated;
- meaningful fork-specific behavior added or retained;
- important compatibility, gateway, plugin, security, or deployment fixes;
- user-visible behavior changes.

Do not dump every commit.

Create an annotated tag whose first line is the tag subject and whose body is the release note text. The workflow reads the annotated tag body for notifications.

Example tag message file:

```text
Sub2API 0.2.11-custom.1

- 同步上游 Sub2API 0.2.11，并保留 fork 自定义功能。
- 修复 ...
- 改进 ...
```

Create it with a temporary file rather than fragile shell quoting:

```bash
git tag -a "$NEXT_TAG" -F "$TAG_MESSAGE_FILE"
```

Do not push the tag yet.

### 5. Validate before remote publication

Before the irreversible publication trigger, show the user:

- current branch and HEAD SHA;
- previous release tag;
- proposed new tag;
- commits included;
- final tag notes;
- whether local `main` is ahead of, behind, or equal to `origin/main`;
- tests/checks already run and their result.

Run the narrowest meaningful validation for the changed areas when practical. At minimum run `git diff --check` and ensure the tree remains clean.

If the user asked for preview/dry-run only, stop here and make no remote changes.

Unless the user explicitly said to publish without another confirmation, ask once before pushing anything that triggers publication.

### 6. Publish main and the release tag

The release commit must be reachable from the fork's `main` branch. If local `main` is ahead of `origin/main`, push `main` first:

```bash
git push origin main
```

Verify `origin/main` resolves to the intended release commit, then push only the new tag:

```bash
git push origin "$NEXT_TAG"
```

Never use `--force` for release tags.

Pushing the `v*` tag starts the repository's Release workflow. Do not manually create a duplicate GitHub Release while that workflow is responsible for publication.

### 7. Monitor and verify the release

If GitHub CLI is available and authenticated, identify the workflow run for the new tag and watch it to completion:

```bash
gh run list --workflow release.yml --limit 10
gh run watch <run-id> --exit-status
```

After success, verify the GitHub Release:

```bash
gh release view "$NEXT_TAG"
```

Check that the expected full release artifacts exist, especially:

- platform archives;
- `checksums.txt`;
- the signed `sub2api-openai-transport-<version>.s2plugin` asset for a normal full release;
- the full-version GHCR image `ghcr.io/eezd/sub2api:<version>`.

Report the release URL, tag, commit SHA, and workflow result.

### 8. Handle failures safely

If the workflow fails after the tag is pushed:

1. Inspect the failed job and logs.
2. Fix the problem on `main`.
3. Prefer cutting the next `custom.N` release rather than moving an already-pushed tag.

Never force-update a published release tag.

Do not delete a remote tag or GitHub Release without explicit user approval. If publication already succeeded or users may have consumed artifacts, treat the tag as immutable and publish a new `custom.N` instead.

## Version examples

Use these rules:

```text
VERSION file: 0.2.4
existing:     v0.2.4-custom.1
next:         v0.2.4-custom.2

VERSION file: 0.2.4
existing:     v0.2.4-custom.1, .2, .3
next:         v0.2.4-custom.4

VERSION file changes after upstream sync: 0.2.4 -> 0.2.5
existing latest fork release: v0.2.4-custom.3
next:                         v0.2.5-custom.1
```

## Guardrails

- Do not modify `backend/cmd/server/VERSION` to the custom release tag as a release step.
- Do not merge upstream implicitly.
- Do not publish from a dirty working tree.
- Do not tag a commit that is not intended to be on `origin/main`.
- Do not reuse, overwrite, or force-move an existing release tag.
- Do not fabricate release notes.
- Do not create a second GitHub Release manually when the tag-triggered workflow is doing it.
- Do not weaken or bypass the signed plugin release path for a normal tag release.
