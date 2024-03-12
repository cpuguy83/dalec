package rpm

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

// func Handlers(getWorker func(opts ...llb.ConstraintsOpt) llb.State) ([]*frontend.Target, error) {
// 	targets := []*frontend.Target{
// 		{
// 			Info: bktargets.Target{
// 				Name:        "debug/buildroot",
// 				Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
// 			},
// 			Build: BuildrootHandler(getWorker),
// 		},
// 		{
// 			Info: bktargets.Target{
// 				Name:        "debug/sources",
// 				Description: "Outputs all the sources specified in the spec file.",
// 			},
// 			Build: HandleSources(getWorker),
// 		},
// 		{
// 			Info: bktargets.Target{
// 				Name:        "debug/spec",
// 				Description: "Outputs the generated RPM spec file",
// 			},
// 			Build: SpecHandler,
// 		},
// 	}
//
// 	return targets, nil
// }

type WorkerFunc func(spec *dalec.Spec, opts ...llb.ConstraintsOpt) llb.State

// HandleDebug returns a build function that adds support for some debugging targets for RPM builds.
func HandleDebug(getWorker WorkerFunc) frontend.BuildFuncRedux {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		var r frontend.RouteMux

		r.Add("buildroot", HandleBuildroot(getWorker), &targets.Target{
			Name:        "buildroot",
			Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
		})

		r.Add("sources", HandleSources(getWorker), &targets.Target{
			Name:        "sources",
			Description: "Outputs all the sources specified in the spec file in the format given to rpmbuild.",
		})

		r.Add("spec", HandleSpec(getWorker), &targets.Target{
			Name:        "spec",
			Description: "Outputs the generated RPM spec file",
		})

		return r.Handle(ctx, client)
	}
}
