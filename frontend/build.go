package frontend

import (
	"bytes"
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func LoadSpec(ctx context.Context, client *dockerui.Client) (*dalec.Spec, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	spec, err := dalec.LoadSpec(bytes.TrimSpace(src.Data))
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}
	return spec, nil
}

func getOS(platform ocispecs.Platform) string {
	return platform.OS
}

func getArch(platform ocispecs.Platform) string {
	return platform.Architecture
}

func getVariant(platform ocispecs.Platform) string {
	return platform.Variant
}

func getPlatformFormat(platform ocispecs.Platform) string {
	return platforms.Format(platform)
}

var passthroughGetters = map[string]func(ocispecs.Platform) string{
	"OS":       getOS,
	"ARCH":     getArch,
	"VARIANT":  getVariant,
	"PLATFORM": getPlatformFormat,
}

func fillPlatformArgs(prefix string, args map[string]string, platform ocispecs.Platform) {
	for attr, getter := range passthroughGetters {
		args[prefix+attr] = getter(platform)
	}
}

func SubstitutePlatformArgs(spec *dalec.Spec, bp, tp *ocispecs.Platform, args map[string]string) error {
	if tp == nil {
		p := platforms.DefaultSpec()
		tp = &p
	}

	args = dalec.DuplicateMap(args)
	fillPlatformArgs("TARGET", args, *tp)
	fillPlatformArgs("BUILD", args, *bp)

	if err := spec.SubstituteArgs(args); err != nil {
		return err
	}

	return nil
}

// func doBuild(ctx context.Context, client gwclient.Client, dc *dockerui.Client, spec *dalec.Spec, bf BuildFunc) (*gwclient.Result, error) {
// 	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
// 		var targetPlatform, buildPlatform ocispecs.Platform
// 		if platform != nil {
// 			targetPlatform = *platform
// 		} else {
// 			targetPlatform = platforms.DefaultSpec()
// 		}
//
// 		// the dockerui client, given the current implementation, should only ever have
// 		// a single build platform
// 		if len(dc.BuildPlatforms) != 1 {
// 			return nil, nil, fmt.Errorf("expected exactly one build platform, got %d", len(dc.BuildPlatforms))
// 		}
// 		buildPlatform = dc.BuildPlatforms[0]
//
// 		args := dalec.DuplicateMap(dc.BuildArgs)
// 		fillPlatformArgs("TARGET", args, targetPlatform)
// 		fillPlatformArgs("BUILD", args, buildPlatform)
// 		if err := spec.SubstituteArgs(args); err != nil {
// 			return nil, nil, err
// 		}
//
// 		return bf(ctx, client, spec, targetKey)
// 	})
// 	if err != nil {
// 		return nil, err
// 	}
//
// 	return rb.Finalize()
// }

type PlatformBuildFunc func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *image.Image, error)

// BuildWithPlatform is a helper function to build a spec with a given platform
// It takes care of looping through each tarrget platform and executing the build with the platform args substituted in the spec.
// This also deals with the docker-style multi-platform output.
func BuildWithPlatform(ctx context.Context, client gwclient.Client, f PlatformBuildFunc) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		spec, err := LoadSpec(ctx, dc)
		if err != nil {
			return nil, nil, err
		}

		if err := SubstitutePlatformArgs(spec, &dc.BuildPlatforms[0], platform, spec.Args); err != nil {
			return nil, nil, err
		}

		targetKey := GetTargetKey(dc)

		return f(ctx, client, platform, spec, targetKey)
	})
	if err != nil {
		return nil, err
	}
	return rb.Finalize()
}
