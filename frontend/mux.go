package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"
)

type BuildFuncRedux func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error)

type RouteMux struct {
	handlers map[string]handler
	defaultH *handler
}

type handler struct {
	f BuildFuncRedux
	t *bktargets.Target
}

func (m *RouteMux) Add(targetKey string, bf BuildFuncRedux, info *bktargets.Target) {
	if m.handlers == nil {
		m.handlers = make(map[string]handler)
	}

	h := handler{bf, info}
	m.handlers[targetKey] = h

	if info != nil && info.Default {
		m.defaultH = &h
	}
}

const KeyTarget = "target"

// describe returns th subrequests that are supported
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
		res, err := m.list(ctx, client, opts[KeyTarget])
		return res, true, err
	case keyTargetOpt:
		return nil, false, nil
	default:
		return nil, false, errors.Errorf("unsupported subrequest %q", opts[requestIDKey])
	}
}

func (m *RouteMux) list(ctx context.Context, client gwclient.Client, target string) (*gwclient.Result, error) {
	var ls bktargets.List

	var check []string
	if target == "" {
		// In this case we want to list all targets
		check = maps.Keys(m.handlers)
	} else {
		// Use the target as a filter
		check = []string{target}
	}

	slices.Sort(check)

	for _, t := range check {
		matched, h, err := m.lookupTarget(t)
		if err != nil {
			return nil, err
		}

		if h.t != nil {
			t := *h.t
			ls.Targets = append(ls.Targets, t)
			continue
		}

		// No target info, so call the handler to get the info
		res, err := h.f(ctx, trimTargetOpt(client, matched))
		if err != nil {
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

func (m *RouteMux) lookupTarget(target string) (matchedPattern string, _ *handler, _ error) {
	// `target` is from `docker build --target=<target>`
	// cases for `t` are as follows:
	//    1. may have an exact match in the handlers (ideal)
	//    2. may have a prefix match in the handlers, e.g. hander for `foo`, `target == "foo/bar"` (fallback)
	//    3. No match in the handlers (error)
	//
	// In some cases, `target` may be empty, in those cases there must be a handler explicity added for the empty string

	h, ok := m.handlers[target]
	if ok {
		return target, &h, nil
	}

	if target == "" && m.defaultH != nil {
		return target, m.defaultH, nil
	}

	for k, h := range m.handlers {
		if strings.HasPrefix(target, k+"/") {
			return k, &h, nil
		}
	}

	return "", nil, handlerNotFound(target, maps.Keys(m.handlers))
}

// Set an explicit default handler
// This is used when handling a request with an empty target and no other default handler was set on the target info.
func (m *RouteMux) Default(target string) {
	h, ok := m.handlers[target]
	if !ok {
		panic("no handler for target " + target)
	}
	m.defaultH = &h
}

func (m *RouteMux) Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	// Cache the opts in case this is the raw client
	// This prevents a grpc request for multiple calls to BuildOpts
	opts := client.BuildOpts().Opts

	res, handled, err := m.handleSubrequest(ctx, client, opts)
	if err != nil {
		return nil, err
	}
	if handled {
		return res, nil
	}

	t := opts[KeyTarget]
	matched, h, err := m.lookupTarget(t)
	if err != nil {
		return nil, err
	}

	// each call to `Handle` handles the next part of the target
	// When we call the handler, we want to remove the part of the target that is being handled so the next handler can handle the next part
	client = trimTargetOpt(client, matched)

	res, err = h.f(ctx, client)
	if err != nil {
		err = checkResssableRoutesError(matched, err)
		return res, err
	}

	// If this request was a request to list targets, we need to modify the response a bit
	// Otherwise we can just return the result as is.
	if opts[requestIDKey] != bktargets.SubrequestsTargetsDefinition.Name {
		return res, nil
	}

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
func checkResssableRoutesError(matched string, err error) error {
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

	updated := strings.TrimPrefix(opts.Opts[KeyTarget], prefix)
	if len(updated) > 0 && updated[0] == '/' {
		updated = updated[1:]
	}
	opts.Opts[KeyTarget] = updated
	return &clientWithCustomOpts{
		Client: client,
		opts:   opts,
	}
}

func (d *clientWithCustomOpts) BuildOpts() gwclient.BuildOpts {
	return d.opts
}

func GetSubrequest(client BuildOpstGetter) string {
	return client.BuildOpts().Opts[requestIDKey]
}
