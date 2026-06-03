---
name: release-notes
description: Generate the curated "## Release notes" highlights for a Dalec release request. Use when asked to write, generate, draft, or refresh release notes or highlights for a Dalec release, or to label PRs worth mentioning in the changelog.
argument-hint: "[version] [previous tag]"
---

# Dalec release notes skill

Use this skill to draft the curated **`## Release notes`** highlights that go in a
release request file (`.github/releases/<tag>.md`). Those highlights are a short,
human-written summary of the changes worth calling out; the **full** per-PR
changelog is generated automatically by the `Create Release` workflow (via the
`generate-notes` API, categorized by [`.github/release.yml`](../../release.yml)),
so this skill should produce a concise highlights summary, **not** an exhaustive
PR list.

The signal for "worth mentioning" is the **`release-note`** label on a PR. That
label both groups the PR under **🌟 Highlights** in the generated changelog and
marks it as a candidate for these curated notes.

Pairs with the [`release-request`](../release-request/SKILL.md) skill, which owns
the request file format and the rest of the release metadata.

## Process

1. **Establish the range.** Determine the new `tag`, the `target` commit/branch,
   and the previous release tag (`notes_start_tag`):
   - Major/minor (`vX.Y.0`): previous tag on the main release line.
   - Patch (`vX.Y.Z`): previous patch tag on that `release/**` branch.

2. **Ensure the label exists** (idempotent):

   ```bash
   gh label create release-note \
     --description "Worth calling out in the release notes / changelog highlights" \
     --color 0E8A16 --force
   ```

3. **Gather merged PRs in range.** Use the previous tag's date as the lower
   bound, scoped to the release branch:

   ```bash
   repo="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
   since="$(gh api "repos/${repo}/commits/${notes_start_tag}" --jq .commit.committer.date)"

   # Everything merged in range (candidates to triage):
   gh pr list --repo "$repo" --state merged --base "$branch" \
     --search "merged:>=${since}" --limit 200 \
     --json number,title,url,labels,author

   # Already flagged as noteworthy:
   gh pr list --repo "$repo" --state merged --base "$branch" \
     --search "merged:>=${since} label:release-note" --limit 200 \
     --json number,title,url
   ```

   Cross-check the PR set against `git log ${notes_start_tag}..${target} --merges`
   when in doubt about range boundaries.

4. **Triage and propose labels (get approval first).** Review the candidates and
   identify additional PRs that deserve `release-note`. Before changing any PR,
   present a table and **stop for explicit approval** — do not run `gh pr edit`
   until the maintainer says to proceed:

   | PR # | Title | Add `release-note`? | Proposed highlight | Rationale |
   | :--- | :---- | :------------------ | :----------------- | :-------- |

   What to highlight: user-facing features, behavior or default changes,
   breaking changes (also add a `breaking-change` label), notable fixes, security
   fixes, new target/distro support. What **not** to highlight: routine
   dependency bumps, CI/test-only changes, internal refactors, docs typos.

5. **Apply approved labels:**

   ```bash
   gh pr edit <number> --repo "$repo" --add-label release-note
   # breaking changes:
   gh pr edit <number> --repo "$repo" --add-label release-note --add-label breaking-change
   ```

6. **Write the highlights.** Put a `## Release notes` section in
   `.github/releases/<tag>.md` (create the file from the `release-request` skill's
   [template](../release-request/template.md) if it does not exist yet). The
   section ends at the next `## ` heading and is extracted verbatim by
   `cmd/release-request`, so keep it to highlights only:

   ```markdown
   ## Release notes

   - Add <feature> so users can <impact> (#1234).
   - Fix <problem> that caused <symptom> (#1240).
   - **Breaking:** <what changed and what to do> (#1251).
   ```

   Style: one bullet per highlight, present-tense verb first, describe **impact**
   (what/why) rather than implementation, reference the PR with `(#NNNN)`, and
   lead with breaking changes (bold `**Breaking:**`). Omit the section entirely
   if there is nothing worth curating — the generated changelog still ships.

7. **Validate** the request file still parses and lints:

   ```bash
   go test ./cmd/release-request
   go run ./cmd/lint ./...
   ```

## Notes

- Do not duplicate the generated changelog here; highlights complement it.
- Do not create or push tags. The workflow signs the tag after the request PR
  merges and the `release` environment is approved.
- The curated section is also embedded in the signed tag annotation, so write it
  for both release-page and `git tag -v` readers.
