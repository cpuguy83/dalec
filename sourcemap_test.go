package dalec

import (
	"context"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/stretchr/testify/require"
)

func TestSourceMapIntegration(t *testing.T) {
	yamlContent := `name: test-spec
sources:
  main:
    git:
      url: https://github.com/example/repo
dependencies:
  build:
    - make
    - gcc
build:
  steps:
    - command: make build
tests:
  - name: unit-tests
    steps:
      - command: make test
`

	// Test NewSourceMapInfo
	sourceMapInfo, err := NewSourceMapInfo("test.yaml", []byte(yamlContent))
	require.NoError(t, err)
	require.NotNil(t, sourceMapInfo)
	require.Equal(t, "test.yaml", sourceMapInfo.Filename)
	require.Equal(t, "yaml", sourceMapInfo.Language)
	require.NotEmpty(t, sourceMapInfo.Positions)

	// Test context integration
	ctx := context.Background()
	ctx = WithSourceMapContext(ctx, sourceMapInfo)

	retrievedSourceMap := SourceMapFromContext(ctx)
	require.NotNil(t, retrievedSourceMap)
	require.Equal(t, sourceMapInfo.Filename, retrievedSourceMap.Filename)

	// Test source map constraint generation
	state := llb.Image("alpine:latest")
	constraint := GetSourceMapConstraintsForPath(ctx, &state, "sources.main")
	require.NotNil(t, constraint)

	// Test with empty context
	emptyCtx := context.Background()
	emptyConstraint := GetSourceMapConstraintsForPath(emptyCtx, &state, "sources.main")
	require.NotNil(t, emptyConstraint) // Should return no-op constraint
}

func TestSourceMapYAMLParsing(t *testing.T) {
	yamlContent := `name: test
sources:
  main:
    git:
      url: https://example.com
build:
  steps:
    - command: echo hello
`

	sourceMapInfo, err := NewSourceMapInfo("test.yaml", []byte(yamlContent))
	require.NoError(t, err)
	require.NotNil(t, sourceMapInfo)

	// Test that positions were extracted
	require.NotEmpty(t, sourceMapInfo.Positions)

	// Test position range retrieval
	pos := sourceMapInfo.GetPositionRange("sources.main")
	if pos != nil {
		require.Greater(t, pos.Start.Line, int32(0))
		require.GreaterOrEqual(t, pos.Start.Character, int32(0))
	}
}