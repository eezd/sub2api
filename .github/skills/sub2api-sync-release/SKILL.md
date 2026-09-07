---
name: sub2api-sync-release
description: Sync the eezd/sub2api fork with the latest Wei-Shaw/sub2api main branch, preserve fork release customizations, derive the next vX.Y.Z-custom.N version, push the fork main branch and tag, publish GitHub Release plus multi-architecture GHCR images, and verify the result. Use when the user asks to 拉取上游、同步最新版、更新 fork、发布 SUB2API、新版本发布、sync upstream, or release eezd/sub2api.
---

# Sub2API Sync Release

Use the bundled script instead of assembling ad hoc Git commands. Run it from the `eezd/sub2api` repository root.

## Workflow

1. Inspect the plan. This fetches refs but does not merge, push, tag, or publish:

   ```bash
   bash .github/skills/sub2api-sync-release/scripts/sync-release.sh --plan
   ```

2. If the user explicitly requested syncing and publishing, execute without asking for another confirmation:

   ```bash
   bash .github/skills/sub2api-sync-release/scripts/sync-release.sh --execute
   ```

3. Report the resulting tag, GitHub Actions URL, GitHub Release URL, GHCR image, architectures, and any skipped verification.

If the user only asks what would be released, stop after `--plan`.

## Invariants

The script MUST:

- operate only on a clean `main` branch;
- discover the fork parent through GitHub instead of assuming a hard-coded upstream remote;
- fast-forward from `origin/main` and merge upstream `main` without force-pushing or rebasing published history;
- abort an upstream merge automatically on conflicts;
- verify the fork release contract before pushing the merged branch;
- derive the base version from the parent repository's latest formal GitHub Release tag, not from an unreleased tag or branch guess;
- reuse an unreleased tag pointing at `HEAD`, making retries idempotent;
- avoid creating a duplicate release when the current `HEAD` is already released;
- publish `vX.Y.Z-custom.N`, incrementing `N` within the current upstream base version; preserve existing `-eezd.N` tags without rewriting them;
- wait for the tag-triggered workflow and fall back to `workflow_dispatch` when GitHub does not register a tag run;
- verify the final GitHub Release and GHCR multi-architecture manifest.

## Release Contract

Before pushing, retain these fork-specific settings:

- GitHub Release and GHCR only; Docker Hub disabled.
- GitHub-native generated release notes.
- Release is formal, not prerelease.
- Only the full GHCR version manifest is published; no `latest`, major, or minor manifest.
- Publishing does not commit `backend/cmd/server/VERSION` back to `main`.

## Failure Handling

- Dirty worktree, wrong branch, divergent `main`, missing GitHub authentication, or invalid upstream formal Release tag: stop before remote writes.
- Merge conflict: the script runs `git merge --abort` and stops. Resolve the upstream conflict manually; never choose ours/theirs for the whole tree.
- Contract check failure: stop before pushing. Update only the fork release customization, then rerun.
- Failure after `main` is pushed but before a tag exists: rerun; synchronization is idempotent.
- Failure after a tag is pushed: do not delete or rewrite the public tag. Rerun; the script reuses the unreleased tag when it still points at `HEAD`.
- Failed Actions run: inspect the exact failed step. Do not create the next suffix until the existing tag is successfully released or intentionally abandoned by the user.
