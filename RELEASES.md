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
- [Curated highlights and the `release-note` label](#curated-highlights-and-the-release-note-label)
- [What the workflow does](#what-the-workflow-does)
- [Artifacts produced by a release](#artifacts-produced-by-a-release)
- [Which branch should a release request go on?](#which-branch-should-a-release-request-go-on)
- [Patch releases](#patch-releases)
- [Pre-releases](#pre-releases)
- [Latest releases and older lines](#latest-releases-and-older-lines)
- [Verifying a release](#verifying-a-release)
- [Immutability](#immutability)
- [Re-running and recovery](#re-running-and-recovery)
- [One-time setup: the binfmt mirror](#one-time-setup-the-binfmt-mirror)
- [Changing the release workflows](#changing-the-release-workflows)

## Overview

The release pipeline is built around supply-chain security:

- The Git tag is an **annotated, signed tag** created with
  [gitsign](https://github.com/sigstore/gitsign) (Sigstore keyless). The tag
  message carries the curated release notes and the signature-verification
  instructions, so the signed tag is self-describing.
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
   [`release-request` skill](.agent/skills/release-request/SKILL.md) and
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
[`.agent/skills/release-request/template.md`](.agent/skills/release-request/template.md).

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

## Curated highlights and the `release-note` label

Every release ships an auto-generated, per-PR changelog. Two mechanisms let you
shape what readers see:

- **The `release-note` label.** Add it to any PR worth calling out. It groups the
  PR under a **🌟 Highlights** category in the generated changelog (configured in
  [`.github/release.yml`](.github/release.yml)) and marks it as a candidate for
  the curated notes below.
- **The curated `## Release notes` section** in the request file. A short,
  human-written summary prepended above the generated changelog (and embedded in
  the signed tag annotation). Keep it to highlights — the generated changelog
  already lists every PR.

To draft the curated highlights and triage which PRs deserve the `release-note`
label, use the [`release-notes` skill](.agent/skills/release-notes/SKILL.md).

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
   `git tag -s`. The tag message is the release title, the curated release notes,
   and the **verification instructions** (the same content rendered into the
   release body, from
   [`.github/release-notes/verification.md.tmpl`](.github/release-notes/verification.md.tmpl)),
   so the signed tag is fully self-describing and the signature covers the
   verification steps too. The tag is verified with `gitsign verify-tag` and
   pushed. If the tag already exists it must point at the same target and is
   re-verified (idempotent).
3. **Ensure the release branch exists.** For a stable minor/major (`vX.Y.0` with
   no pre-release suffix) cut from `main`, the job creates `release/X.Y` at the
   **target commit** if it does not already exist, so future patches always have
   a home with a stable `@refs/heads/release/X.Y` signing identity. It never
   moves an existing branch. Patches (already on `release/**`) and pre-releases
   skip this step.
4. **Create the release as a draft.** Renders the release body —
   curated notes, then GitHub's generated changelog
   (`POST /releases/generate-notes`, categorized by
   [`.github/release.yml`](.github/release.yml)), then the verification
   instructions from
   [`.github/release-notes/verification.md.tmpl`](.github/release-notes/verification.md.tmpl)
   — and creates a **draft** release with `--verify-tag`. PRs labeled
   `release-note` are surfaced as **Highlights** in the generated changelog; see
   [Curated highlights and the `release-note` label](#curated-highlights-and-the-release-note-label).
5. **Build, sign, and attach the images (blocking).** Dispatches the two image
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
   explicitly as the `make_latest` input — true only when this tag is the
   highest stable version (see
   [Latest releases and older lines](#latest-releases-and-older-lines)) —
   because the release is still a draft and cannot be inferred as "latest" yet.

   This step is idempotent: on a re-run it reuses an already successful or
   in-flight build for the tag instead of rebuilding.
6. **Publish the release.** Once both builds succeed, the draft is published
   (`gh release edit --draft=false`) and locked **immutably** with all signed
   assets already attached. It is marked `--latest` only when it is the highest
   stable version (see [Latest releases and older lines](#latest-releases-and-older-lines)).
   If either build fails, the job fails and the release stays an unpublished
   draft.

```
release request PR ──merge──▶ Create Release (release environment approval)
                                  │
                                  ├─ sign + push tag (gitsign, notes in annotation)
                                  ├─ ensure release/X.Y branch (stable minors only)
                                  ├─ create DRAFT release (notes + changelog + verify steps)
                                  ├─ dispatch + BLOCK on:
                                  │     ├─ release.yml      → frontend image (sign, attest, digest manifest)
                                  │     └─ worker-images.yml → worker images  (sign, attest, digest manifests)
                                  └─ publish release  ▶ IMMUTABLE release + assets
```

## Artifacts produced by a release

- A signed, annotated **Git tag** `v<x.y.z>` whose message contains the curated
  release notes and the verification instructions.
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

## Which branch should a release request go on?

Release requests are accepted on `main` and on `release/**` branches, split by
the kind of release:

- **Minor and major releases** (`vX.Y.0`) are cut from **`main`**. The release
  request file (and its curated notes) stays in `main`'s history, and the
  workflow seeds the matching `release/X.Y` branch from the released commit.
- **Patch releases** (`vX.Y.Z`, `Z > 0`) are cut from the matching
  **`release/**`** branch.

The workflow enforces this: a request on `main` must use a `vX.Y.0` tag, and the
`target` commit must be reachable from the branch the request is on.

You cannot "always release from `main`". A patch is, by definition, a fix on top
of an already-released minor line whose commit is generally **not reachable from
`main`** (because `main` has moved on with newer, possibly breaking, unreleased
work). The `release/**` branch holds that stable line, so it is the only place
the patch's `target` commit is reachable. Release branches also let you backport
a fix to an older line without dragging in everything that has since landed on
`main`.

The trade-off: the signed-tag identity is `create-release.yml@<branch ref>`, so
it is `@refs/heads/main` for minors and `@refs/heads/release/...` for patches.
Verifiers must substitute the branch the release was cut from (see
[Verifying a release](#verifying-a-release)).

## Patch releases

Patch releases (for example `v0.20.1`) are prepared on the relevant `release/**`
branch, not on `main`. Set `target` to the commit on the release branch and
`notes_start_tag` to the previous patch tag on that branch. The signing identity
for a patch release is `create-release.yml@refs/heads/release/...` rather than
`@refs/heads/main`, which matters when verifying the tag.

### I haven't cut the release branch yet

Normally you don't have to. When a stable `vX.Y.0` is released from `main`, the
workflow **automatically creates `release/X.Y`** at the released commit (see step
3 of [What the workflow does](#what-the-workflow-does)), so the branch is waiting
whenever you need the first patch.

You only cut it by hand for minors that predate this automation, or if the branch
was deleted. The workflow still rejects any non-`vX.Y.0` tag on `main`
unconditionally — even if `main` has not moved since the minor and the `target`
commit is therefore still reachable from `main`, the request is refused with
`Patch releases should be prepared on their release/** branch, not main.` In the
common case where nothing has diverged the branch is identical to `main`:

```sh
git branch release/0.5 v0.5.0   # branch at the released minor tag
git push origin release/0.5
# then add .github/releases/v0.5.1.md (target = the patched commit) on that branch
```

Seeding the branch at the minor (which the workflow now does for you) keeps every
patch in a line verifiable under a single, stable `@refs/heads/release/0.5`
signing identity, instead of `v0.5.1` being signed under `@refs/heads/main` and
later patches under `@refs/heads/release/0.5`.

## Pre-releases

Set `prerelease: true` in the request. The release is marked as a pre-release,
is **not** marked `latest`, and the images are **not** tagged `latest`
(`make_latest` is `false`).

## Latest releases and older lines

Only the **highest stable version** is marked `latest`. When publishing, the
workflow lists existing published stable releases, adds the tag being released,
sorts them by semver, and sets `make_latest=true` only when this tag sorts
highest. That value drives both the GitHub Release `latest` flag and whether the
images are retagged `:latest`.

This matters when you patch an **older** line. If `v0.6.0` is already out and you
cut `v0.5.3` on `release/0.5`, `v0.5.3` is published normally (signed tag, signed
images, immutable release) but is **not** marked `latest`, and the `:latest`
image tags keep pointing at the newer `v0.6.x` build. Cutting a newer version
later (e.g. `v0.6.1` or `v0.7.0`) reclaims `latest` automatically. To re-point
`latest` by hand, use `gh release edit <tag> --latest`.

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

## One-time setup: the binfmt mirror

Worker image builds register QEMU/binfmt emulators for cross-architecture
builds using the `tonistiigi/binfmt` image. That image lives on Docker Hub,
whose egress on hosted runners intermittently times out — which previously
failed releases at the "Setup QEMU" step even with retries.

To avoid depending on Docker Hub on the release hot path, the build pulls the
emulator image from a **GHCR mirror** first (`ghcr.io/<owner>/binfmt`, the same
trusted, allowlisted registry used for every other image), falling back to
Docker Hub only if the mirror is missing.

The mirror must be seeded **once per repository owner** (and refreshed whenever
the pinned digest is bumped). Copying is digest-preserving, so the pin in
`worker-images.yml` resolves against GHCR unchanged. Run this one-time command
locally, authenticated to GHCR with a token that has `write:packages`:

```sh
owner="<your-org-or-user>"  # lowercase; matches github.repository_owner
digest="sha256:d3b963f787999e6c0219a48dba02978769286ff61a5f4d26245cb6a6e5567ea3"

docker login ghcr.io                 # username + a PAT with write:packages
docker buildx imagetools create \
  -t "ghcr.io/${owner}/binfmt:latest" \
  "docker.io/tonistiigi/binfmt:latest@${digest}"

# Confirm the digest was preserved on the mirror.
docker buildx imagetools inspect "ghcr.io/${owner}/binfmt@${digest}"
```

(`crane copy docker.io/tonistiigi/binfmt:latest@${digest} ghcr.io/${owner}/binfmt:latest`
works equally well.) Then make the resulting `binfmt` package visible to the
build (typically `internal`/`public` for the org) so the worker jobs can pull
it.

If you bump the binfmt digest, update it in the "Setup QEMU" step in
`worker-images.yml` and re-run the command above with the new digest.

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
