package frontend

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/pkg/errors"
)

type BuildFunc func(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string) (gwclient.Reference, *image.Image, error)

type FetchHandlersFunc func(ctx context.Context, client gwclient.Client, targetKey string) ([]*Target, error)

type Target struct {
	Info  bktargets.Target
	Build BuildFunc
}

type router struct {
	routes map[string]*Target
	init   func() error
}

func (r *router) Add(p string, t *Target) {
	if r.routes == nil {
		r.routes = make(map[string]*Target)
	}
	r.routes[p] = t
}

func (r *router) Get(p string) *Target {
	r.init()
	return r.routes[p]
}

func (r *router) ForEach(f func(string, *Target)) {
	r.init()
	for k, v := range r.routes {
		f(k, v)
	}
}

func makeRequestHandler(target string, rtr *router) dockerui.RequestHandler {
	h := dockerui.RequestHandler{AllowOther: true}

	h.ListTargets = func(ctx context.Context) (*targets.List, error) {
		var ls targets.List
		rtr.ForEach(func(p string, t *Target) {
			if p == "" || strings.HasPrefix(target, p) {
				ls.Targets = append(ls.Targets, t.Info)
			}
		})
		return &ls, nil
	}

	return h
}

func requestFrontendTargets(ctx context.Context, client gwclient.Client, frontend *dalec.Frontend) FetchHandlersFunc {
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
				Build: makeTargetForwarder(frontend, t, targetKey),
			})
		}
		return targets, nil
	}
}
