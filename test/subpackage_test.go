package test

import (
	"bufio"
	"context"
	"io"
	"io/fs"
	"strings"
	"testing"

	"github.com/cavaliergopher/rpm"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"gotest.tools/v3/assert"
)

// testSubPackages is the entry point called from testLinuxDistro for subpackage integration tests.
func testSubPackages(ctx context.Context, t *testing.T, testConfig testLinuxConfig) {
	t.Run("basic build includes subpackage", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testSubPackageBasicBuild(ctx, t, testConfig)
	})

	t.Run("subpackage metadata", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testSubPackageMetadata(ctx, t, testConfig)
	})

	t.Run("subpackage name override", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testSubPackageNameOverride(ctx, t, testConfig)
	})
}

// newSubPackageSpec creates a spec with a primary package and a "contrib" subpackage.
// Both produce a distinct binary so their outputs can be verified independently.
func newSubPackageSpec() *dalec.Spec {
	return &dalec.Spec{
		Name:        "test-subpkg",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Vendor:      "Dalec",
		Packager:    "Dalec",
		Description: "Test primary package",
		Sources: map[string]dalec.Source{
			"primary-bin": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents:    "#!/usr/bin/env bash\necho primary\n",
						Permissions: 0o755,
					},
				},
			},
			"contrib-bin": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents:    "#!/usr/bin/env bash\necho contrib\n",
						Permissions: 0o755,
					},
				},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{Command: "/bin/true"},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"primary-bin": {},
			},
		},
		Packages: map[string]dalec.SubPackage{
			"contrib": {
				Description: "Contrib subpackage",
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"contrib-bin": {},
					},
				},
			},
		},
	}
}

// testSubPackageBasicBuild verifies that building the package target produces
// output containing both the primary package and the subpackage files.
func testSubPackageBasicBuild(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newSubPackageSpec()

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
		res := solveT(ctx, t, client, sr)

		ref, err := res.SingleRef()
		assert.NilError(t, err)

		pkgfs := bkfs.FromRef(ctx, ref)

		// Walk all files and look for both primary and subpackage outputs.
		var foundPrimary, foundSub bool
		resolvedSubName := "test-subpkg-contrib"

		err = fs.WalkDir(pkgfs, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}

			base := strings.ToLower(path)

			if strings.HasSuffix(base, ".src.rpm") {
				return nil
			}

			isRPM := strings.HasSuffix(base, ".rpm")
			isDeb := strings.HasSuffix(base, ".deb")
			if !isRPM && !isDeb {
				return nil
			}

			// Check if the filename contains the subpackage resolved name.
			if strings.Contains(path, resolvedSubName) {
				foundSub = true
			} else if strings.Contains(path, spec.Name) {
				foundPrimary = true
			}
			return nil
		})
		assert.NilError(t, err)
		assert.Assert(t, foundPrimary, "primary package file not found in build output")
		assert.Assert(t, foundSub, "subpackage file (test-subpkg-contrib) not found in build output")
	})
}

// testSubPackageNameOverride verifies that an explicit Name field on a SubPackage
// runtime deps, provides, conflicts, replaces) is set correctly.
func testSubPackageMetadata(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	rpmDep := "bash"
	debDep := "bash"

	spec := newSubPackageSpec()
	spec.Packages["contrib"] = dalec.SubPackage{
		Description: "The contrib tools",
		Artifacts: &dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"contrib-bin": {},
			},
		},
		Dependencies: &dalec.SubPackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				rpmDep: {},
			},
		},
		Provides: map[string]dalec.PackageConstraints{
			"contrib-compat": {},
		},
		Conflicts: map[string]dalec.PackageConstraints{
			"old-contrib": {},
		},
		Replaces: map[string]dalec.PackageConstraints{
			"old-contrib": {},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
		res := solveT(ctx, t, client, sr)

		ref, err := res.SingleRef()
		assert.NilError(t, err)

		pkgfs := bkfs.FromRef(ctx, ref)

		resolvedSubName := "test-subpkg-contrib"

		checkRPMMetadata := func(path string) {
			f, err := pkgfs.Open(path)
			assert.NilError(t, err)
			defer f.Close()

			pkg, err := rpm.Read(f)
			assert.NilError(t, err)

			// Check description
			assert.Assert(t, strings.Contains(pkg.Summary(), "The contrib tools"),
				"expected description containing 'The contrib tools', got: %s", pkg.Summary())

			// Check runtime dependency
			var foundDep bool
			for _, r := range pkg.Requires() {
				if r.Name() == rpmDep {
					foundDep = true
					break
				}
			}
			assert.Assert(t, foundDep, "expected runtime dep %q in RPM requires", rpmDep)

			// Check provides
			var foundProvides bool
			for _, p := range pkg.Provides() {
				if p.Name() == "contrib-compat" {
					foundProvides = true
					break
				}
			}
			assert.Assert(t, foundProvides, "expected 'contrib-compat' in RPM provides")

			// Check conflicts
			var foundConflicts bool
			for _, c := range pkg.Conflicts() {
				if c.Name() == "old-contrib" {
					foundConflicts = true
					break
				}
			}
			assert.Assert(t, foundConflicts, "expected 'old-contrib' in RPM conflicts")

			// Check obsoletes (replaces maps to Obsoletes in RPM)
			var foundObsoletes bool
			for _, o := range pkg.Obsoletes() {
				if o.Name() == "old-contrib" {
					foundObsoletes = true
					break
				}
			}
			assert.Assert(t, foundObsoletes, "expected 'old-contrib' in RPM obsoletes")
		}

		checkDebMetadata := func(path string) {
			f, err := pkgfs.Open(path)
			assert.NilError(t, err)
			defer f.Close()

			cf := extractDebControlFile(t, f.(io.ReaderAt))
			assert.Assert(t, cf != nil, "control file not found in deb")
			defer cf.Close()

			scanner := bufio.NewScanner(cf)
			var (
				foundDesc      bool
				foundDep       bool
				foundProvides  bool
				foundConflicts bool
				foundReplaces  bool
			)

			for scanner.Scan() {
				txt := scanner.Text()
				key, value, ok := strings.Cut(txt, ": ")
				if !ok {
					// Continuation lines in Description
					if foundDesc && strings.HasPrefix(txt, " ") {
						continue
					}
					continue
				}

				switch key {
				case "Description":
					if strings.Contains(value, "The contrib tools") {
						foundDesc = true
					}
				case "Depends":
					if strings.Contains(value, debDep) {
						foundDep = true
					}
				case "Provides":
					if strings.Contains(value, "contrib-compat") {
						foundProvides = true
					}
				case "Conflicts":
					if strings.Contains(value, "old-contrib") {
						foundConflicts = true
					}
				case "Replaces":
					if strings.Contains(value, "old-contrib") {
						foundReplaces = true
					}
				}
			}
			assert.NilError(t, scanner.Err())

			assert.Assert(t, foundDesc, "expected 'The contrib tools' in deb Description")
			assert.Assert(t, foundDep, "expected %q in deb Depends", debDep)
			assert.Assert(t, foundProvides, "expected 'contrib-compat' in deb Provides")
			assert.Assert(t, foundConflicts, "expected 'old-contrib' in deb Conflicts")
			assert.Assert(t, foundReplaces, "expected 'old-contrib' in deb Replaces")
		}

		var found bool
		err = fs.WalkDir(pkgfs, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".src.rpm") {
				return nil
			}

			// Only check the subpackage file, not the primary.
			if !strings.Contains(path, resolvedSubName) {
				return nil
			}

			if strings.HasSuffix(path, ".rpm") {
				found = true
				checkRPMMetadata(path)
			}
			if strings.HasSuffix(path, ".deb") {
				found = true
				checkDebMetadata(path)
			}
			return nil
		})
		assert.NilError(t, err)
		assert.Assert(t, found, "no subpackage rpm or deb found in output")
	})
}

// testSubPackageNameOverride verifies that an explicit Name field on a SubPackage
// produces a package file with the overridden name rather than the default
// "<parent>-<key>" naming.
func testSubPackageNameOverride(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newSubPackageSpec()
	sp := spec.Packages["contrib"]
	sp.Name = "custom-contrib-name"
	spec.Packages["contrib"] = sp

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
		res := solveT(ctx, t, client, sr)

		ref, err := res.SingleRef()
		assert.NilError(t, err)

		pkgfs := bkfs.FromRef(ctx, ref)

		var foundCustomName bool
		var foundDefaultName bool

		err = fs.WalkDir(pkgfs, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".src.rpm") {
				return nil
			}

			isRPM := strings.HasSuffix(path, ".rpm")
			isDeb := strings.HasSuffix(path, ".deb")
			if !isRPM && !isDeb {
				return nil
			}

			if strings.Contains(path, "custom-contrib-name") {
				foundCustomName = true
			}
			if strings.Contains(path, "test-subpkg-contrib") {
				foundDefaultName = true
			}
			return nil
		})
		assert.NilError(t, err)
		assert.Assert(t, foundCustomName, "expected package file with custom name 'custom-contrib-name' in output")
		assert.Assert(t, !foundDefaultName, "should not find default name 'test-subpkg-contrib' when Name override is set")
	})
}
