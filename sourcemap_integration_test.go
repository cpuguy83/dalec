package dalec

import (
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceMapErrorReporting(t *testing.T) {
	// Test fixture with intentionally bad build dependency
	yamlContent := `# syntax=ghcr.io/azure/dalec/frontend:latest

name: sourcemap-test
description: Test spec with intentionally bad build dependency to verify source maps
website: https://example.com
version: 1.0.0
revision: 1
vendor: Test
license: MIT

# This spec is designed to fail with a bad build dependency
# to test that source maps correctly point to line 16 below
dependencies:
  build:
    gcc:
    git:
    this-package-does-not-exist-and-should-fail:  # Line 16 - intentionally bad package
    make:

sources:
  hello:
    inline:
      file:
        contents: |
          #!/bin/bash
          echo "Hello World"

build:
  steps:
    - command: echo "This build will fail during dependency installation"

artifacts:
  binaries:
    hello: {}
`

	// Load the spec with source map
	spec, sourceMapInfo, err := LoadSpecWithSourceMap("test.yml", []byte(yamlContent))
	require.NoError(t, err)
	require.NotNil(t, spec)
	require.NotNil(t, sourceMapInfo)

	// Verify the spec parsed correctly
	assert.Equal(t, "sourcemap-test", spec.Name)
	assert.Equal(t, "1.0.0", spec.Version)

	// Verify we have source map information
	assert.Equal(t, "test.yml", sourceMapInfo.Filename)
	assert.NotEmpty(t, sourceMapInfo.Data)

	// Check that we can extract position information for the bad dependency
	// The problematic package should be in the build dependencies
	assert.Contains(t, spec.Dependencies.Build, "this-package-does-not-exist-and-should-fail")

	// Create a dummy LLB state for testing constraint creation
	dummyState := llb.Image("alpine")

	// Verify the source map can create constraints for the dependencies section
	sourceMap := sourceMapInfo.CreateSourceMap(&dummyState)
	require.NotNil(t, sourceMap)

	// Test that we can get constraints for the build dependencies path
	constraints := sourceMapInfo.LocationConstraint(&dummyState, "dependencies.build.this-package-does-not-exist-and-should-fail")
	assert.NotNil(t, constraints, "Should be able to create constraints for build dependency path")

	// Verify we can get position information for various parts of the spec
	buildConstraints := sourceMapInfo.LocationConstraint(&dummyState, "dependencies.build")
	assert.NotNil(t, buildConstraints, "Should be able to create constraints for build dependencies section")

	nameConstraints := sourceMapInfo.LocationConstraint(&dummyState, "name")
	assert.NotNil(t, nameConstraints, "Should be able to create constraints for name field")
}

func TestSourceMapIntegrationWithBadFixture(t *testing.T) {
	// This test demonstrates how to use the bad fixture file we created
	t.Run("BadBuildDependencyFixture", func(t *testing.T) {
		// In a real integration test, you would try to build this spec
		// and verify that the error message includes source location information
		// pointing to line 18 where the bad dependency is defined.
		
		// For now, we just verify we can load it with source maps
		fixtureContent := `# syntax=ghcr.io/azure/dalec/frontend:latest

name: sourcemap-test
description: Test spec with intentionally bad build dependency to verify source maps
website: https://example.com
version: 1.0.0
revision: 1
vendor: Test
license: MIT

# This spec is designed to fail with a bad build dependency
# to test that source maps correctly point to line 18 below
dependencies:
  build:
    gcc:
    git:
    this-package-does-not-exist-and-should-fail:  # Line 18 - intentionally bad package
    make:

sources:
  hello:
    inline:
      file:
        contents: |
          #!/bin/bash
          echo "Hello World"

build:
  steps:
    - command: echo "This build will fail during dependency installation"

artifacts:
  binaries:
    hello: {}`

		spec, sourceMapInfo, err := LoadSpecWithSourceMap("sourcemap-test-bad-builddep.yml", []byte(fixtureContent))
		require.NoError(t, err)
		assert.NotNil(t, spec)
		assert.NotNil(t, sourceMapInfo)

		// The key insight: when a target implementation encounters this bad dependency,
		// it can use sourceMapInfo.LocationConstraint(&state, "dependencies.build.this-package-does-not-exist-and-should-fail")
		// to add source location information to the LLB operation that fails.
		// This will cause BuildKit to report the error with the exact YAML line/column.
		
		dummyState := llb.Image("alpine")
		badDepConstraints := sourceMapInfo.LocationConstraint(&dummyState, "dependencies.build.this-package-does-not-exist-and-should-fail")
		assert.NotNil(t, badDepConstraints, "Should be able to create constraints for the bad dependency")
		
		t.Logf("Source map info created for test fixture. Target implementations can now use:")
		t.Logf("  constraints := dalec.GetSourceMapConstraintsForPath(ctx, &state, \"dependencies.build.this-package-does-not-exist-and-should-fail\")")
		t.Logf("  llbOp := llb.Image(\"base\").Run(llb.Shlex(\"apt install this-package-does-not-exist-and-should-fail\")).With(constraints)")
		t.Logf("When this operation fails, BuildKit will report the error with source location pointing to the YAML.")
	})
}