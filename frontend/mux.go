package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
)

type BuildFuncRedux func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error)

type RouteMux struct {
	handlers map[string]handler
	defaultH *handler
	// cached spec so we don't have to load it every time its needed
	spec *dalec.Spec

	always map[string]struct{}
}

type handler struct {
	f BuildFuncRedux
	t *bktargets.Target
}

// Add adds a handler for the given target
// [targetKey] is the resource path to be handled
func (m *RouteMux) Add(targetKey string, bf BuildFuncRedux, info *bktargets.Target) {
	if m.handlers == nil {
		m.handlers = make(map[string]handler)
	}

	h := handler{bf, info}
	m.handlers[targetKey] = h

	if info != nil && info.Default {
		m.defaultH = &h
	}

	bklog.G(context.TODO()).WithField("target", targetKey).Info("Added handler to router")
}

// Always are for targets that should be available even if the spec has its own targets listed.
func (m *RouteMux) Always(target string) {
	if m.always == nil {
		m.always = make(map[string]struct{})
	}

	if _, ok := m.handlers[target]; !ok {
		panic("target must be registered with a handler before being marked as always available: " + target)
	}
	m.always[target] = struct{}{}
}

const keyTarget = "target"

// describe returns the subrequests that are supported
func (m *RouteMux) describe() (*gwclient.Result, error) {
	subs := []subrequests.Request{bktargets.SubrequestsTargetsDefinition, subrequests.SubrequestsDescribeDefinition}

	dt, err := json.Marshal(subs)
	if err != nil {
		return nil, errors.Wrap(err, "error marshalling describe result to json")
	}

	buf := bytes.NewBuffer(nil)
	if err := subrequests.PrintDescribe(dt, buf); err != nil {
		return nil, err
	}

	res := gwclient.NewResult()
	res.Metadata = map[string][]byte{
		"result.json": dt,
		"result.txt":  buf.Bytes(),
		"version":     []byte(subrequests.SubrequestsDescribeDefinition.Version),
	}
	return res, nil
}

func (m *RouteMux) handleSubrequest(ctx context.Context, client gwclient.Client, opts map[string]string) (*gwclient.Result, bool, error) {
	switch opts[requestIDKey] {
	case "":
		return nil, false, nil
	case subrequests.RequestSubrequestsDescribe:
		res, err := m.describe()
		return res, true, err
	case bktargets.SubrequestsTargetsDefinition.Name:
		res, err := m.list(ctx, client, opts[keyTarget])
		return res, true, err
	case keyTargetOpt:
		return nil, false, nil
	default:
		return nil, false, errors.Errorf("unsupported subrequest %q", opts[requestIDKey])
	}
}

func (m *RouteMux) loadSpec(ctx context.Context, client gwclient.Client) (*dalec.Spec, error) {
	if m.spec != nil {
		return m.spec, nil
	}
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	spec, err := LoadSpec(ctx, dc)
	if err != nil {
		return nil, err
	}
	m.spec = spec

	return spec, nil
}

// list outputs the list of targets that are supported by the mux
func (m *RouteMux) list(ctx context.Context, client gwclient.Client, target string) (*gwclient.Result, error) {
	var ls bktargets.List

	check := maps.Keys(m.always)
	if target == "" {
		// TODO: If the spec has targets set in it, we should only return the targets that are in the spec
		for k := range m.handlers {
			if _, ok := m.always[k]; !ok {
				check = append(check, k)
			}
		}
	} else {
		// Use the target as a filter so the response only incldues routes that are underneath the target
		check = append(check, target)
	}

	slices.Sort(check)

	bklog.G(ctx).WithField("checks", check).Info("Checking targets")

	for _, t := range check {
		ctx := bklog.WithLogger(ctx, bklog.G(ctx).WithField("check", t))
		bklog.G(ctx).Info("Lookup target")
		matched, h, err := m.lookupTarget(ctx, t)
		if err != nil {
			bklog.G(ctx).WithError(err).Warn("Error looking up target, skipping")
			continue
		}

		ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("matched", matched))
		bklog.G(ctx).Info("Matched target")

		if h.t != nil {
			t := *h.t
			// We have the target info, we can use this directly
			ls.Targets = append(ls.Targets, t)
			continue
		}

		bklog.G(ctx).Info("No target info, calling handler")
		// No target info, so call the handler to get the info
		// This calls the route handler.
		// The route handler must be setup to handle the subrequest
		// Today we assume all route handers are setup to handle the subrequest.
		res, err := h.f(ctx, trimTargetOpt(client, matched))
		if err != nil {
			bklog.G(ctx).Errorf("%+v", err)
			return nil, err
		}

		var _ls bktargets.List
		if err := unmarshalResult(res, &_ls); err != nil {
			return nil, err
		}

		for _, t := range _ls.Targets {
			t.Name = path.Join(matched, t.Name)
			ls.Targets = append(ls.Targets, t)
		}
	}

	return ls.ToResult()
}

type noSuchHandlerError struct {
	Target    string
	Available []string
}

func handlerNotFound(target string, available []string) error {
	return &noSuchHandlerError{Target: target, Available: available}
}

func (err *noSuchHandlerError) Error() string {
	return fmt.Sprintf("no such handler for target %q: available targets: %s", err.Target, strings.Join(err.Available, ", "))
}

func (m *RouteMux) lookupTarget(ctx context.Context, target string) (matchedPattern string, _ *handler, _ error) {
	// `target` is from `docker build --target=<target>`
	// cases for `t` are as follows:
	//    1. may have an exact match in the handlers (ideal)
	//    2. may have a prefix match in the handlers, e.g. hander for `foo`, `target == "foo/bar"` (assume nested route)
	// 	  3. No matching handler and `target == ""` and there is a default handler set (assume default handler)
	//    4. No match in the handlers (error)
	h, ok := m.handlers[target]
	if ok {
		return target, &h, nil
	}

	if target == "" && m.defaultH != nil {
		bklog.G(ctx).Info("Using default target")
		return target, m.defaultH, nil
	}

	for k, h := range m.handlers {
		if strings.HasPrefix(target, k+"/") {
			bklog.G(ctx).WithField("prefix", k).WithField("matching request", target).Info("Using prefix match for target")
			return k, &h, nil
		}
	}

	return "", nil, handlerNotFound(target, maps.Keys(m.handlers))
}

func (m *RouteMux) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	// Cache the opts in case this is the raw client
	// This prevents a grpc request for multiple calls to BuildOpts

	opts := client.BuildOpts().Opts
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("handlers", maps.Keys(m.handlers)).WithField("target", opts[keyTarget]).WithField("requestid", opts[requestIDKey]).WithField("targetKey", GetTargetKey(client)))

	bklog.G(ctx).Info("Handling request")

	res, handled, err := m.handleSubrequest(ctx, client, opts)
	if err != nil {
		return nil, err
	}
	if handled {
		return res, nil
	}

	t := opts[keyTarget]

	matched, h, err := m.lookupTarget(ctx, t)
	if err != nil {
		return nil, err
	}

	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("matched", matched))

	// each call to `Handle` handles the next part of the target
	// When we call the handler, we want to remove the part of the target that is being handled so the next handler can handle the next part
	client = trimTargetOpt(client, matched)

	res, err = h.f(ctx, client)
	if err != nil {
		err = injectPathsToNotFoundError(matched, err)
		return res, err
	}

	// If this request was a request to list targets, we need to modify the response a bit
	// Otherwise we can just return the result as is.
	if opts[requestIDKey] == bktargets.SubrequestsTargetsDefinition.Name {
		return m.fixupListResult(matched, res)
	}
	return res, nil
}

func (m *RouteMux) fixupListResult(matched string, res *gwclient.Result) (*gwclient.Result, error) {
	// Update the targets to include the matched key in their path
	var v bktargets.List
	if err := unmarshalResult(res, &v); err != nil {
		return nil, err
	}

	updated := make([]bktargets.Target, 0, len(v.Targets))
	for _, t := range v.Targets {
		t.Name = path.Join(matched, t.Name)
		updated = append(updated, t)
	}

	v.Targets = updated
	if err := marshalResult(res, &v); err != nil {
		return nil, err
	}

	asResult, err := v.ToResult()
	if err != nil {
		return nil, err
	}

	// update the original result with the new data
	// See `v.ToResult()` for the metadata keys
	res.AddMeta("result.json", asResult.Metadata["result.json"])
	res.AddMeta("result.txt", asResult.Metadata["result.txt"])
	res.AddMeta("version", asResult.Metadata["version"])
	return res, nil
}

// If the error is from noSuchHandlerError, we want to update the error to include the matched target
// This makes sure the returned error message has the full target path.
func injectPathsToNotFoundError(matched string, err error) error {
	if err == nil {
		return nil
	}

	var e *noSuchHandlerError
	if !errors.As(err, &e) {
		return err
	}

	e.Target = path.Join(matched, e.Target)
	for i, v := range e.Available {
		e.Available[i] = path.Join(matched, v)
	}
	return e
}

func unmarshalResult[T any](res *gwclient.Result, v *T) error {
	dt, ok := res.Metadata["result.json"]
	if !ok {
		return errors.Errorf("no result.json metadata in response")
	}
	return json.Unmarshal(dt, v)
}

func marshalResult[T any](res *gwclient.Result, v *T) error {
	dt, err := json.Marshal(v)
	if err != nil {
		return errors.Wrap(err, "error marshalling result to json")
	}
	res.Metadata["result.json"] = dt
	res.Metadata["result.txt"] = dt
	return nil
}

type clientWithCustomOpts struct {
	opts gwclient.BuildOpts
	gwclient.Client
}

func trimTargetOpt(client gwclient.Client, prefix string) gwclient.Client {
	opts := client.BuildOpts()

	updated := strings.TrimPrefix(opts.Opts[keyTarget], prefix)
	if len(updated) > 0 && updated[0] == '/' {
		updated = updated[1:]
	}
	opts.Opts[keyTarget] = updated
	return &clientWithCustomOpts{
		Client: client,
		opts:   opts,
	}
}

func (d *clientWithCustomOpts) BuildOpts() gwclient.BuildOpts {
	return d.opts
}
func (d *clientWithCustomOpts) CurrentFrontend() (*llb.State, error) {
	return d.Client.(interface{ CurrentFrontend() (*llb.State, error) }).CurrentFrontend()
}

// HandleWithForwards wraps [m.Handle] such that it will forward requests to custom frontends listed in the spec.
// Custom frontends should just use `[m.Handle]` directly.
// This is used by the main dalec frontend.
func (m *RouteMux) HandleWithForwards(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("withForwarding", true))

	spec, err := m.loadSpec(ctx, client)
	if err != nil {
		return nil, err
	}

	for key, t := range spec.Targets {
		if t.Frontend == nil {
			continue
		}

		if _, ok := m.always[key]; ok {
			return nil, fmt.Errorf("target %q is marked as always available and cannot be forwarded to a custom frontend", key)
		}
		m.Add(key, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
			ctx = bklog.WithLogger(ctx, bklog.G(ctx).WithField("frontend", key).WithField("forwarded", true))
			bklog.G(ctx).Info("Forwarding to custom frontend")
			req, err := newSolveRequest(
				copyForForward(ctx, client),
				withSpec(ctx, spec, dalec.ProgressGroup("prepare spec to forward to frontend")),
				toFrontend(t.Frontend),
				withTarget(client.BuildOpts().Opts[keyTarget]),
				withDalecTargetKey(key),
			)

			if err != nil {
				return nil, err
			}

			return client.Solve(ctx, req)
		}, nil)
		bklog.G(ctx).WithField("target", key).WithField("targets", maps.Keys(m.handlers)).WithField("targetKey", GetTargetKey(client)).Info("Added custom frontend to router")
	}

	res, err := m.Handle(ctx, client)
	if err != nil {
		bklog.G(ctx).Errorf("%+v", err)
	}
	return res, err
}
