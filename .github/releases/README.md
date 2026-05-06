# Release requests

Create releases by opening a pull request that adds one `.github/releases/<tag>.md` file to `main` or the relevant `release/**` branch. After the PR merges, the `Create Release` workflow requires approval through the `release` environment, signs the requested tag with Sigstore `gitsign`, creates the immutable GitHub Release with generated notes, and dispatches image publishing.

Major and minor releases should be requested on `main`. Patch releases should be requested on their release branch.

When using an agent, ask it to use the `/release-request` skill to prepare the release request PR.

```markdown
---
tag: v0.20.0
target: 0123456789abcdef0123456789abcdef01234567
prerelease: false
notes_start_tag: v0.19.0
---

# Dalec v0.20.0

This release request creates the signed `v0.20.0` tag and publishes an immutable
GitHub Release after approval through the `release` environment.

## Release notes

Optional curated release notes can go here. When present, this section is
prepended to GitHub's generated release notes.

## Maintainer checklist

- [ ] The target commit is the intended release commit.
- [ ] CI is green on the target branch.
- [ ] Generated release notes have been reviewed.
```

- `tag` must match `vMAJOR.MINOR.PATCH` with an optional prerelease suffix.
- `target` must be the full commit SHA to tag and must be reachable from the branch where the PR merges.
- `prerelease` is optional and defaults to `false`.
- `notes_start_tag` is optional and is passed to GitHub's generated release notes.
- The `## Release notes` section is optional. When present, it is prepended to GitHub's generated release notes; other Markdown body content is for reviewer context only.
