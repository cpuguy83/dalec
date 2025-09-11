package dalec

import (
	"context"
	"fmt"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
)

type sourceMapContextKey struct{}

type SourceMapInfo struct {
	Filename  string
	Language  string
	Data      []byte
	Positions map[string]*pb.Range
}

func WithSourceMapContext(ctx context.Context, sourceMapInfo *SourceMapInfo) context.Context {
	return context.WithValue(ctx, sourceMapContextKey{}, sourceMapInfo)
}

func SourceMapFromContext(ctx context.Context) *SourceMapInfo {
	if sourceMapInfo, ok := ctx.Value(sourceMapContextKey{}).(*SourceMapInfo); ok {
		return sourceMapInfo
	}
	return nil
}

// NewSourceMapInfo creates a new SourceMapInfo from YAML data
func NewSourceMapInfo(filename string, data []byte) (*SourceMapInfo, error) {
	parsed, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("error parsing yaml for source map: %w", err)
	}

	positions := make(map[string]*pb.Range)

	if len(parsed.Docs) > 0 && parsed.Docs[0].Body != nil {
		extractPositions(parsed.Docs[0].Body, positions, "")
	}

	return &SourceMapInfo{
		Filename:  filename,
		Language:  "yaml",
		Data:      data,
		Positions: positions,
	}, nil
}

// extractPositions recursively extracts position information from YAML AST nodes
func extractPositions(node ast.Node, positions map[string]*pb.Range, prefix string) {
	if node == nil {
		return
	}

	switch n := node.(type) {
	case *ast.MappingNode:
		for _, mapVal := range n.Values {
			if mapVal.Key != nil {
				if keyNode, ok := mapVal.Key.(*ast.StringNode); ok {
					key := keyNode.Value
					fullPath := key
					if prefix != "" {
						fullPath = prefix + "." + key
					}

					// For command fields, use the value's position (which includes the multi-line content)
					// instead of the key's position
					if key == "command" && mapVal.Value != nil {
						positions[fullPath] = nodeToRange(mapVal.Value)
					} else {
						positions[fullPath] = nodeToRange(mapVal.Key)
					}

					// Recursively process the value with the new path
					if mapVal.Value != nil {
						extractPositions(mapVal.Value, positions, fullPath)
					}
				}
			}
		}
	case *ast.SequenceNode:
		for i, val := range n.Values {
			indexPath := fmt.Sprintf("%s[%d]", prefix, i)
			positions[indexPath] = nodeToRange(val)
			extractPositions(val, positions, indexPath)
		}
	}
}

// nodeToRange converts an AST node to a protobuf Range
func nodeToRange(node ast.Node) *pb.Range {
	if node == nil {
		return &pb.Range{
			Start: &pb.Position{Line: 1, Character: 1},
			End:   &pb.Position{Line: 1, Character: 1},
		}
	}

	pos := node.GetToken().Position
	start := &pb.Position{
		Line:      int32(pos.Line),
		Character: int32(pos.Column),
	}

	// For multi-line content, calculate the end position based on the content
	content := node.String()
	lines := strings.Split(content, "\n")
	endLine := pos.Line + len(lines) - 1
	var endChar int
	if len(lines) > 1 {
		// Multi-line: end character is the length of the last line
		endChar = len(lines[len(lines)-1])
	} else {
		// Single line: end character is start + content length
		endChar = pos.Column + len(content)
	}

	return &pb.Range{
		Start: start,
		End: &pb.Position{
			Line:      int32(endLine),
			Character: int32(endChar),
		},
	}
}

// GetSourceMapConstraintsForPath returns source map constraints for a specific path
func GetSourceMapConstraintsForPath(ctx context.Context, state *llb.State, yamlPath string) llb.ConstraintsOpt {
	sourceMapInfo := SourceMapFromContext(ctx)
	if sourceMapInfo == nil {
		return ConstraintsOptFunc(func(c *llb.Constraints) {})
	}

	constraint := sourceMapInfo.LocationConstraint(state, yamlPath)
	// Return empty constraint if no position found
	if constraint == nil {
		return ConstraintsOptFunc(func(c *llb.Constraints) {})
	}
	return constraint
}

func GetMergedSourceMapConstraintsForPaths(ctx context.Context, state *llb.State, yamlPaths []string) llb.ConstraintsOpt {
	sourceMapInfo := SourceMapFromContext(ctx)
	if sourceMapInfo == nil {
		return ConstraintsOptFunc(func(c *llb.Constraints) {})
	}

	var allRanges []*pb.Range
	for _, yamlPath := range yamlPaths {
		if pos, ok := sourceMapInfo.Positions[yamlPath]; ok {
			allRanges = append(allRanges, pos)
		}
	}

	if len(allRanges) == 0 {
		return ConstraintsOptFunc(func(c *llb.Constraints) {})
	}

	// Create a source map and location constraint with all ranges
	sourceMap := llb.NewSourceMap(state, sourceMapInfo.Filename, sourceMapInfo.Language, sourceMapInfo.Data)
	return sourceMap.Location(allRanges)
}

func (smi *SourceMapInfo) LocationConstraint(state *llb.State, yamlPath string) llb.ConstraintsOpt {
	// Look up the actual position for this path
	pos, ok := smi.Positions[yamlPath]
	if !ok {
		// Return nil if no position found so constraint won't be added
		return nil
	}

	// Create a source map and location constraint properly
	sourceMap := llb.NewSourceMap(state, smi.Filename, smi.Language, smi.Data)
	locationConstraint := sourceMap.Location([]*pb.Range{pos})

	// Return the location constraint which should set SourceLocations
	return locationConstraint
}

// GetPositionRange returns the position range for a given path
func (smi *SourceMapInfo) GetPositionRange(path string) *pb.Range {
	if smi.Positions == nil {
		return nil
	}
	return smi.Positions[path]
}

// CreateSourceMap creates a BuildKit source map from this SourceMapInfo
func (smi *SourceMapInfo) CreateSourceMap(state *llb.State) *llb.SourceMap {
	return llb.NewSourceMap(state, smi.Filename, smi.Language, smi.Data)
}

func GetBuildStepPath(stepIndex int) string {
	return fmt.Sprintf("$.build.steps[%d]", stepIndex)
}

func GetTestPath(testIndex int) string {
	return fmt.Sprintf("tests[%d]", testIndex)
}

func GetTestStepPath(testIndex, stepIndex int) string {
	return fmt.Sprintf("tests[%d].steps[%d]", testIndex, stepIndex)
}

func GetDependencyPath(depType, packageName string) string {
	return fmt.Sprintf("$.dependencies.%s.%s", depType, packageName)
}

func GetPatchPath(sourceName string) string {
	return fmt.Sprintf("$.sources.%s.patches", sourceName)
}
