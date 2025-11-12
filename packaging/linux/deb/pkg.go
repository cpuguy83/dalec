package deb

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

const (
	// Unique name that would not normally be in the spec
	// This will get used to create the source tar for go module deps
	gomodsName = "xxxdalecGomodsInternal"
	// Unique name that would not normally be in the spec
	// This will get used to create the source tar for cargo deps
	cargohomeName = "xxxdalecCargoHomeInternal"
	// Unique name that would not normally be in the spec
	// This will get used to create the source tar for pip deps
	pipDepsName = "xxxdalecPipDepsInternal"
)

var debArchMap = map[string]string{
	"amd64":   "amd64",
	"arm64":   "arm64",
	"ppc64le": "ppc64el",
	"s390x":   "s390x",
	"riscv64": "riscv64",
}

var debArchVariantMap = map[string]map[string]string{
	"arm": {
		"v7": "armhf",
	},
}

func mountSources(sources map[string]llb.State, dir string, mod func(string) string) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		for key, src := range sources {
			if mod != nil {
				key = mod(key)
			}
			llb.AddMount(filepath.Join(dir, key), src, llb.SourcePath(key)).SetRunOption(ei)
		}
	})
}

var errMissingRequiredField = fmt.Errorf("missing required field")

func dpkgHostArchFlag(targetPlatform ocispecs.Platform) (string, error) {
	if targetPlatform.Architecture == "" {
		return "", nil
	}
	arch, err := debArchFromPlatform(targetPlatform)
	if err != nil {
		return "", err
	}
	return " --host-arch " + arch, nil
}

func debArchFromPlatform(p ocispecs.Platform) (string, error) {
	if p.Architecture == "" {
		return "", fmt.Errorf("target platform missing architecture")
	}

	if variants, ok := debArchVariantMap[p.Architecture]; ok {
		if p.Variant == "" {
			return "", fmt.Errorf("target variant required for architecture %q", p.Architecture)
		}
		if mapped, ok := variants[p.Variant]; ok {
			return mapped, nil
		}
		return "", fmt.Errorf("unsupported variant %q for architecture %q", p.Variant, p.Architecture)
	}

	if mapped, ok := debArchMap[p.Architecture]; ok {
		return mapped, nil
	}

	return "", fmt.Errorf("unsupported target architecture %q", p.Architecture)
}

func validateSpec(spec *dalec.Spec) error {
	if spec.Packager == "" {
		return errors.Wrap(errMissingRequiredField, "packager")
	}
	return nil
}

// Dalec patches apply directly to each individual source tree, e.g. `cd <src>; patch ...`
// Debian applies patches from 1 directory up from the source tree (e.g. no `cd` as above).
// As such the patch files are not formatted correctly for Debian's build tooling.
// Here we generate a single patch file that generates the correct format.
//
// This way dpkg-source can automatically apply patches for us, and informs
// the caller of the patches applied and is generally just more inline with
// a typical deb build.
//
// This is using git instead of raw `diff` or other standalone tooling because only git appears to preserve permissions for new files.
// As an example, if patch adds a new file with mode +x, `diff` will not see the permissions for that new file.
func createPatches(spec *dalec.Spec, sources map[string]llb.State, worker llb.State, dr llb.State, opts ...llb.ConstraintsOpt) llb.State {
	patches := llb.Scratch()
	if len(spec.Patches) > 0 {
		patchesMountInput := llb.Scratch().
			File(llb.Mkfile("dalec-changes.patch", 0o600, patchHeader))

		patches = worker.
			Run(dalec.ShArgs("set -e; git config --global user.email phony; git config --global user.name Dalec")).
			Run(
				dalec.ShArgs("set -e; git init .; git add .; git commit -m 'Initial commit'; \"${DEBIAN_DIR}/dalec/patch.sh\"; git add .; git commit -m 'With patch'; git diff HEAD~1 >> /work/out/dalec-changes.patch; echo 'dalec-changes.patch' > /work/out/series"),
				llb.Dir("/work/sources"),
				mountSources(sources, "/work/sources", nil),
				// DEBIAN_DIR is used by the patch script to find the debian directory where we actually have the patches
				llb.AddEnv("DEBIAN_DIR", "/work/debian"),
				llb.AddMount("/work/debian", dr, llb.SourcePath("debian"), llb.Readonly),
				dalec.WithConstraints(opts...),
			).AddMount("/work/out", patchesMountInput)
	}

	return patches
}

func nestState(dest string) llb.StateOption {
	return func(in llb.State) llb.State {
		return llb.Scratch().File(
			llb.Mkdir(dest, 0o755, llb.WithParents(true)),
		).File(
			llb.Copy(in, "/", dest, dalec.WithDirContentsOnly()),
		)
	}
}

func SourcePackage(ctx context.Context, sOpt dalec.SourceOpts, worker llb.State, spec *dalec.Spec, targetKey, distroVersionID string, cfg SourcePkgConfig, targetPlatform ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	if err := validateSpec(spec); err != nil {
		return llb.Scratch(), err
	}
	hostFlag, err := dpkgHostArchFlag(targetPlatform)
	if err != nil {
		return llb.Scratch(), err
	}
	dr, err := Debroot(ctx, sOpt, spec, worker, llb.Scratch(), targetKey, "", distroVersionID, cfg, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	sources, err := dalec.Sources(spec, sOpt)
	if err != nil {
		return llb.Scratch(), err
	}

	gomodSt, err := spec.GomodDeps(sOpt, worker, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error preparing gomod deps")
	}
	if gomodSt != nil {
		st := gomodSt.With(nestState(gomodsName))
		gomodSt = &st
	}

	cargohomeSt, err := spec.CargohomeDeps(sOpt, worker, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error preparing cargohome deps")
	}
	if cargohomeSt != nil {
		st := cargohomeSt.With(nestState(cargohomeName))
		cargohomeSt = &st
	}

	pipDepsSt, err := spec.PipDeps(sOpt, worker, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error preparing pip deps")
	}
	if pipDepsSt != nil {
		st := pipDepsSt.With(nestState(pipDepsName))
		pipDepsSt = &st
	}

	srcsWithNodeMods, err := spec.NodeModDeps(sOpt, worker, opts...)
	if err != nil {
		return llb.Scratch(), errors.Wrap(err, "error preparing node deps")
	}
	sorted := dalec.SortMapKeys(srcsWithNodeMods)

	for _, key := range sorted {
		sources[key] = srcsWithNodeMods[key]
	}

	if gomodSt != nil {
		sources[gomodsName] = *gomodSt
	}

	if cargohomeSt != nil {
		sources[cargohomeName] = *cargohomeSt
	}

	if pipDepsSt != nil {
		sources[pipDepsName] = *pipDepsSt
	}

	patches := createPatches(spec, sources, worker, dr, opts...)

	buildpkg := fmt.Sprintf("set -e; dpkg-buildpackage%s -S -us -uc; mkdir -p /tmp/out; cp -r /work/%s_%s* /tmp/out", hostFlag, spec.Name, spec.Version)
	work := worker.Run(
		dalec.ShArgs(buildpkg),
		llb.Dir("/work/pkg"),
		llb.AddMount("/work/pkg/debian", dr, llb.SourcePath("debian")), // This cannot be readonly because the debian directory gets modified by dpkg-buildpackage
		llb.AddMount("/work/pkg/debian/patches", patches, llb.Readonly),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			debSources := TarDebSources(worker, spec, sources, "src.tar.gz", sOpt, opts...)
			llb.AddMount("/work/"+spec.Name+"_"+spec.Version+".orig.tar.gz", debSources, llb.SourcePath("src.tar.gz")).SetRunOption(ei)
		}),
		dalec.WithConstraints(opts...),
	)

	return work.AddMount("/tmp/out", llb.Scratch()), nil
}

func BuildDebBinaryOnly(worker llb.State, spec *dalec.Spec, debroot llb.State, distroVersionID string, targetPlatform ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	dirName := filepath.Join("/work", spec.Name+"_"+spec.Version+"-"+spec.Revision)
	hostFlag, err := dpkgHostArchFlag(targetPlatform)
	if err != nil {
		return llb.Scratch(), err
	}
	cmd := fmt.Sprintf("set -e; dpkg-buildpackage%s -b -uc -us; mkdir -p /tmp/out; cp ../*.deb /tmp/out", hostFlag)
	st := worker.
		Run(
			dalec.ShArgs(cmd),
			llb.Dir(dirName),
			llb.AddMount(dirName, debroot),
			dalec.WithConstraints(opts...),
		).AddMount("/tmp/out", llb.Scratch())

	return st, nil
}

func BuildDeb(worker llb.State, spec *dalec.Spec, srcPkg llb.State, distroVersionID string, targetPlatform ocispecs.Platform, opts ...llb.ConstraintsOpt) (llb.State, error) {
	dirName := filepath.Join("/work", spec.Name+"_"+spec.Version+"-"+spec.Revision)
	buildRootRel := spec.Name + "-" + spec.Version
	hostFlag, err := dpkgHostArchFlag(targetPlatform)
	if err != nil {
		return llb.Scratch(), err
	}
	cmd := fmt.Sprintf("set -e; dpkg-source -x ./*.dsc; cd %s; dpkg-buildpackage%s -b -uc -us; mkdir -p /tmp/out; cp ../*.deb /tmp/out", buildRootRel, hostFlag)
	st := worker.
		Run(
			dalec.ShArgs(cmd),
			llb.Dir(dirName),
			llb.AddMount(dirName, srcPkg),
			dalec.WithConstraints(opts...),
			dalec.RunOptFunc(func(ei *llb.ExecInfo) {
				opts := []dalec.CacheConfigOption{
					dalec.WithCacheDirConstraints(opts...),
				}
				for _, cache := range spec.Build.Caches {
					cache.ToRunOption(worker, distroVersionID, opts...).SetRunOption(ei)
				}
			}),
		).AddMount("/tmp/out", llb.Scratch())

	return dalec.MergeAtPath(llb.Scratch(), []llb.State{st, srcPkg}, "/"), nil
}

func TarDebSources(work llb.State, spec *dalec.Spec, srcStates map[string]llb.State, dest string, sOpts dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Prepare debian sources"))
	states := make([]llb.State, 0, len(srcStates))
	for key, state := range srcStates {
		src, ok := spec.Sources[key]

		// If the source is not explicitly listed in the spec sources, assume it is a directory (e.g., for gomod dependencies)
		isDir := true
		if ok {
			isDir = src.IsDir()
		}

		// If the tar contains only a single directory, dpkg will extract its contents directly into the root directory.
		// So nest it an extra step
		if len(srcStates) == 1 && isDir {
			state = state.With(nestState(key))
		}
		states = append(states, state)
	}

	st := dalec.MergeAtPath(llb.Scratch(), states, "/")
	return dalec.Tar(work, st, dest, opts...)
}
