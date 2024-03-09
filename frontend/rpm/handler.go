package rpm

import (
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

func Handlers(getWorker func(opts ...llb.ConstraintsOpt) llb.State) ([]*frontend.Target, error) {
	targets := []*frontend.Target{
		{
			Info: bktargets.Target{
				Name:        "debug/buildroot",
				Description: "Outputs an rpm buildroot suitable for passing to rpmbuild.",
			},
			Build: BuildrootHandler(getWorker),
		},
		{
			Info: bktargets.Target{
				Name:        "debug/sources",
				Description: "Outputs all the sources specified in the spec file.",
			},
			Build: HandleSources(getWorker),
		},
		{
			Info: bktargets.Target{
				Name:        "debug/spec",
				Description: "Outputs the generated RPM spec file",
			},
			Build: SpecHandler,
		},
	}

	return targets, nil
}
