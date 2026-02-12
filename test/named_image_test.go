package test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

// testNamedImages is the entry point called from testLinuxDistro for named image integration tests.
func testNamedImages(ctx context.Context, t *testing.T, testConfig testLinuxConfig) {
	t.Run("named image with all packages", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNamedImageAllPackages(ctx, t, testConfig)
	})

	t.Run("named image with package filter", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNamedImagePackageFilter(ctx, t, testConfig)
	})

	t.Run("named image with image config", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNamedImageConfig(ctx, t, testConfig)
	})

	t.Run("named image with post-install symlinks", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNamedImagePostSymlinks(ctx, t, testConfig)
	})

	t.Run("named image with image-specific tests", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNamedImageSpecificTests(ctx, t, testConfig)
	})

	t.Run("bare container target fails when images defined", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testBareContainerFailsWithImages(ctx, t, testConfig)
	})

	t.Run("nonexistent image name fails", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNonexistentImageNameFails(ctx, t, testConfig)
	})

	t.Run("named image sub-package file isolation", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNamedImageFileIsolation(ctx, t, testConfig)
	})
}

// newNamedImageSpec creates a spec with a primary package and a "contrib"
// subpackage, plus two named image definitions:
//   - "full": installs all packages (Packages is nil)
//   - "primary-only": installs only the primary package
func newNamedImageSpec() *dalec.Spec {
	return &dalec.Spec{
		Name:        "test-named-img",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Vendor:      "Dalec",
		Packager:    "Dalec",
		Description: "Test named images",
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
		Dependencies: &dalec.PackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				"coreutils": {},
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
		Images: map[string]dalec.ImageDefinition{
			"full": {
				// No Packages field — install everything.
			},
			"primary-only": {
				Packages: []string{"test-named-img"},
			},
		},
	}
}

// testNamedImageAllPackages verifies that a named image with no Packages field
// installs all packages (primary + subpackages).
func testNamedImageAllPackages(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newNamedImageSpec()

	// Add tests that verify both binaries are present.
	spec.Images["full"] = dalec.ImageDefinition{
		Tests: []*dalec.TestSpec{
			{
				Name: "all packages installed",
				Steps: []dalec.TestStep{
					{
						Command: "/usr/bin/primary-bin",
						Stdout:  dalec.CheckOutput{Contains: []string{"primary"}},
					},
					{
						Command: "/usr/bin/contrib-bin",
						Stdout:  dalec.CheckOutput{Contains: []string{"contrib"}},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container+"/full"),
		)
		solveT(ctx, t, gwc, sr)
	})
}

// testNamedImagePackageFilter verifies that a named image with a Packages field
// installs only the listed packages. The "primary-only" image should have the
// primary binary but not the contrib binary.
func testNamedImagePackageFilter(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newNamedImageSpec()

	spec.Images["primary-only"] = dalec.ImageDefinition{
		Packages: []string{"test-named-img"},
		Tests: []*dalec.TestSpec{
			{
				Name: "only primary package installed",
				Steps: []dalec.TestStep{
					{
						Command: "/usr/bin/primary-bin",
						Stdout:  dalec.CheckOutput{Contains: []string{"primary"}},
					},
					{
						// contrib-bin should NOT be present since we only
						// installed the primary package.
						Command: "/bin/bash -c 'test ! -f /usr/bin/contrib-bin'",
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container+"/primary-only"),
		)
		solveT(ctx, t, gwc, sr)
	})
}

// testNamedImageConfig verifies that named image config fields (entrypoint,
// env, labels) are applied to the output image metadata.
func testNamedImageConfig(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newNamedImageSpec()

	spec.Images["configured"] = dalec.ImageDefinition{
		ImageConfig: dalec.ImageConfig{
			Entrypoint: "/usr/bin/primary-bin",
			Env:        []string{"FOO=bar"},
			Labels: map[string]string{
				"com.example.test": "named-image",
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container+"/configured"),
		)
		res := solveT(ctx, t, gwc, sr)

		dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
		assert.Assert(t, ok, "result metadata should contain image config")

		var imgCfg dalec.DockerImageSpec
		assert.NilError(t, json.Unmarshal(dt, &imgCfg))

		assert.DeepEqual(t, imgCfg.Config.Entrypoint, []string{"/usr/bin/primary-bin"})

		assert.Assert(t, slices.Contains(imgCfg.Config.Env, "FOO=bar"))
		assert.Assert(t, imgCfg.Config.Labels["com.example.test"] == "named-image",
			"expected label com.example.test=named-image, got: %v", imgCfg.Config.Labels)
	})
}

// testNamedImagePostSymlinks verifies that post-install symlinks from a named
// image definition are applied correctly.
func testNamedImagePostSymlinks(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newNamedImageSpec()

	spec.Images["with-links"] = dalec.ImageDefinition{
		ImageConfig: dalec.ImageConfig{
			Post: &dalec.PostInstall{
				Symlinks: map[string]dalec.SymlinkTarget{
					"/usr/bin/primary-bin": {
						Path: "/primary-link",
					},
				},
			},
		},
		Tests: []*dalec.TestSpec{
			{
				Name: "symlink created",
				Steps: []dalec.TestStep{
					{Command: "/bin/bash -exc 'test -L /primary-link'"},
					{Command: "/bin/bash -exc 'test \"$(readlink /primary-link)\" = \"/usr/bin/primary-bin\"'"},
					{
						Command: "/primary-link",
						Stdout:  dalec.CheckOutput{Contains: []string{"primary"}},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container+"/with-links"),
		)
		solveT(ctx, t, gwc, sr)
	})
}

// testNamedImageSpecificTests verifies that image-specific tests are executed
// during the build. We rely on the dalec test runner to fail the build if a
// test step fails, so a successful solve means the tests ran and passed.
func testNamedImageSpecificTests(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newNamedImageSpec()

	spec.Images["tested"] = dalec.ImageDefinition{
		Tests: []*dalec.TestSpec{
			{
				Name: "image-specific test",
				Steps: []dalec.TestStep{
					{
						Command: "/usr/bin/primary-bin",
						Stdout:  dalec.CheckOutput{Contains: []string{"primary"}},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container+"/tested"),
		)
		solveT(ctx, t, gwc, sr)
	})
}

// testBareContainerFailsWithImages verifies that when a spec has named images
// defined, using the bare "container" target (without specifying an image name)
// produces an error.
func testBareContainerFailsWithImages(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newNamedImageSpec()

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container),
		)
		_, err := gwc.Solve(ctx, sr)
		assert.Assert(t, err != nil, "expected error when using bare container target with named images")
		assert.Assert(t, strings.Contains(err.Error(), "named images"),
			"error should mention named images, got: %v", err)
	})
}

// testNonexistentImageNameFails verifies that requesting a named image that
// doesn't exist in the spec produces an error.
func testNonexistentImageNameFails(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := newNamedImageSpec()

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container+"/does-not-exist"),
		)
		_, err := gwc.Solve(ctx, sr)
		assert.Assert(t, err != nil, "expected error when requesting nonexistent named image")
		assert.Assert(t, strings.Contains(err.Error(), "does-not-exist"),
			"error should mention the image name, got: %v", err)
	})
}

// testNamedImageFileIsolation verifies that a named image installing only a
// specific sub-package contains ONLY that sub-package's files — not files from
// the main package or other sub-packages.
func testNamedImageFileIsolation(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := &dalec.Spec{
		Name:        "test-isolation",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Vendor:      "Dalec",
		Packager:    "Dalec",
		Description: "Test file isolation across packages",
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
			"extras-bin": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents:    "#!/usr/bin/env bash\necho extras\n",
						Permissions: 0o755,
					},
				},
			},
			"primary.conf": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: "primary-config=true\n",
					},
				},
			},
			"contrib.conf": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: "contrib-config=true\n",
					},
				},
			},
		},
		Dependencies: &dalec.PackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				"coreutils": {},
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
			ConfigFiles: map[string]dalec.ArtifactConfig{
				"primary.conf": {},
			},
		},
		Packages: map[string]dalec.SubPackage{
			"contrib": {
				Description: "Contrib subpackage",
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"contrib-bin": {},
					},
					ConfigFiles: map[string]dalec.ArtifactConfig{
						"contrib.conf": {},
					},
				},
			},
			"extras": {
				Description: "Extras subpackage",
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"extras-bin": {},
					},
				},
			},
		},
		Images: map[string]dalec.ImageDefinition{
			"contrib-only": {
				// Install ONLY the contrib sub-package.
				Packages: []string{"test-isolation-contrib"},
				Tests: []*dalec.TestSpec{
					{
						Name: "only contrib files present",
						Steps: []dalec.TestStep{
							// contrib-bin MUST exist
							{
								Command: "/usr/bin/contrib-bin",
								Stdout:  dalec.CheckOutput{Contains: []string{"contrib"}},
							},
							// contrib.conf MUST exist
							{
								Command: "/bin/bash -c 'test -f /etc/contrib.conf'",
							},
							// primary-bin must NOT exist
							{
								Command: "/bin/bash -c 'test ! -f /usr/bin/primary-bin'",
							},
							// primary.conf must NOT exist
							{
								Command: "/bin/bash -c 'test ! -f /etc/primary.conf'",
							},
							// extras-bin must NOT exist
							{
								Command: "/bin/bash -c 'test ! -f /usr/bin/extras-bin'",
							},
						},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(
			withSpec(ctx, t, spec),
			withBuildTarget(cfg.Target.Container+"/contrib-only"),
		)
		solveT(ctx, t, gwc, sr)
	})
}
