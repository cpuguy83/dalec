package dalec

import (
	goerrors "errors"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// ImageDefinition defines a named container image configuration.
// Each entry in the spec's Images map represents one container image
// that can be built from the spec's packages.
//
// ImageDefinition embeds ImageConfig for all the standard image configuration
// fields (entrypoint, cmd, env, labels, post, bases, etc.) and adds
// image-specific fields for package selection and testing.
type ImageDefinition struct {
	ImageConfig `yaml:",inline" json:",inline"`

	// Packages specifies which packages from this spec to install in the
	// container image. Package names refer to the installed package names
	// (e.g., "foo", "foo-contrib"), not the map keys from the packages field.
	//
	// If omitted, all packages the spec produces (primary + all supplemental)
	// are installed.
	// If present, only the listed packages are installed — the primary package
	// is NOT implicitly included.
	Packages []string `yaml:"packages,omitempty" json:"packages,omitempty"`

	// Tests are image-specific tests that are appended to the root-level
	// and target-level tests when building this named image.
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`
}

func (d *ImageDefinition) validate() error {
	if d == nil {
		return nil
	}

	var errs []error

	if err := d.ImageConfig.validate(); err != nil {
		errs = append(errs, err)
	}

	for _, t := range d.Tests {
		if err := t.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "test %s", t.Name))
		}
	}

	return goerrors.Join(errs...)
}

func (d *ImageDefinition) fillDefaults() {
	if d == nil {
		return
	}
	d.ImageConfig.fillDefaults()
}

func (d *ImageDefinition) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if d == nil {
		return nil
	}

	var errs []error

	if err := d.ImageConfig.processBuildArgs(lex, args, allowArg); err != nil {
		errs = append(errs, err)
	}

	for _, t := range d.Tests {
		if err := t.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}

	return goerrors.Join(errs...)
}
