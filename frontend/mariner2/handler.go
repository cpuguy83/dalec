package mariner2

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

const (
	DefaultTargetKey = "mariner2"
)

func Handlers(ctx context.Context, client gwclient.Client, targetKey string) ([]*frontend.Target, error) {
	targets := []*frontend.Target{
		{
			Info: targets.Target{
				Name:        "rpm",
				Description: "Builds an rpm and src.rpm for mariner2.",
			},
			Build: handleRPM,
		},
		{
			Info: targets.Target{
				Name:        "rpm/debug/buildroot",
				Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
			},
			Build: func(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string) (gwclient.Reference, *image.Image, error) {
				return rpm.BuildrootHandler(getSourceWorkerFunc(client, spec))(ctx, client, spec, targetKey)
			},
		},

		{
			Info: targets.Target{
				Name:        "container",
				Description: "Builds a container with the RPM installed.",
				Default:     true,
			},
			Build: handleContainer,
		},
		{
			Info: targets.Target{
				Name:        "container/depsonly",
				Description: "Builds a container with only the runtime dependencies installed.",
			},
		},
	}

	rpm.Handlers()

	return append(targets, rpmTargets...), nil
}

// We keep source deps separate from the main worker image in order to prevent conflicts with build dependencies
func installSourceDeps(spec *dalec.Spec, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		if !spec.RequiresGo() {
			return in
		}
		return in.Run(
			shArgs("tdnf install -y ca-certificates git msft-golang"),
			dalec.WithConstraints(append(opts, dalec.ProgressGroup("Add golang to worker image"))...),
		).
			AddEnv("GOPROXY", "proxy.golang.org").
			AddEnv("GOSUMDB", "sum.golang.org")
	}
}

func getSourceWorkerFunc(resolver llb.ImageMetaResolver) func(spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
	return func(spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State {
		opts = append(opts, dalec.ProgressGroup("Prepare source worker image"))
		return getWorkerImage(resolver, opts...).With(installSourceDeps(spec, opts...))
	}
}

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.RouteMux
	mux.Add("rpm", rpmHandler)
	mux.Add("container", handleContainer)

	var dc *dockerui.Client
	dc.HandleSubrequest()

	return mux.Handle(ctx, client)
}

func rpmHandler(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var mux frontend.RouteMux

	mux.Add("", handleRPM)

	mux.Add("buildroot", func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return rpm.BuildrootHandler(getSourceWorkerFunc(client))(ctx, client)
	})
}
