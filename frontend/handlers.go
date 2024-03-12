package frontend

import (
	"context"
	"encoding/json"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/pkg/errors"
)

type BuildFunc func(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string) (gwclient.Reference, *image.Image, error)

type FetchHandlersFunc func(ctx context.Context, client gwclient.Client, targetKey string) ([]*Target, error)

type Target struct {
	Info  bktargets.Target
	Build BuildFunc
}

func requestFrontendTargets(frontend *dalec.Frontend) FetchHandlersFunc {
	return func(ctx context.Context, client gwclient.Client, targetKey string) ([]*Target, error) {
		req, err := newSolveRequest(
			withSubrequest(bktargets.SubrequestsTargetsDefinition.Name),
			toFrontend(frontend),
			copyForForward(ctx, client),
		)
		if err != nil {
			return nil, errors.Wrapf(err, "error creating request to resolve routes")
		}

		res, err := client.Solve(ctx, req)
		if err != nil {
			return nil, errors.Wrapf(err, "error getting targets from frontend")
		}

		dt, ok := res.Metadata["result.json"]
		if !ok {
			return nil, errors.Errorf("missing result.json from list targets")
		}
		var tl bktargets.List
		if err := json.Unmarshal(dt, &tl); err != nil {
			return nil, errors.Wrapf(err, "error unmarshalling targets")
		}

		targets := make([]*Target, 0, len(tl.Targets))

		for _, t := range tl.Targets {
			targets = append(targets, &Target{
				Info:  t,
				Build: makeTargetForwarder(frontend, t),
			})
		}
		return targets, nil
	}
}
