//go:generate go run $GOFILE ../../.github/workflows/fuzz/matrix.json

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type matrix struct {
	Include []entry `json:"include" yaml:"include"`
}

type entry struct {
	Package string `json:"pkg" yaml:"pkg"`
	Fuzz    string `json:"fuzz" yaml:"fuzz"`
}

func main() {
	flag.Parse()
	if flag.NArg() > 1 {
		fmt.Println("Usage: fuzz-matrix [output]")
		os.Exit(2)
	}

	output := os.Stdout
	if outPath := flag.Arg(0); outPath != "" {
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			panic(fmt.Errorf("failed to create output directory: %w", err))
		}
		f, err := os.Create(outPath)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		output = f
	}

	pkgs, err := goList()
	if err != nil {
		panic(err)
	}
	modPath, err := modulePath()
	if err != nil {
		panic(err)
	}

	var entries []entry
	for _, pkg := range pkgs {
		names, err := goListFuzz(pkg)
		if err != nil {
			panic(err)
		}
		if len(names) == 0 {
			continue
		}
		rel := trimModulePath(modPath, pkg)
		for _, name := range names {
			entries = append(entries, entry{
				Package: rel,
				Fuzz:    name,
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Package == entries[j].Package {
			return entries[i].Fuzz < entries[j].Fuzz
		}
		return entries[i].Package < entries[j].Package
	})

	out := matrix{Include: entries}

	enc := json.NewEncoder(output)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		panic(fmt.Errorf("failed to write output: %w", err))
	}
}

func goList() ([]string, error) {
	cmd := exec.Command("go", "list", "./...")
	cmd.Dir = moduleRoot()
	stdout := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var pkgs []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pkgs = append(pkgs, line)
	}
	return pkgs, nil
}

func modulePath() (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Path}}")
	cmd.Dir = moduleRoot()
	stdout := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go list -m failed: %w", err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func goListFuzz(pkg string) ([]string, error) {
	cmd := exec.Command("go", "test", pkg, "-list", "^Fuzz")
	cmd.Dir = moduleRoot()
	stdout := bytes.NewBuffer(nil)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go test -list '^Fuzz' failed for %s: %w", pkg, err)
	}

	var names []string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "ok "):
			continue
		case strings.HasPrefix(line, "FAIL"):
			return nil, fmt.Errorf("go test reported failure for %s: %s", pkg, line)
		case strings.HasPrefix(line, "?"):
			continue
		case strings.Contains(line, "[no test files]"):
			continue
		case line == "PASS":
			continue
		}
		names = append(names, line)
	}
	return names, scanner.Err()
}

func trimModulePath(module, pkg string) string {
	module = strings.TrimSuffix(module, "/")
	switch {
	case module == "":
		return pkg
	case pkg == module:
		return "."
	case strings.HasPrefix(pkg, module+"/"):
		return strings.TrimPrefix(pkg, module+"/")
	default:
		return pkg
	}
}

func moduleRoot() string {
	// Get the current file and use that to craft a path to the repo root
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return root
}
