# Releasing Dalec

Dalec releases are **PR-driven**, **signed end-to-end**, and **immutable**. A
release is requested by merging a small Markdown file into the repository; an
automated, approval-gated workflow then signs the tag, builds and signs the
release images, ties their provenance to the release, and publishes an immutable
GitHub Release.

Nobody pushes tags or creates releases by hand. Everything below is produced by
the `Create Release` workflow ([`.github/workflows/create-release.yml`](.github/workflows/create-release.yml))
after a release request merges and the `release` environment is approved.

## Table of contents

- [Overview](#overview)
- [How to cut a release](#how-to-cut-a-release)
- [The release request file](#the-release-request-file)
- [What the workflow does](#what-the-workflow-does)
- [Artifacts produced by a release](#artifacts-produced-by-a-release)
- [Patch releases](#patch-releases)
- [Pre-releases](#pre-releases)
- [Verifying a release](#verifying-a-release)
- [Immutability](#immutability)
- [Re-running and recovery](#re-running-and-recovery)
- [Changing the release workflows](#changing-the-release-workflows)

## Overview

The release pipeline is built around supply-chain security:

- The Git tag is an **annotated, signed tag** created with
  [gitsign](https://github.com/sigstore/gitsign) (Sigstore keyless). The tag
  message carries the curated release notes.
- The **frontend image** and every **worker image** are built, pushed to GHCR,
  signed with [cosign](https://github.com/sigstore/cosign) keyless signing, and
  get a [build-provenance attestation](https://docs.github.com/actions/security-guides/using-artifact-attestations).
- Because the images are not GitHub release assets, each build attaches a small
  signed **digest manifest** (`*.digest.json` plus a `*.digest.json.cosign.bundle`
  signature) to the release. This ties the exact image digests to the release.
- The release is created as a **draft**, the signed images and their digest
  manifests are attached while it is mutable, and only then is it **published**,
  which makes it **immutable** with all assets in place.
- The `Create Release` job blocks until both image builds succeed, so a release
  is only ever published once its signed images exist. A failed build leaves an
  unpublished draft instead of a broken release.

## How to cut a release

1. **Open a release-request PR.** Add exactly one file named
   `.github/releases/<tag>.md` (for example `.github/releases/v0.27.0.md`). See
   [The release request file](#the-release-request-file) for the format. The
   [`release-request` skill](.github/skills/release-request/SKILL.md) and
   [`cmd/release-request`](cmd/release-request) help author and validate it.
2. **Review and merge.** Reviewers confirm the `target` commit, that CI is green
   on the target branch, and that `notes_start_tag` points at the previous
   release. Do **not** create or push a tag in the PR.
3. **Workflow triggers on merge.** Pushing the new `.github/releases/*.md` file
   to `main` (or a `release/**` branch) starts the `Create Release` workflow. It
   can also be run manually via **workflow_dispatch** with the `release_file`
   input.
4. **Approve the `release` environment.** The `create-release` job runs in the
   `release` environment, so it waits for the configured reviewers to approve
   before it signs anything.
5. **Wait for completion.** The job creates the signed tag, drafts the release,
   builds and signs the images, attaches the signed digest manifests, and
   publishes the immutable release. This blocks on the image builds and takes
   roughly 10–15 minutes.

## The release request file

A release request lives at `.github/releases/<tag>.md` and has YAML front matter
(automation fields) followed by a Markdown body (reviewer context). Start from
[`.github/skills/release-request/template.md`](.github/skills/release-request/template.md).

```markdown
---
tag: v0.27.0
target: 0123456789abcdef0123456789abcdef01234567
prerelease: false
notes_start_tag: v0.26.0
---

# Dalec v0.27.0

## Release notes

Optional curated notes. When present, this section is prepended to GitHub's
generated changelog and is embedded in the signed tag annotation. Omit the
section to use generated notes only.

## Maintainer checklist

- [ ] The target commit is the intended release commit.
- [ ] CI is green on the target branch.
- [ ] `notes_start_tag` points to the previous release for generated notes.
```

Front-matter fields (parsed and validated by `cmd/release-request`):

| Field             | Required | Meaning |
| ----------------- | -------- | ------- |
| `tag`             | yes      | Release tag, `vMAJOR.MINOR.PATCH` with an optional `-prerelease` suffix. The file must be named `<tag>.md`. |
| `target`          | yes      | Full 40-character commit SHA to tag. Must be reachable from the PR's target branch. |
| `prerelease`      | no       | `true` marks a pre-release and prevents it from becoming `latest`. Defaults to `false`. |
| `notes_start_tag` | no       | Previous release tag used as the starting point for GitHub's generated changelog. |

Only the content under a `## Release notes` heading is treated as curated notes;
everything else in the body (title, checklist) is reviewer context.

Validation before opening the PR:

```sh
go test ./cmd/release-request
go run ./cmd/lint ./...
```

## What the workflow does

The `create-release` job ([`.github/workflows/create-release.yml`](.github/workflows/create-release.yml))
runs these steps, in order, inside a [step-security/harden-runner](https://github.com/step-security/harden-runner)
with a **blocking egress allowlist** (only GitHub and the Sigstore/Go endpoints
needed for signing and module download):

1. **Read and validate the release request.** `cmd/release-request` parses the
   front matter, enforces the tag/SHA format, checks that the target is
   reachable from the branch, writes the curated notes to `release-notes.md`,
   and emits the `tag`, `target`, `prerelease`, and `notes_start_tag` outputs.
2. **Create the signed tag.** Configures gitsign as the signing program and runs
   `git tag -s`. The tag message is the release title plus the curated release
   notes, so the signed tag is self-describing. The tag is verified with
   `gitsign verify-tag` and pushed. If the tag already exists it must point at
   the same target and is re-verified (idempotent).
3. **Create the release as a draft.** Renders the release body —
   curated notes, then GitHub's generated changelog
   (`POST /releases/generate-notes`), then the verification instructions from
   [`.github/release-notes/verification.md.tmpl`](.github/release-notes/verification.md.tmpl)
   — and creates a **draft** release with `--verify-tag`.
4. **Build, sign, and attach the images (blocking).** Dispatches the two image
   workflows at the **tag ref** and blocks until both succeed:
   - [`release.yml`](.github/workflows/release.yml) → builds/signs the
     **frontend** image via the reusable
     [`frontend-image.yml`](.github/workflows/frontend-image.yml).
   - [`worker-images.yml`](.github/workflows/worker-images.yml) → matrix-builds
     and signs every **worker** image.

   Each build pushes and cosign-signs its image(s), records a build-provenance
   attestation, then writes a `*.digest.json` manifest, signs it
   (`cosign sign-blob` → `*.digest.json.cosign.bundle`), and uploads both to the
   **draft** release. Whether images also get the `latest` tag is passed
   explicitly as the `make_latest` input (true for non-pre-releases), because the
   release is still a draft and cannot be inferred as "latest" yet.

   This step is idempotent: on a re-run it reuses an already successful or
   in-flight build for the tag instead of rebuilding.
5. **Publish the release.** Once both builds succeed, the draft is published
   (`gh release edit --draft=false`, marked `--latest` unless it is a
   pre-release). Publishing locks the release **immutably** with all signed
   assets already attached. If either build fails, the job fails and the release
   stays an unpublished draft.

```
release request PR ──merge──▶ Create Release (release environment approval)
                                  │
                                  ├─ sign + push tag (gitsign, notes in annotation)
                                  ├─ create DRAFT release (notes + changelog + verify steps)
                                  ├─ dispatch + BLOCK on:
                                  │     ├─ release.yml      → frontend image (sign, attest, digest manifest)
                                  │     └─ worker-images.yml → worker images  (sign, attest, digest manifests)
                                  └─ publish release  ▶ IMMUTABLE release + assets
```

## Artifacts produced by a release

- A signed, annotated **Git tag** `v<x.y.z>` whose message contains the curated
  release notes.
- An immutable **GitHub Release** with the rendered notes and, as assets, one
  `*.digest.json` + `*.digest.json.cosign.bundle` pair per image (frontend and
  each worker).
- The **frontend image** at `ghcr.io/<owner>/<repo>/frontend`, tagged
  `<x.y.z>`, `<x.y>`, and (for non-pre-releases) `latest`. Note the image tag
  **drops the leading `v`**.
- One **worker image** per matrix target at
  `ghcr.io/<owner>/<repo>/<distro>/worker`, tagged `v<x.y>` and (for
  non-pre-releases) `latest`. Worker tags **keep the leading `v`**. The worker
  targets are listed in [`.github/workflows/worker-images/matrix.json`](.github/workflows/worker-images/matrix.json).
- cosign signatures and build-provenance attestations for every image, stored in
  the registry.

## Patch releases

Patch releases (for example `v0.20.1`) are prepared on the relevant `release/**`
branch, not on `main`. The workflow enforces this: a request on `main` must use a
`vX.Y.0` tag. Set `target` to the commit on the release branch and
`notes_start_tag` to the previous patch tag on that branch. The signing identity
for a patch release is therefore `create-release.yml@refs/heads/release/...`
rather than `@refs/heads/main` (relevant when verifying the tag).

## Pre-releases

Set `prerelease: true` in the request. The release is marked as a pre-release,
is **not** marked `latest`, and the images are **not** tagged `latest`
(`make_latest` is `false`).

## Verifying a release

Every published release includes a "Verifying this release" section with the
exact commands (rendered from
[`.github/release-notes/verification.md.tmpl`](.github/release-notes/verification.md.tmpl)).
The patterns are:

**Verify the signed tag** (substitute the signing identity — `@refs/heads/main`
for minor releases, `@refs/heads/release/...` for patch releases):

```sh
gitsign verify-tag \
  --certificate-identity="https://github.com/<owner>/<repo>/.github/workflows/create-release.yml@refs/heads/main" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  v0.27.0
```

**Verify the frontend image** (the image tag has no leading `v`; the certificate
identity keeps it):

```sh
cosign verify ghcr.io/<owner>/<repo>/frontend:0.27.0 \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  --certificate-identity="https://github.com/<owner>/<repo>/.github/workflows/frontend-image.yml@refs/tags/v0.27.0"
```

**Verify a digest manifest** attached to the release (download the
`*.digest.json` and its `*.digest.json.cosign.bundle` first). Use
`frontend-image.yml` in the identity for `frontend.digest.json` and
`worker-images.yml` for the `*-worker.digest.json` manifests:

```sh
cosign verify-blob frontend.digest.json \
  --bundle frontend.digest.json.cosign.bundle \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  --certificate-identity="https://github.com/<owner>/<repo>/.github/workflows/frontend-image.yml@refs/tags/v0.27.0"
```

For worker images and `gh attestation verify` usage, see the
[image verification guide](https://project-dalec.github.io/dalec/verifying-images).

## Immutability

The repository has **release immutability** enabled: once a release is
published, its notes and assets are locked (`HTTP 422: Cannot upload assets to an
immutable release`). The pipeline is built around this — it attaches all signed
digest manifests to the **draft** and publishes only afterward, so published
releases are tamper-proof and complete. Do not add a step that uploads assets to
an already-published release; attach assets while the release is still a draft.

## Re-running and recovery

- The job is **idempotent**. Re-running it reuses an existing signed tag (after
  confirming it points at the same target) and reuses an already successful or
  in-flight image build for the tag instead of rebuilding.
- If an image build fails, the `create-release` job fails and the release
  remains an **unpublished draft**. Fix the cause and re-run; the draft is
  reused and published once the builds pass.

## Changing the release workflows

When editing any of the release workflows, lint them before opening a PR:

```sh
nix-shell -p actionlint --run 'actionlint -ignore SC2086 -ignore artifact-metadata \
  .github/workflows/create-release.yml \
  .github/workflows/release.yml \
  .github/workflows/worker-images.yml'
```

Keep the harden-runner egress allowlist in `create-release.yml` in sync with any
new network dependency, and remember that publishing or asset uploads must
happen while the release is still a draft (see [Immutability](#immutability)).
