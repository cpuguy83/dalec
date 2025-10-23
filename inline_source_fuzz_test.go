package dalec

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
)

const inlineMaxEntries = 8
const inlineMaxString = 512

type inlineFilePayload struct {
	Name        string `json:"name"`
	Contents    string `json:"contents"`
	Permissions uint32 `json:"perm"`
	UID         int    `json:"uid"`
	GID         int    `json:"gid"`
}

type inlinePayload struct {
	UseDir    bool                `json:"use_dir"`
	File      *inlineFilePayload  `json:"file"`
	Files     []inlineFilePayload `json:"files"`
	DirPerm   uint32              `json:"dir_perm"`
	DirUID    int                 `json:"dir_uid"`
	DirGID    int                 `json:"dir_gid"`
	Path      string              `json:"path"`
	Includes  []string            `json:"includes"`
	Excludes  []string            `json:"excludes"`
	Rename    string              `json:"rename"`
	ExtraStep bool                `json:"extra_step"`
}

func clampString(s string) string {
	if len(s) > inlineMaxString {
		return s[:inlineMaxString]
	}
	return s
}

func buildInlineFile(payload inlineFilePayload) *SourceInlineFile {
	return &SourceInlineFile{
		Contents:    payload.Contents,
		Permissions: fs.FileMode(payload.Permissions) & 0o777,
		UID:         payload.UID,
		GID:         payload.GID,
	}
}

func buildInlineFromPayload(p inlinePayload) SourceInline {
	inline := SourceInline{}

	if p.UseDir {
		dir := &SourceInlineDir{
			Permissions: fs.FileMode(p.DirPerm) & 0o777,
			UID:         p.DirUID,
			GID:         p.DirGID,
			Files:       make(map[string]*SourceInlineFile),
		}

		limit := len(p.Files)
		if limit > inlineMaxEntries {
			limit = inlineMaxEntries
		}
		for i := 0; i < limit; i++ {
			entry := p.Files[i]
			name := clampString(entry.Name)
			if name == "" {
				name = fmt.Sprintf("file-%d", i)
			}
			dir.Files[name] = buildInlineFile(entry)
		}
		inline.Dir = dir
	}

	if p.File != nil {
		inline.File = buildInlineFile(*p.File)
	}

	return inline
}

func FuzzSourceInline(f *testing.F) {
	f.Add([]byte(`{"use_dir":true,"files":[{"name":"foo","contents":"hello"}]}`))
	f.Add([]byte(`{"file":{"name":"x","contents":"inline"}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var payload inlinePayload
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Skip()
		}

		inline := buildInlineFromPayload(payload)

		opts := fetchOptions{
			Path:     clampString(payload.Path),
			Includes: nil,
			Excludes: nil,
			Rename:   clampString(payload.Rename),
		}

		if len(payload.Includes) > 0 {
			count := len(payload.Includes)
			if count > inlineMaxEntries {
				count = inlineMaxEntries
			}
			opts.Includes = make([]string, 0, count)
			for i := 0; i < count; i++ {
				opts.Includes = append(opts.Includes, clampString(payload.Includes[i]))
			}
		}

		if len(payload.Excludes) > 0 {
			count := len(payload.Excludes)
			if count > inlineMaxEntries {
				count = inlineMaxEntries
			}
			opts.Excludes = make([]string, 0, count)
			for i := 0; i < count; i++ {
				opts.Excludes = append(opts.Excludes, clampString(payload.Excludes[i]))
			}
		}

		// validate must never panic
		_ = inline.validate(opts)

		inline.fillDefaults(nil)

		if inline.File != nil || inline.Dir != nil {
			_ = inline.toState(opts)
			_, _ = inline.toMount(opts)
			var buf strings.Builder
			inline.doc(&buf, "inline")
		}

		if payload.ExtraStep {
			spec := Source{
				Inline:   &inline,
				Path:     opts.Path,
				Includes: opts.Includes,
				Excludes: opts.Excludes,
			}
			_ = spec.processBuildArgs(shell.NewLex('\\'), map[string]string{}, AllowAnyArg)
		}

		if inline.Dir != nil {
			for _, file := range inline.Dir.Files {
				_ = file.validate()
			}
		}

		var final strings.Builder
		_, _ = fmt.Fprintf(&final, "noop")
	})
}
