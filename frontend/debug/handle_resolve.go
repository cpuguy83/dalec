package debug

import (
	"context"
	"fmt"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	yaml "github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
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
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	spec, err := frontend.LoadSpec(ctx, dc)
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}

	dt, err := yaml.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("error marshalling spec: %w", err)
	}
	st := llb.Scratch().File(llb.Mkfile("spec.yml", 0640, dt), llb.WithCustomName("Generate resolved spec file - spec.yml"))
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("error marshalling llb: %w", err)
	}
	return client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
}
