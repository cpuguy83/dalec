package debug

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	yaml "github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// HandleResolve is a handler that generates a resolved spec file with all the build args and variables expanded.
func HandleResolve(ctx context.Context, client gwclient.Client, spec *dalec.Spec, _ string) (gwclient.Reference, *image.Image, error) {
	dt, err := yaml.Marshal(spec)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling spec: %w", err)
	}
	st := llb.Scratch().File(llb.Mkfile("spec.yml", 0640, dt), llb.WithCustomName("Generate resolved spec file - spec.yml"))
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
	}
	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	// Do not return a nil image, it may cause a panic
	return ref, &image.Image{}, err
}

func Resolve(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *image.Image, error) {
		dt, err := yaml.Marshal(spec)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling spec: %w", err)
		}
		st := llb.Scratch().File(llb.Mkfile("spec.yml", 0640, dt), llb.WithCustomName("Generate resolved spec file - spec.yml"))
		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
		}
		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		return ref, nil, err
	})
}
