package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

type request struct {
	Tag           string `yaml:"tag"`
	Target        string `yaml:"target"`
	Prerelease    *bool  `yaml:"prerelease"`
	NotesStartTag string `yaml:"notes_start_tag"`
}

func main() {
	var notesFile string
	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flags.StringVar(&notesFile, "notes-file", "", "path to write the optional release notes section")
	if err := flags.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if flags.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [--notes-file <path>] <release-request.md>\n", os.Args[0])
		os.Exit(2)
	}

	dt, err := os.ReadFile(flags.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	req, err := parse(dt)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if notesFile != "" && req.ReleaseNotes != "" {
		if err := os.WriteFile(notesFile, []byte(req.ReleaseNotes), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("release_notes_file=%s\n", notesFile)
	}

	fmt.Printf("tag=%s\n", req.Tag)
	fmt.Printf("target=%s\n", req.Target)
	fmt.Printf("prerelease=%t\n", req.Prerelease)
	fmt.Printf("notes_start_tag=%s\n", req.NotesStartTag)
}

type output struct {
	Tag           string
	Target        string
	Prerelease    bool
	NotesStartTag string
	ReleaseNotes  string
}

func parse(dt []byte) (output, error) {
	frontMatter, body, err := extractFrontMatter(dt)
	if err != nil {
		return output{}, err
	}

	var req request
	if err := yaml.UnmarshalWithOptions(frontMatter, &req, yaml.DisallowUnknownField()); err != nil {
		return output{}, err
	}

	prerelease := false
	if req.Prerelease != nil {
		prerelease = *req.Prerelease
	}

	out := output{
		Tag:           req.Tag,
		Target:        req.Target,
		Prerelease:    prerelease,
		NotesStartTag: req.NotesStartTag,
		ReleaseNotes:  extractReleaseNotes(body),
	}

	if err := validateOutput(out); err != nil {
		return output{}, err
	}

	return out, nil
}

func extractFrontMatter(dt []byte) ([]byte, []byte, error) {
	dt = bytes.TrimPrefix(dt, []byte{0xef, 0xbb, 0xbf})
	dt = bytes.ReplaceAll(dt, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(dt, []byte("---\n")) {
		return nil, nil, fmt.Errorf("release request must start with YAML front matter")
	}

	rest := dt[len("---\n"):]
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return nil, nil, fmt.Errorf("release request front matter is not closed")
	}

	lineEnd := end + len("\n---")
	if len(rest) > lineEnd && rest[lineEnd] != '\n' && rest[lineEnd] != '\r' {
		return nil, nil, fmt.Errorf("release request front matter closing marker must be on its own line")
	}

	body := rest[lineEnd:]
	body = bytes.TrimPrefix(body, []byte("\n"))
	body = bytes.TrimPrefix(body, []byte("\r\n"))

	return rest[:end], body, nil
}

func extractReleaseNotes(body []byte) string {
	lines := strings.Split(string(body), "\n")

	for i, line := range lines {
		if strings.TrimSpace(line) != "## Release notes" {
			continue
		}

		start := i + 1
		end := len(lines)
		for j := start; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") {
				end = j
				break
			}
		}

		return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
	}

	return ""
}

func validateOutput(out output) error {
	fields := map[string]string{
		"tag":             out.Tag,
		"target":          out.Target,
		"notes_start_tag": out.NotesStartTag,
	}

	for name, value := range fields {
		if bytes.ContainsAny([]byte(value), "\r\n") {
			return fmt.Errorf("%s must not contain newlines", name)
		}
	}

	return nil
}
