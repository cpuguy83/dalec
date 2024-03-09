package dalec

import (
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

// GomModDepsKey is the key used to store the dependencies of a go module
const GoModDepsKey = "gomods"

// SourceGoModule is a source type that represents a go module.
// Go modules are a special source type as they have extra dependencies to download
type SourceGoModule struct {
	// From is the source to download the main go module from
	// Once the main module is fetched, the dependencies will be fetched from the normal go module resolution
	From Source `json:"from" yaml:"from"`
}

var errNoGoWorker = errors.New("worker image for fetching go sources not set: this is a bug in the buildkit frontend")

// AsState returns the state of the go module and its dependencies
//
// TODO: Support passing in a base go mods llb.State?
// TODO: This could allow for the go mods to be shared between multiple go modules
func (s *SourceGoModule) AsState(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (mod, deps llb.State, _ error) {
	if sOpt.GetSourceWorker == nil {
		return llb.Scratch(), llb.Scratch(), errNoGoWorker
	}

	cachePath := "/go/pkg/mod"
	worker := sOpt.GetSourceWorker(opts...).AddEnv("GOMODCACHE", cachePath)

	mod, _deps, err := s.From.AsState(name, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), llb.Scratch(), err
	}

	if len(_deps) > 0 {
		return llb.Scratch(), llb.Scratch(), errors.New("go module parent sources with depdenencies not supported")
	}

	work := worker.
		Run(
			shArgs("go mod download -x"),
			llb.AddMount("/src", mod, llb.Readonly),
			llb.Dir("/src"),
			WithConstraints(append(opts, ProgressGroup("Download go module dependencies: "+name))...),
		)

	deps = work.AddMount(cachePath, llb.Scratch())

	return mod, deps, nil
}

// RequiresGo returns true if the spec requires go to be able to fetch one or more build sources.
func (s *Spec) RequiresGo() bool {
	for _, src := range s.Sources {
		if src.Gomod != nil {
			return true
		}
	}
	return false
}
