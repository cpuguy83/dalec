package dalec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceMapConstraints(t *testing.T) {
	yamlContent := `name: test-spec
sources:
  main:
    git:
      url: https://github.com/example/repo
  config:
    inline:
      file:
        contents: "test"
dependencies:
  build:
    - make
    - gcc
  runtime:
    - libc
build:
  steps:
    - command: make build
    - command: make install
tests:
  - name: unit-tests
    steps:
      - command: make test
patches:
  main:
    - path: fix.patch
`

	sourceMapInfo, err := NewSourceMapInfo("test.yaml", []byte(yamlContent))
	require.NoError(t, err)

	ctx := WithSourceMapContext(context.Background(), sourceMapInfo)
	smc := NewSourceMapConstraints(ctx)

	// Test source constraints
	sourceConstraint := smc.ForSource("main")
	require.NotNil(t, sourceConstraint)

	configConstraint := smc.ForSource("config")
	require.NotNil(t, configConstraint)

	// Test dependency constraints
	buildDepsConstraint := smc.ForBuildDependencies()
	require.NotNil(t, buildDepsConstraint)

	runtimeDepsConstraint := smc.ForRuntimeDependencies()
	require.NotNil(t, runtimeDepsConstraint)

	testDepsConstraint := smc.ForTestDependencies()
	require.NotNil(t, testDepsConstraint)

	// Test build step constraints
	step0Constraint := smc.ForBuildStep(0)
	require.NotNil(t, step0Constraint)

	step1Constraint := smc.ForBuildStep(1)
	require.NotNil(t, step1Constraint)

	// Test test constraints
	test0Constraint := smc.ForTest(0)
	require.NotNil(t, test0Constraint)

	testStep0Constraint := smc.ForTestStep(0, 0)
	require.NotNil(t, testStep0Constraint)

	// Test patch constraints
	patchConstraint := smc.ForPatch("main")
	require.NotNil(t, patchConstraint)

	// Test specific dependency constraints
	depConstraint := smc.ForDependency("build", "make")
	require.NotNil(t, depConstraint)
}

func TestSourceMapConstraintsWithEmptyContext(t *testing.T) {
	// Test with empty context (no source map info)
	ctx := context.Background()
	smc := NewSourceMapConstraints(ctx)

	// All constraints should return no-op constraints but not be nil
	sourceConstraint := smc.ForSource("main")
	require.NotNil(t, sourceConstraint)

	buildDepsConstraint := smc.ForBuildDependencies()
	require.NotNil(t, buildDepsConstraint)

	step0Constraint := smc.ForBuildStep(0)
	require.NotNil(t, step0Constraint)
}