package rpm

import (
	"errors"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
)

type CacheInfo struct {
	TargetKey string
	Caches    []dalec.CacheConfig
}

// Builds an RPM and source RPM from a spec
//
// `topDir` is the rpmbuild top directory which should contain the SOURCES and SPECS directories along with any other necessary files
//
// `workerImg` is the image to use for the build
// It is expected to have rpmbuild and any other necessary build dependencies installed
//
// `specPath` is the path to the spec file to build relative to `topDir`
// `targetPlatform` is optional and, when set, determines the rpmbuild --target flag.
func Build(topDir, workerImg llb.State, specPath string, caches CacheInfo, targetPlatform ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Build RPM"))
	cmd := `rpmbuild --define "_topdir /build/top" --define "_srcrpmdir /build/out/SRPMS" --define "_rpmdir /build/out/RPMS" --buildroot /build/tmp/work`
	if targetPlatform.Architecture != "" {
		arch, err := rpmTargetFromPlatform(targetPlatform)
		if err != nil {
			return llb.Scratch(), err
		}
		cmd += " --target " + arch
	}
	cmd += " -ba " + specPath
	st := workerImg.Run(
		// some notes on these args:
		//  - _topdir is the directory where rpmbuild will look for SOURCES and SPECS
		//  - _srcrpmdir is the directory where rpmbuild will put the source RPM
		//  - _rpmdir is the directory where rpmbuild will put the RPM
		//  - --buildroot is the work directory where rpmbuild will build the RPM
		//  - -ba tells rpmbuild to build the source and binary RPMs
		//
		// TODO(cpuguy83): specPath would ideally never change.
		// We don't want to have to re-run this step just because the path changed, this should be based on inputs only (ie the content of the rpm spec, sources, etc)
		// The path should not be an input.
		dalec.ShArgs(cmd),
		llb.AddMount("/build/top", topDir),
		llb.AddMount("/build/tmp", llb.Scratch(), llb.Tmpfs()),
		llb.Dir("/build/top"),
		dalec.WithConstraints(opts...),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			opts := []dalec.CacheConfigOption{
				dalec.WithCacheDirConstraints(opts...),
			}
			for _, cache := range caches.Caches {
				cache.ToRunOption(workerImg, caches.TargetKey, opts...).SetRunOption(ei)
			}
		}),
	).
		AddMount("/build/out", llb.Scratch())
	return st, nil
}

var rpmArchMap = map[string]string{
	"amd64":   "x86_64",
	"arm64":   "aarch64",
	"ppc64le": "ppc64le",
	"s390x":   "s390x",
	"riscv64": "riscv64",
}

var rpmArchVariantMap = map[string]map[string]string{
	"arm": {
		"v7": "armv7hl",
	},
}

func rpmTargetFromPlatform(p ocispecs.Platform) (string, error) {
	if p.Architecture == "" {
		return "", fmt.Errorf("target platform missing architecture")
	}

	if variants, ok := rpmArchVariantMap[p.Architecture]; ok {
		if p.Variant == "" {
			return "", fmt.Errorf("target variant required for architecture %q", p.Architecture)
		}
		if mapped, ok := variants[p.Variant]; ok {
			return mapped, nil
		}
		return "", fmt.Errorf("unsupported variant %q for architecture %q", p.Variant, p.Architecture)
	}

	if mapped, ok := rpmArchMap[p.Architecture]; ok {
		return mapped, nil
	}

	return "", fmt.Errorf("unsupported target architecture %q", p.Architecture)
}

var errMissingRequiredField = errors.New("missing required field")

// ValidateSpec makes sure all the necessary fields are present in the spec to make rpmbuild work
// This validation is specific to rpmbuild.
func ValidateSpec(spec *dalec.Spec) (out error) {
	if spec.Name == "" {
		out = errors.Join(out, fmt.Errorf("%w: name", errMissingRequiredField))
	}
	if spec.Version == "" {
		out = errors.Join(out, fmt.Errorf("%w: version", errMissingRequiredField))
	}
	if spec.Revision == "" {
		out = errors.Join(out, fmt.Errorf("%w: revision", errMissingRequiredField))
	}
	if spec.Description == "" {
		out = errors.Join(out, fmt.Errorf("%w: description", errMissingRequiredField))
	}
	if spec.License == "" {
		out = errors.Join(out, fmt.Errorf("%w: license", errMissingRequiredField))
	}
	return out
}
