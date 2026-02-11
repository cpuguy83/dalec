package linux

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type DistroConfig interface {
	// Validate does any distro or packaging-specific validation of a Dalec spec.
	Validate(*dalec.Spec) error

	// Worker returns the worker image for the particular distro
	Worker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State
	SysextWorker(sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) llb.State

	// BuildPkg returns an llb.State containing the built package
	// which the passed in spec describes. This should be composable with
	// BuildContainer(), which can consume the returned state.
	BuildPkg(ctx context.Context,
		client gwclient.Client,
		sOpt dalec.SourceOpts,
		spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.State

	// ExtractPkg consumes an llb.State containing the built package from the
	// given *dalec.Spec, and extracts it in a scratch container, along with any
	// dependencies listed under sysext. The package manager is not used, so no
	// further dependency resolution is performed.
	ExtractPkg(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts,
		spec *dalec.Spec, targetKey string,
		pkgState llb.State, opts ...llb.ConstraintsOpt) llb.State

	// BuildContainer consumes an llb.State containing the built package from the
	// given *dalec.Spec, and installs it in a target container.
	BuildContainer(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts,
		spec *dalec.Spec, targetKey string,
		pkgState llb.State, opts ...llb.ConstraintsOpt) llb.State

	// RunTests runts the tests specified in a dalec spec against a built container, which may be the target container.
	// Some distros may need to pass in a separate worker before mounting the target container.
	RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, ctr llb.State,
		targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption

	// FilterPackages filters the package state to only include the packages
	// with the given names. This is used by named images with a packages field
	// to install only the requested subset of packages.
	//
	// The packageNames are the installed package names (e.g., "foo", "foo-contrib"),
	// not map keys. The spec provides version information needed to construct
	// filename patterns.
	//
	// If packageNames is empty, the original pkgState is returned unmodified
	// (install all packages).
	FilterPackages(pkgState llb.State, spec *dalec.Spec, packageNames []string, opts ...llb.ConstraintsOpt) llb.State
}

func BuildImageConfig(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, platform *ocispecs.Platform, targetKey string) (*dalec.DockerImageSpec, error) {
	bi, err := spec.GetSingleBase(targetKey)
	if err != nil {
		return nil, err
	}

	img, err := resolveConfig(ctx, sOpt, platform, bi)
	if err != nil {
		return nil, err
	}

	if err := dalec.BuildImageConfig(spec, targetKey, img); err != nil {
		return nil, err
	}

	return img, nil
}

// BuildNamedImageConfig resolves the OCI image config for a named image definition.
// It resolves the base image from the named image definition's merge chain,
// then applies the named image's merged ImageConfig on top.
func BuildNamedImageConfig(ctx context.Context, sOpt dalec.SourceOpts, spec *dalec.Spec, platform *ocispecs.Platform, targetKey, imageName string) (*dalec.DockerImageSpec, error) {
	bases := spec.GetNamedImageBases(imageName, targetKey)

	img, err := resolveBaseImageConfig(ctx, sOpt, platform, bases)
	if err != nil {
		return nil, err
	}

	if err := dalec.BuildNamedImageConfig(spec, targetKey, imageName, img); err != nil {
		return nil, err
	}

	return img, nil
}

// resolveBaseImageConfig resolves the OCI config from the provided base images.
// If no bases are provided, it returns a default base image config.
// If more than one base is provided, an error is returned (only single base is supported).
func resolveBaseImageConfig(ctx context.Context, sOpt dalec.SourceOpts, platform *ocispecs.Platform, bases []dalec.BaseImage) (*dalec.DockerImageSpec, error) {
	if len(bases) > 1 {
		return nil, errors.New("multiple image bases, expected only one")
	}
	if len(bases) == 0 {
		return dalec.BaseImageConfig(platform), nil
	}

	bi := &bases[0]
	return resolveConfig(ctx, sOpt, platform, bi)
}

func resolveConfig(ctx context.Context, sOpt dalec.SourceOpts, platform *ocispecs.Platform, bi *dalec.BaseImage) (*dalec.DockerImageSpec, error) {
	if bi == nil {
		return dalec.BaseImageConfig(platform), nil
	}

	dt, err := bi.ResolveImageConfig(ctx, sOpt, sourceresolver.Opt{
		ImageOpt: &sourceresolver.ResolveImageOpt{
			Platform: platform,
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "error resolving base image config")
	}

	var img dalec.DockerImageSpec
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, errors.Wrap(err, "error unmarshalling base image config")
	}
	return &img, nil
}

func HandleContainer(c DistroConfig) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
			if err != nil {
				return nil, nil, err
			}

			// Extract the image name from the remaining target path.
			// After the mux strips the "container" prefix, the remaining
			// target is the named image (e.g., "with-contrib" from
			// "azlinux3/container/with-contrib").
			imageName := client.BuildOpts().Opts["target"]

			var opts []llb.ConstraintsOpt
			opts = append(opts, dalec.ProgressGroup(spec.Name))
			opts = append(opts, dalec.Platform(platform))

			pkgSt, foundPrebuiltPkg := getPrebuiltPackage(ctx, targetKey, client, opts, sOpt)

			// Pre-built package wasn't found so we need to build it.
			if !foundPrebuiltPkg {
				pkgSt = c.BuildPkg(ctx, client, sOpt, spec, targetKey, opts...)
			}

			var img *dalec.DockerImageSpec

			if imageName != "" {
				// Named image: use the 3-level merge chain.
				def := spec.GetImageDefinition(imageName, targetKey)
				if def == nil {
					return nil, nil, errors.Errorf("named image %q not defined in spec", imageName)
				}
				img, err = BuildNamedImageConfig(ctx, sOpt, spec, platform, targetKey, imageName)
				if err != nil {
					return nil, nil, err
				}

				// Override the spec's Image with the resolved named image
				// config so that downstream code (BuildContainer,
				// GetImagePost, GetSingleBase, etc.) uses the correct
				// values from the named image definition. This is safe
				// because the spec is freshly loaded per build invocation.
				spec.Image = &def.ImageConfig
				if t, ok := spec.Targets[targetKey]; ok {
					t.Image = nil
					spec.Targets[targetKey] = t
				}

				// Append image-specific tests so they run alongside
				// root + target tests during c.RunTests.
				if len(def.Tests) > 0 {
					spec.Tests = append(spec.Tests, def.Tests...)
				}

				// Filter packages to only include those requested by the
				// named image definition. When Packages is non-nil, only
				// the listed packages are installed; when nil, all packages
				// are installed (the full build output).
				if def.Packages != nil {
					pkgSt = c.FilterPackages(pkgSt, spec, def.Packages, opts...)
				}
			} else if len(spec.Images) > 0 {
				// No image name specified but spec has named images.
				// When images are defined, bare "container" target is not valid —
				// a specific image name must be provided.
				return nil, nil, errors.New("spec defines named images; specify an image name in the target path (e.g., container/<name>)")
			} else {
				// Traditional single-image spec.
				img, err = BuildImageConfig(ctx, sOpt, spec, platform, targetKey)
				if err != nil {
					return nil, nil, err
				}
			}

			ctr := c.BuildContainer(ctx, client, sOpt, spec, targetKey, pkgSt, opts...)
			tests := c.RunTests(ctx, client, spec, sOpt, ctr, targetKey, opts...)

			ref, err := getRef(ctx, client, ctr.With(tests))

			return ref, img, err
		})
	}
}

//go:embed build_sysext.sh
var buildSysextSh []byte

func HandleSysext(c DistroConfig) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
			if err != nil {
				return nil, nil, err
			}

			pc := dalec.Platform(platform)
			var opts []llb.ConstraintsOpt
			opts = append(opts, dalec.ProgressGroup(spec.Name))
			opts = append(opts, pc)

			pkgSt, foundPrebuiltPkg := getPrebuiltPackage(ctx, targetKey, client, opts, sOpt)

			// Pre-built package wasn't found so we need to build it.
			if !foundPrebuiltPkg {
				pkgSt = c.BuildPkg(ctx, client, sOpt, spec, targetKey, opts...)
			}

			extracted := c.ExtractPkg(ctx, client, sOpt, spec, targetKey, pkgSt, opts...)

			if platform == nil {
				p := platforms.DefaultSpec()
				platform = &p
			}

			scriptPath := "/tmp/dalec/internal/sysext/build.sh"

			scriptFile := llb.Scratch().File(
				llb.Mkfile("build_sysext.sh", 0o755, []byte(buildSysextSh)),
				dalec.WithConstraints(opts...),
			)

			rev := spec.Revision
			if rev == "" {
				rev = "1"
			}

			erofs := c.SysextWorker(sOpt, opts...).Run(
				llb.Args([]string{scriptPath, spec.Name, fmt.Sprintf("v%s-%s-%s", spec.Version, rev, targetKey), platform.Architecture}),
				llb.AddMount(scriptPath, scriptFile, llb.SourcePath("build_sysext.sh"), llb.Readonly),
				llb.AddMount("/input", extracted, llb.Readonly),
				dalec.WithConstraints(opts...),
			).AddMount("/output", llb.Scratch())

			ctr := c.BuildContainer(ctx, client, sOpt, spec, targetKey, pkgSt, pc)
			tests := c.RunTests(ctx, client, spec, sOpt, erofs, targetKey, pc)

			ref, err := getRef(ctx, client, ctr.With(tests))
			return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, err
		})
	}
}

// getPrebuiltPackage retrieves a package based on the target environment.
// Target-specific packages (e.g., "{targetKey}-pkg") are prioritized over generic packages ("pkg").
// This ensures compatibility with the build context and optimizes functionality for specific environments.
// Examples of target keys include "mariner2", "azlinux3", "windowscross", and "bookworm".
func getPrebuiltPackage(ctx context.Context, targetKey string, client gwclient.Client, opts []llb.ConstraintsOpt, sOpt dalec.SourceOpts) (llb.State, bool) {
	var pkgSt llb.State

	// Try target-specific package first.
	targetSpecificName := targetKey + dalec.PreBuiltPkgSuffix
	targetPkgSt, err := sOpt.GetContext(targetSpecificName, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch().Async(func(ctx context.Context, _ llb.State, _ *llb.Constraints) (llb.State, error) {
			// If attempts failed for retrieving a pre-built package from the build context, surface the error up when the state gets marshalled.
			return pkgSt, fmt.Errorf("error when retrieving target-specified package for %s: %w", targetKey, err)
		}), false
	}
	if targetPkgSt != nil {
		pkgSt = *targetPkgSt
		frontend.Warn(ctx, client, pkgSt, fmt.Sprintf("Using target-specific package from %s context", targetSpecificName))
		return pkgSt, true
	}

	// Try generic package.
	genericPkgSt, err := sOpt.GetContext(dalec.GenericPkg, dalec.WithConstraints(opts...))
	if err != nil {
		return llb.Scratch().Async(func(ctx context.Context, _ llb.State, _ *llb.Constraints) (llb.State, error) {
			// If attempts failed for retrieving a pre-built package from the build context, surface the error up when the state gets marshalled.
			return pkgSt, fmt.Errorf("error when retrieving generic package for %s: %w", targetKey, err)
		}), false
	}
	if genericPkgSt != nil {
		pkgSt = *genericPkgSt
		frontend.Warn(ctx, client, pkgSt, fmt.Sprintf("Fallback to using generic package from %s context", targetSpecificName))
		return pkgSt, true
	}

	return pkgSt, false
}

func HandlePackage(cfg DistroConfig) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			if err := cfg.Validate(spec); err != nil {
				return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
			}

			pg := dalec.ProgressGroup("Building " + targetKey + " package: " + spec.Name)
			sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
			if err != nil {
				return nil, nil, err
			}

			pc := dalec.Platform(platform)

			pkgSt := cfg.BuildPkg(ctx, client, sOpt, spec, targetKey, pg, pc)

			ctr := cfg.BuildContainer(ctx, client, sOpt, spec, targetKey, pkgSt, pg, pc)
			tests := cfg.RunTests(ctx, client, spec, sOpt, pkgSt, targetKey, pg, pc)

			ref, err := getRef(ctx, client, ctr.With(tests))
			if platform == nil {
				p := platforms.DefaultSpec()
				platform = &p
			}
			return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, err
		})
	}
}

func getRef(ctx context.Context, client gwclient.Client, st llb.State) (gwclient.Reference, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	return res.SingleRef()
}
