package dalec

import (
	goerrors "errors"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/pkg/errors"
)

// SourceBuild is used to generate source from a DockerFile build, either
// inline or from a local file.
type SourceBuild struct {
	// A source specification to use as the context for the Dockerfile build
	// Unless [Dockerfile] is set, it is expected that the source contains a Dockerfile at [DockerfilePath].
	// Either [Source] or [Dockerfile] must be set, or both.
	Source *Source `yaml:"source,omitempty" json:"source,omitempty"`

	// DockerfilePath is the path to the build file in the build context
	// If not set the default is assumed by buildkit to be `Dockerfile` at the root of the context.
	// If [Dockerfile] is set, this field referes to the path inside the [Dockerfile] source.
	DockerfilePath string `yaml:"dockerfile_path,omitempty" json:"dockerfile_path,omitempty"`

	// Dockerfile is the content of the Dockerfile to use for the build.
	// When this is not set, the Dockerfile is assumed to be in [Source].
	// To customize the path to the Dockerfile withen this source, use [DockerfilePath].
	// Either [Source] or [Dockerfile] must be set, or both.
	Dockerfile *Source `yaml:"dockerfile,omitempty" json:"dockerfile,omitempty"`

	// Target specifies the build target to use.
	// If unset, the default target is determined by the frontend implementation
	// (e.g. the dockerfile frontend uses the last build stage as the default).
	Target string `yaml:"target,omitempty" json:"target,omitempty"`

	// Args are the build args to pass to the build.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

func (s *SourceBuild) validate() (retErr error) {
	if s.Source == nil && s.Dockerfile == nil {
		retErr = goerrors.Join(retErr, fmt.Errorf("build source must have source and/or dockerfile set"))
	}

	if s.Source != nil {
		if s.Source.Build != nil {
			retErr = goerrors.Join(retErr, fmt.Errorf("build sources cannot be recursive"))
		}

		if err := s.Source.validate(); err != nil {
			retErr = goerrors.Join(retErr, errors.Wrap(err, "eror validating build source"))
		}
	}

	if s.Dockerfile != nil {
		if err := s.Dockerfile.validate(); err != nil {
			retErr = goerrors.Join(retErr, errors.Wrap(err, "error validating dockerfile"))
		}
	}

	return
}

func (src *SourceBuild) AsState(name string, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if src.Source == nil && src.Dockerfile == nil {
		return llb.Scratch(), errors.New("build source must have source and/or dockerfile set")
	}

	if src.Source != nil && src.Source.Inline != nil && src.Source.Inline.File != nil {
		name = src.DockerfilePath
		if name == "" {
			name = dockerui.DefaultDockerfileName
		}
	}

	bctx := llb.Scratch()
	if src.Source != nil {
		st, err := src.Source.AsState(name, sOpt, opts...)
		if err != nil {
			return llb.Scratch(), errors.Wrap(err, "error creating build context state")
		}
		bctx = st
	}

	var dockerfile llb.State

	if src.Dockerfile != nil {
		p := src.DockerfilePath
		if p == "" {
			p = dockerui.DefaultDockerfileName
		}

		st, err := src.Dockerfile.AsState(p, sOpt, opts...)
		if err != nil {
			return llb.Scratch(), errors.Wrap(err, "error creating dockerfile state")
		}
		dockerfile = st
	} else {
		dockerfile = bctx
	}

	st, err := sOpt.Forward(bctx, dockerfile, src)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error forwarding build request")
	}

	return st, nil
}
