---
name: sync-upstream
description: Synchronize this fork of Wei-Shaw/sub2api with the upstream repository. Use when the user asks to sync, update, merge, pull, or incorporate changes from the upstream sub2api project.
---

# Sync upstream

This repository is a fork/customized version of:

`https://github.com/Wei-Shaw/sub2api`

Synchronize upstream changes into the current project while preserving local customizations.

## Workflow

1. Inspect the current repository before making changes.

   Check:

   ```bash
   git status --short
   git branch --show-current
   git remote -v
   ```

2. Ensure the `upstream` remote points to:

   ```text
   https://github.com/Wei-Shaw/sub2api.git
   ```

   If `upstream` does not exist:

   ```bash
   git remote add upstream https://github.com/Wei-Shaw/sub2api.git
   ```

   If it exists with a different URL, inspect why before changing it.

3. Fetch the latest upstream commits:

   ```bash
   git fetch upstream --prune
   ```

4. Determine the upstream default branch instead of assuming its name.

   Prefer the remote HEAD when available:

   ```bash
   git symbolic-ref --short refs/remotes/upstream/HEAD
   ```

   If necessary, inspect the upstream remote to determine its default branch.

5. Before merging, inspect the incoming changes.

   Compare the current branch against the upstream branch and understand what upstream changed, especially changes touching files that have local modifications.

6. Merge the upstream branch into the current branch.

   Use a normal merge. Do not rebase, force-reset, discard local commits, or rewrite history unless the user explicitly requests it.

## Conflict handling

When a merge conflict occurs, first classify the conflict.

### Code-level conflict

A code-level conflict is one where the intended behavior of both sides is compatible and the conflict is caused by overlapping edits, refactoring, imports, formatting, renamed symbols, moved code, or similar implementation details.

Resolve these conflicts autonomously.

When resolving:

- Preserve the behavior of the current fork.
- Incorporate the upstream implementation where compatible.
- Adapt local code to upstream API or structural changes when necessary.
- Do not blindly choose `ours` or `theirs`.
- Read the surrounding implementation and understand both versions before editing.
- Remove all conflict markers.
- Keep the resulting implementation internally consistent.

After resolving, run appropriate formatting, type checking, tests, builds, or other available project validation.

### Functional conflict

A functional conflict exists when upstream and the current fork intentionally implement different behavior and both cannot be preserved without making a product/design decision.

Examples include:

- Upstream removes a feature that the fork intentionally retains.
- Upstream changes authentication or authorization behavior while the fork has custom authentication behavior.
- The fork modifies API semantics that upstream also changed differently.
- Configuration defaults conflict for intentional product reasons.
- The same feature has been independently implemented upstream and in the fork with materially different behavior.
- Keeping both implementations requires deciding which behavior users should receive.

Do NOT make the product decision yourself.

Stop and ask the user.

Explain:

1. What upstream changed.
2. What the fork currently does.
3. Why the behaviors conflict.
4. Which files or modules are affected.
5. The reasonable resolution options.
6. Your recommended option and its consequences.

Do not continue the merge past that decision point until the user chooses how the functionality should behave.

## Local modifications

Preserve all intentional fork-specific changes.

Do not treat differences from upstream as obsolete merely because upstream differs.

If there are uncommitted changes before synchronization, protect them before proceeding. Never discard them.

If they interfere with the merge, preserve them safely and restore them after the upstream merge.

## Validation

After resolving all conflicts:

1. Confirm there are no unresolved Git conflicts.
2. Review the complete merge diff.
3. Look specifically for accidental removal of fork-specific behavior.
4. Run the project's relevant tests/build/type checks.
5. Fix straightforward regressions caused by the merge.
6. If a regression requires a product or behavioral decision, ask the user.

Before finishing, report:

- Upstream commits incorporated.
- Important upstream changes.
- Files with merge conflicts.
- How code-level conflicts were resolved.
- Any fork-specific behavior deliberately preserved.
- Validation performed and its result.
