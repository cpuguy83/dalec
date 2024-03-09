package debug

import (
	"context"

	"github.com/Azure/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

func Handlers(ctx context.Context, client gwclient.Client, _ string) ([]*frontend.Target, error) {
	return []*frontend.Target{
		{
			Info: targets.Target{
				Name:        "resolve",
				Description: "Outputs the resolved dalec spec file with build args applied.",
			},
			Build: HandleResolve,
		},
		{
			Info: targets.Target{
				Name:        "sources",
				Description: "Outputs all sources from a dalec spec file.",
			},
			Build: HandleSources,
		},
	}, nil
}
