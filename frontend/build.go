package frontend

import (
	"bytes"
	"context"
	"fmt"
	"strings"

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

type FetchHandlersFunc func(ctx context.Context, c gwclient.Client, targetKey string) ([]*Target, error)

// NewBuilder is the main entrypoint for the dalec frontend.
func NewBuilder(handlers map[string]FetchHandlersFunc) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		if !SupportsDiffMerge(client) {
			dalec.DisableDiffMerge(true)
		}

		dc, err := dockerui.NewClient(client)
		if err != nil {
			return nil, fmt.Errorf("could not create build client: %w", err)
		}

		spec, err := LoadSpec(ctx, dc)
		if err != nil {
			return nil, err
		}

		var rtr router

		// TODO: list requests

		targetKey, _, _ := strings.Cut(dc.Target, "/")
		if targetKey == "" && len(spec.Targets) > 0 {
			// targetKey = <find last target in yaml spec>
			// We can't look at the go struct for this because the entries are not ordered
		}

		// registerTargets := func(group string, rth FetchHandlersFunc) error {
		// 	targets, err := rth(ctx, client, group)
		// 	if err != nil {
		// 		return err
		// 	}

		// 	var defaultTarget *Target
		// 	for _, t := range targets {
		// 		rtr.Add(path.Join(group, t.Info.Name), t)
		// 		rtr.Add(path.Join(group, t.Info.Name, spec.Name), t)
		// 		if t.Info.Default {
		// 			defaultTarget = t
		// 		}
		// 	}

		// 	if defaultTarget == nil {
		// 		defaultTarget = targets[len(targets)-1]
		// 	}
		// 	rtr.Add(group, defaultTarget)

		// 	return nil
		// }

		// initRoutes := func() error {
		// 	specTargets := spec.Targets

		// 	if dc.Target != "" {
		// 		// filter down spec targets to only those that match the target
		// 		// This prevents us from registering targets that are not relevant
		// 		// Potentially saving one or more PRC's (if there are any custom frontends set).
		// 		root, _, _ := strings.Cut(dc.Target, "/")
		// 		t, ok := specTargets[root]
		// 		if ok {
		// 			specTargets = map[string]dalec.Target{root: t}
		// 		} else {
		// 			specTargets = nil
		// 		}
		// 	}

		// 	for group, t := range specTargets {
		// 		if t.Frontend != nil {
		// 			if err := registerTargets(group, requestFrontendTargets(ctx, client, t.Frontend)); err != nil {
		// 				return errors.Wrapf(err, "error getting targets for target %q", group)
		// 			}
		// 			continue
		// 		}

		// 		rth, ok := handlers[group]
		// 		if !ok {
		// 			// We have a target that does not have any registered handlers
		// 			// Nor does it have a frontend to forward to.
		// 			return errors.Errorf("no handler for target %q", group)
		// 		}

		// 		if err := registerTargets(group, rth); err != nil {
		// 			return err
		// 		}
		// 	}

		// 	if len(spec.Targets) == 0 {
		// 		for group, rth := range handlers {
		// 			if err := registerTargets(group, rth); err != nil {
		// 				return err
		// 			}
		// 		}
		// 	}

		// 	return nil
		// }
		// rtr.init = initRoutes

		// res, handled, err := dc.HandleSubrequest(ctx, makeRequestHandler(dc.Target, &rtr))
		// if err != nil || handled {
		// 	return res, err
		// }

		// Handle additional subrequests supported by dalec
		requestID := client.BuildOpts().Opts[requestIDKey]
		switch requestID {
		case dalecSubrequstForwardBuild:
		case "":
		default:
			return nil, fmt.Errorf("unknown request id %q", requestID)
		}

		// "", "{distro}", "{distro}/{rpm|container|deb}", "{distro}/{rpm|container|deb}/{sub}/{sub}"

		//t := rtr.Get(dc.Target)
		//if t == nil {
		//	return nil, fmt.Errorf("no handler for target %q", dc.Target)
		//}

		return doBuild(ctx, client, dc, spec, t.Build)
	}
}

func doBuild(ctx context.Context, client gwclient.Client, dc *dockerui.Client, spec *dalec.Spec, bf BuildFunc) (*gwclient.Result, error) {
	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		var targetPlatform, buildPlatform ocispecs.Platform
		if platform != nil {
			targetPlatform = *platform
		} else {
			targetPlatform = platforms.DefaultSpec()
		}

		// the dockerui client, given the current implementation, should only ever have
		// a single build platform
		if len(dc.BuildPlatforms) != 1 {
			return nil, nil, fmt.Errorf("expected exactly one build platform, got %d", len(dc.BuildPlatforms))
		}
		buildPlatform = dc.BuildPlatforms[0]

		args := dalec.DuplicateMap(dc.BuildArgs)
		fillPlatformArgs("TARGET", args, targetPlatform)
		fillPlatformArgs("BUILD", args, buildPlatform)
		if err := spec.SubstituteArgs(args); err != nil {
			return nil, nil, err
		}

		return bf(ctx, client, spec, targetKey)
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}
