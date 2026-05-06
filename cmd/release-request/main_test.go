package main

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()

	req, err := parse([]byte(`---
tag: v0.20.0
target: 0123456789abcdef0123456789abcdef01234567
prerelease: true
notes_start_tag: v0.19.0
---

# Dalec v0.20.0

## Release notes

This release has curated notes.

## Maintainer checklist
`))
	if err != nil {
		t.Fatal(err)
	}

	if req.Tag != "v0.20.0" {
		t.Fatalf("unexpected tag: %q", req.Tag)
	}
	if req.Target != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("unexpected target: %q", req.Target)
	}
	if !req.Prerelease {
		t.Fatal("expected prerelease to be true")
	}
	if req.NotesStartTag != "v0.19.0" {
		t.Fatalf("unexpected notes_start_tag: %q", req.NotesStartTag)
	}
	if req.ReleaseNotes != "This release has curated notes." {
		t.Fatalf("unexpected release notes: %q", req.ReleaseNotes)
	}
}

func TestParseDefaultsPrerelease(t *testing.T) {
	t.Parallel()

	req, err := parse([]byte(`---
tag: v0.20.0
target: 0123456789abcdef0123456789abcdef01234567
---
`))
	if err != nil {
		t.Fatal(err)
	}

	if req.Prerelease {
		t.Fatal("expected prerelease to default to false")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	_, err := parse([]byte(`---
tag: v0.20.0
target: 0123456789abcdef0123456789abcdef01234567
extra: nope
---
`))
	if err == nil {
		t.Fatal("expected unknown field to fail")
	}
}

func TestParseRejectsMissingFrontMatter(t *testing.T) {
	t.Parallel()

	_, err := parse([]byte(`tag: v0.20.0
target: 0123456789abcdef0123456789abcdef01234567
`))
	if err == nil {
		t.Fatal("expected missing front matter to fail")
	}
}

func TestParseRejectsUnclosedFrontMatter(t *testing.T) {
	t.Parallel()

	_, err := parse([]byte(`---
tag: v0.20.0
target: 0123456789abcdef0123456789abcdef01234567
`))
	if err == nil {
		t.Fatal("expected unclosed front matter to fail")
	}
}

func TestParseRejectsNewlinesInOutputFields(t *testing.T) {
	t.Parallel()

	_, err := parse([]byte(`---
tag: |
  v0.20.0
  malicious=true
target: 0123456789abcdef0123456789abcdef01234567
---
`))
	if err == nil {
		t.Fatal("expected newline in output field to fail")
	}
}

func TestExtractReleaseNotesMissingSection(t *testing.T) {
	t.Parallel()

	req, err := parse([]byte(`---
tag: v0.20.0
target: 0123456789abcdef0123456789abcdef01234567
---

# Dalec v0.20.0
`))
	if err != nil {
		t.Fatal(err)
	}

	if req.ReleaseNotes != "" {
		t.Fatalf("unexpected release notes: %q", req.ReleaseNotes)
	}
}
