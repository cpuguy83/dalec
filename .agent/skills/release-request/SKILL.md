---
name: release-request
description: Prepare Dalec release request pull requests. Use when asked to cut, prepare, request, or draft a Dalec release, release candidate, patch release, or signed release tag.
argument-hint: "[version] [target commit or branch]"
---

# Dalec release request skill

Use this skill when preparing a PR that requests a Dalec release. The release workflow is PR-driven: a reviewed Markdown file under `.github/releases/` supplies the release metadata, then the `Create Release` workflow signs the tag and publishes the immutable GitHub Release after environment approval.

## Process

1. Determine the release tag, target commit, and branch:
   - Use `main` for major and minor releases such as `v0.20.0` or `v0.20.0-rc.1`.
   - Use the relevant `release/**` branch for patch releases such as `v0.20.1`.
   - `target` must be the full 40-character commit SHA to tag, reachable from the PR target branch.
2. Find the previous release tag for `notes_start_tag`.
   - For a major/minor release, use the previous release on the main release line.
   - For a patch release, use the previous patch tag on that release branch.
3. Create exactly one release request file named `.github/releases/<tag>.md`.
4. Use YAML front matter for automation fields and Markdown body content for reviewer context.
5. Include `## Release notes` only when maintainers want curated notes prepended to GitHub-generated release notes. Otherwise omit the section and let GitHub generate the notes.
6. Include a maintainer checklist so reviewers can confirm the target commit and CI signal before merge.
7. Do not create or push tags manually as part of the PR. The workflow creates or verifies the signed tag after the request merges and the `release` environment is approved.

## Release request template

Use [template.md](./template.md) as the starting point. Replace every placeholder before opening the PR.

## Validation

Before considering the PR ready:

```bash
go test ./cmd/release-request
go run ./cmd/lint ./...
```

If workflow files changed, also run:

```bash
nix-shell -p actionlint --run 'actionlint -ignore SC2086 -ignore artifact-metadata .github/workflows/create-release.yml .github/workflows/release.yml .github/workflows/worker-images.yml'
```

