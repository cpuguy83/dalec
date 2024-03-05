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
	SourceGit `yaml:",inline"`
}

var errNoGoWorker = errors.New("worker image for fetching go sources not set: this is a bug in the buildkit frontend")

// AsState returns the state of the go module and its dependencies
//
// TODO: Support passing in a base go mods llb.State?
// TODO: This could allow for the go mods to be shared between multiple go modules
func (s *SourceGoModule) AsState(worker llb.State, opts ...llb.ConstraintsOpt) (mod, deps llb.State, _ error) {
	cachePath := "/go/pkg/mod"

	mod, err := s.SourceGit.AsState(opts...)
	if err != nil {
		return llb.Scratch(), llb.Scratch(), err
	}

	work := worker.
		With(llb.AddEnv("GOMODCACHE", cachePath)).
		Run(
			shArgs("go mod download"),
			llb.Dir("/src"),
			llb.AddMount("/src", mod),
			WithConstraints(opts...),
		)

	deps = work.AddMount(cachePath, llb.Scratch())

	return mod, deps, nil
}
