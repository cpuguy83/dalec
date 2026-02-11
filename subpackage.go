package dalec

import (
	goerrors "errors"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// SubPackage defines a supplemental package that shares the spec's build output
// but has its own artifact selection, metadata, and dependency declarations.
//
// Supplemental packages inherit version, revision, license, vendor, website,
// sources, build, patches, and changelog from the parent spec. They cannot
// override these fields.
//
// This maps to RPM subpackages (%package) and Debian binary packages
// (additional Package stanzas in debian/control).
type SubPackage struct {
	// Name is the full package name override. If empty, the package name
	// defaults to "<parent>-<key>" where <key> is the map key under which
	// this SubPackage appears.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`

	// Description is the package description for this supplemental package.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Artifacts is the artifact selection for this supplemental package.
	// Each supplemental package's artifacts are self-contained — no inheritance
	// from the primary package.
	Artifacts *Artifacts `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// Dependencies specifies the runtime dependencies for this supplemental
	// package. Only runtime dependencies are supported — build dependencies are
	// shared from the parent spec.
	Dependencies *SubPackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`

	// Conflicts is the list of packages that conflict with this supplemental package.
	Conflicts PackageDependencyList `yaml:"conflicts,omitempty" json:"conflicts,omitempty"`
	// Provides is the list of things that this supplemental package provides.
	Provides PackageDependencyList `yaml:"provides,omitempty" json:"provides,omitempty"`
	// Replaces is the list of packages that are replaced by this supplemental package.
	Replaces PackageDependencyList `yaml:"replaces,omitempty" json:"replaces,omitempty"`
}

// SubPackageDependencies holds the dependency declarations available to
// supplemental packages. Only runtime dependencies are allowed since build
// dependencies are shared from the parent spec.
type SubPackageDependencies struct {
	// Runtime is the list of packages required to install/run this supplemental package.
	Runtime PackageDependencyList `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	// Recommends is the list of packages recommended to install with this supplemental package.
	Recommends PackageDependencyList `yaml:"recommends,omitempty" json:"recommends,omitempty"`
}

// ResolvedName returns the effective package name for this supplemental package.
// If a Name override is set, it is returned. Otherwise, the name is
// "<parentName>-<key>".
func (sp *SubPackage) ResolvedName(parentName, key string) string {
	if sp.Name != "" {
		return sp.Name
	}
	return parentName + "-" + key
}

func (sp *SubPackage) validate() error {
	if sp == nil {
		return nil
	}

	var errs []error

	if sp.Artifacts != nil {
		if err := sp.Artifacts.validate(); err != nil {
			errs = append(errs, errors.Wrap(err, "artifacts"))
		}
	}

	return goerrors.Join(errs...)
}

func (sp *SubPackage) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if sp == nil {
		return nil
	}

	var errs []error

	if sp.Dependencies != nil {
		if err := sp.Dependencies.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrap(err, "dependencies"))
		}
	}

	for k, v := range sp.Provides {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "provides %s version %d", k, i))
				continue
			}
			sp.Provides[k].Version[i] = updated
		}
	}

	for k, v := range sp.Replaces {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "replaces %s version %d", k, i))
				continue
			}
			sp.Replaces[k].Version[i] = updated
		}
	}

	for k, v := range sp.Conflicts {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "conflicts %s version %d", k, i))
				continue
			}
			sp.Conflicts[k].Version[i] = updated
		}
	}

	return goerrors.Join(errs...)
}

func (sp *SubPackage) fillDefaults() {
	// Currently no defaults to fill for SubPackage.
	// This is a placeholder for future use.
}

func (d *SubPackageDependencies) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if d == nil {
		return nil
	}

	var errs []error

	for k, v := range d.Runtime {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "runtime version %s", ver))
				continue
			}
			v.Version[i] = updated
		}
		d.Runtime[k] = v
	}

	for k, v := range d.Recommends {
		for i, ver := range v.Version {
			updated, err := expandArgs(lex, ver, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "recommends version %s", ver))
				continue
			}
			v.Version[i] = updated
		}
		d.Recommends[k] = v
	}

	return goerrors.Join(errs...)
}

func (d *SubPackageDependencies) GetRuntime() PackageDependencyList {
	if d == nil {
		return nil
	}
	return d.Runtime
}

func (d *SubPackageDependencies) GetRecommends() PackageDependencyList {
	if d == nil {
		return nil
	}
	return d.Recommends
}
