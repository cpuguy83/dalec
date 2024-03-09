package frontend

import (
	"context"
	"strings"

	"github.com/Azure/dalec"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests"
	"github.com/moby/buildkit/frontend/subrequests/outline"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
)

type solveRequestOpt func(*gwclient.SolveRequest) error

func newSolveRequest(opts ...solveRequestOpt) (gwclient.SolveRequest, error) {
	var sr gwclient.SolveRequest

	for _, o := range opts {
		if err := o(&sr); err != nil {
			return sr, err
		}
	}
	return sr, nil
}

func toFrontend(f *dalec.Frontend) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		req.Frontend = gatewayFrontend
		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string)
		}
		req.FrontendOpt["source"] = f.Image
		req.FrontendOpt["cmdline"] = f.CmdLine
		return nil
	}
}

func withSpec(ctx context.Context, spec *dalec.Spec, opts ...llb.ConstraintsOpt) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		if req.FrontendInputs == nil {
			req.FrontendInputs = make(map[string]*pb.Definition)
		}

		dt, err := yaml.Marshal(spec)
		if err != nil {
			return errors.Wrap(err, "error marshalling spec to yaml")
		}

		def, err := llb.Scratch().File(llb.Mkfile(dockerui.DefaultDockerfileName, 0600, dt), opts...).Marshal(ctx)
		if err != nil {
			return errors.Wrap(err, "error marshaling spec to LLB")
		}
		req.FrontendInputs[dockerui.DefaultLocalNameDockerfile] = def.ToPB()
		return nil
	}
}

func withTarget(t string) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string)
		}
		req.FrontendOpt["target"] = t
		return nil
	}
}

func withSubrequest(id string) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string)
		}
		req.FrontendOpt[requestIDKey] = id
		caps := req.FrontendOpt["frontend.caps"]

		const subreqCap = "moby.buildkit.frontend.subrequests"
		if !strings.Contains(caps, subreqCap) {
			req.FrontendOpt["frontend.caps"] = strings.Join(append(strings.Split(caps, ","), "moby.buildkit.frontend.subrequests"), ",")
		}

		return nil
	}
}

func toDockerfile(ctx context.Context, bctx llb.State, dt []byte, spec *dalec.SourceBuild, opts ...llb.ConstraintsOpt) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		req.Frontend = "dockerfile.v0"

		bctxDef, err := bctx.Marshal(ctx)
		if err != nil {
			return errors.Wrap(err, "error marshaling dockerfile to LLB")
		}
		if req.FrontendInputs == nil {
			req.FrontendInputs = make(map[string]*pb.Definition)
		}

		dfDef, err := marshalDockerfile(ctx, dt, opts...)
		if err != nil {
			return errors.Wrap(err, "error marshaling dockerfile to LLB")
		}

		req.FrontendInputs[dockerui.DefaultLocalNameContext] = bctxDef.ToPB()
		req.FrontendInputs[dockerui.DefaultLocalNameDockerfile] = dfDef.ToPB()

		if ref, cmdline, _, ok := parser.DetectSyntax(dt); ok {
			req.Frontend = gatewayFrontend
			if req.FrontendOpt == nil {
				req.FrontendOpt = make(map[string]string)
			}
			req.FrontendOpt["source"] = ref
			req.FrontendOpt["cmdline"] = cmdline
		}

		if spec != nil {
			if req.FrontendOpt == nil {
				req.FrontendOpt = make(map[string]string)
			}
			if spec.Target != "" {
				req.FrontendOpt["target"] = spec.Target
			}
			for k, v := range spec.Args {
				req.FrontendOpt["build-arg:"+k] = v
			}
		}
		return nil
	}
}

func marshalDockerfile(ctx context.Context, dt []byte, opts ...llb.ConstraintsOpt) (*llb.Definition, error) {
	st := llb.Scratch().File(llb.Mkfile(dockerui.DefaultDockerfileName, 0600, dt), opts...)
	return st.Marshal(ctx)
}

// Sets the target key for the dalec frontend
// This is used when forwarding to another dalec frontend
// The key is used to determine which [dalec.Target] to select in the spec.
func withDalecTargetKey(t string) solveRequestOpt {
	return func(req *gwclient.SolveRequest) error {
		if req.FrontendOpt == nil {
			req.FrontendOpt = make(map[string]string)
		}
		req.FrontendOpt[keyTargetOpt] = t
		return nil
	}
}

type BuildFuncRedux func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error)

type RouteMux struct {
	handlers map[string]BuildFuncRedux
}

func (m *RouteMux) Add(targetKey string, bf BuildFuncRedux) {
	if m.handlers == nil {
		m.handlers = make(map[string]BuildFuncRedux)
	}
	m.handlers[targetKey] = bf
}

const KeyTarget = "target"

func (m *RouteMux) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	root, _, _ := strings.Cut(client.BuildOpts().Opts[KeyTarget], "/")
	h, ok := m.handlers[root]
	if !ok {
		return nil, errors.Errorf("no handler found for target %q", root)
	}

	res, err := h(ctx, client)
	if err != nil {
		return nil, errors.Wrap(err, "error building target "+root)
	}
}

type clientWithCustomOpts struct {
	opts gwclient.BuildOpts
	gwclient.Client
}

func (d *clientWithCustomOpts) BuildOpts() gwclient.BuildOpts {
	return d.opts
}

func trimBuildTarget(client gwclient.Client, prefix string) gwclient.Client {
	opts := client.BuildOpts()
	opts.Opts[KeyTarget] = strings.TrimPrefix(opts.Opts[KeyTarget], prefix)
	return &clientWithCustomOpts{
		Client: client,
		opts:   opts,
	}
}

func GetSubrequest(client BuildOpstGetter) string {
	return client.BuildOpts().Opts[requestIDKey]
}

func HandleSubrequest(client BuildOpstGetter) (*gwclient.Result, bool, error) {
	req, ok := client.BuildOpts().Opts[requestIDKey]
	if !ok {
		return nil, false, nil
	}
	switch req {
	case subrequests.RequestSubrequestsDescribe:
		res, err := describe(client)
		return res, true, err
	case outline.SubrequestsOutlineDefinition.Name:
	}
}
