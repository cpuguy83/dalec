package dalec

import (
	"context"

	"github.com/moby/buildkit/client/llb"
)

// SourceMapConstraints provides a convenient way to apply source map constraints
// to LLB operations based on YAML paths
type SourceMapConstraints struct {
	ctx context.Context
}

// NewSourceMapConstraints creates a new SourceMapConstraints helper
func NewSourceMapConstraints(ctx context.Context) *SourceMapConstraints {
	return &SourceMapConstraints{ctx: ctx}
}

// ForSource returns constraints for a specific source
func (smc *SourceMapConstraints) ForSource(sourceName string) llb.ConstraintsOpt {
	yamlPath := "sources." + sourceName
	return GetSourceMapConstraintsForPath(smc.ctx, nil, yamlPath)
}

// ForBuildDependencies returns constraints for build dependencies
func (smc *SourceMapConstraints) ForBuildDependencies() llb.ConstraintsOpt {
	return GetSourceMapConstraintsForPath(smc.ctx, nil, "dependencies.build")
}

// ForRuntimeDependencies returns constraints for runtime dependencies
func (smc *SourceMapConstraints) ForRuntimeDependencies() llb.ConstraintsOpt {
	return GetSourceMapConstraintsForPath(smc.ctx, nil, "dependencies.runtime")
}

// ForTestDependencies returns constraints for test dependencies
func (smc *SourceMapConstraints) ForTestDependencies() llb.ConstraintsOpt {
	return GetSourceMapConstraintsForPath(smc.ctx, nil, "dependencies.test")
}

// ForBuildStep returns constraints for a specific build step
func (smc *SourceMapConstraints) ForBuildStep(stepIndex int) llb.ConstraintsOpt {
	yamlPath := GetBuildStepPath(stepIndex)
	return GetSourceMapConstraintsForPath(smc.ctx, nil, yamlPath)
}

// ForTest returns constraints for a specific test
func (smc *SourceMapConstraints) ForTest(testIndex int) llb.ConstraintsOpt {
	yamlPath := GetTestPath(testIndex)
	return GetSourceMapConstraintsForPath(smc.ctx, nil, yamlPath)
}

// ForTestStep returns constraints for a specific test step
func (smc *SourceMapConstraints) ForTestStep(testIndex, stepIndex int) llb.ConstraintsOpt {
	yamlPath := GetTestStepPath(testIndex, stepIndex)
	return GetSourceMapConstraintsForPath(smc.ctx, nil, yamlPath)
}

// ForPatch returns constraints for patches of a specific source
func (smc *SourceMapConstraints) ForPatch(sourceName string) llb.ConstraintsOpt {
	yamlPath := GetPatchPath(sourceName)
	return GetSourceMapConstraintsForPath(smc.ctx, nil, yamlPath)
}

// ForDependency returns constraints for a specific dependency
func (smc *SourceMapConstraints) ForDependency(depType, packageName string) llb.ConstraintsOpt {
	yamlPath := GetDependencyPath(depType, packageName)
	return GetSourceMapConstraintsForPath(smc.ctx, nil, yamlPath)
}

// Example usage in a target implementation:
//
//   func BuildPackage(ctx context.Context, spec *dalec.Spec, ...) {
//       sourceMapConstraints := dalec.NewSourceMapConstraints(ctx)
//       
//       // Apply source map constraints to build dependency installation
//       worker = worker.Run(
//           dalec.WithConstraints(opts...),
//           sourceMapConstraints.ForBuildDependencies(),
//           InstallBuildDeps(...),
//       ).Root()
//       
//       // Apply source map constraints to individual build steps
//       for i, step := range spec.Build.Steps {
//           worker = worker.Run(
//               dalec.WithConstraints(opts...),
//               sourceMapConstraints.ForBuildStep(i),
//               BuildStep(step),
//           ).Root()
//       }
//   }