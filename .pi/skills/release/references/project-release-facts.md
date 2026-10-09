# eezd/sub2api release facts

Use this file only when the repository's release behavior needs to be re-checked or explained.

- Fork: `https://github.com/eezd/sub2api`
- Upstream: `https://github.com/Wei-Shaw/sub2api`
- Fork release tags follow `vX.Y.Z-custom.N`.
- Historical releases include repeated custom revisions on one upstream base, such as `v0.2.4-custom.1`, `.2`, and `.3`.
- `backend/cmd/server/VERSION` follows the upstream/base version rather than the custom suffix.
- `.github/workflows/release.yml` runs on pushed `v*` tags.
- The workflow resolves the release version from the tag and writes that full version into the release checkout used to build artifacts.
- Normal tag releases build the full artifact set. The manual `simple_release` mode is not the normal tag path.
- The workflow reads the annotated tag body for release notification text.
- GoReleaser publishes the GitHub Release and uses GitHub-native changelog generation.
- Normal fork releases publish versioned GHCR images and a signed OpenAI Transport `.s2plugin` asset.
