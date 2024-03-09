package rpm

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"golang.org/x/exp/maps"
)

func shArgs(cmd string) llb.RunOption {
	return llb.Args([]string{"sh", "-c", cmd})
}

func tar(worker, src llb.State, dest string, opts ...llb.ConstraintsOpt) llb.State {
	// Put the output tar in a consistent location regardless of `dest`
	// This way if `dest` changes we don't have to rebuild the tarball, which can be expensive.
	outBase := "/tmp/out"
	out := filepath.Join(outBase, filepath.Dir(dest))
	work := worker.Run(
		llb.AddMount("/src", src, llb.Readonly),
		shArgs("tar -C /src -cvzf /tmp/st ."),
		dalec.WithConstraints(opts...),
	).
		Run(
			shArgs("mkdir -p "+out+" && mv /tmp/st "+filepath.Join(out, filepath.Base(dest))),
			dalec.WithConstraints(opts...),
		)

	return work.AddMount(outBase, llb.Scratch())
}

func HandleSources(getWorker func(opts ...llb.ConstraintsOpt) llb.State) frontend.BuildFunc {
	return func(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string) (gwclient.Reference, *image.Image, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		sOpt.GetSourceWorker = getWorker
		sources, deps, err := Dalec2SourcesLLB(spec, sOpt)
		if err != nil {
			return nil, nil, err
		}

		sources = addDepsToSources(sources, deps)

		// Now we can merge sources into the desired path
		st := dalec.MergeAtPath(llb.Scratch(), sources, "/SOURCES")

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
}

func Dalec2SourcesLLB(spec *dalec.Spec, sOpt dalec.SourceOpts, opts ...llb.ConstraintsOpt) ([]llb.State, map[string]llb.State, error) {
	// Sort the map keys so that the order is consistent This shouldn't be
	// needed when MergeOp is supported, but when it is not this will improve
	// cache hits for callers of this function.
	sorted := dalec.SortMapKeys(spec.Sources)

	if sOpt.GetSourceWorker == nil {
		sOpt.GetSourceWorker = getDefaultSourceWorker(sOpt.Resolver, spec)
	}

	out := make([]llb.State, 0, len(spec.Sources))
	var gomods []llb.State

	for _, k := range sorted {
		src := spec.Sources[k]
		isDir, err := dalec.SourceIsDir(src)
		if err != nil {
			return nil, nil, err
		}

		ref := getSourceRef(&src)
		pg := dalec.ProgressGroup("Add spec source: " + k + " " + ref)

		opts := append(opts, pg)
		st, deps, err := src.AsState(k, sOpt, opts...)
		if err != nil {
			return nil, nil, err
		}

		if len(deps) > 0 {
			mods, ok := deps[dalec.GoModDepsKey]
			if ok {
				gomods = append(gomods, mods)
				delete(deps, dalec.GoModDepsKey)
			}

			if len(deps) > 0 {
				// For now we'll only support gomods as a special case
				// But the underlying code should be able to support more if needed.
				return nil, nil, fmt.Errorf("unexpected dependencies: %v", maps.Keys(deps))
			}
		}

		if isDir {
			worker := sOpt.GetSourceWorker(opts...)
			opts = append(opts, dalec.ProgressGroup("Create tarball of source: "+k))
			out = append(out, tar(worker, st, k+".tar.gz", opts...))
		} else {
			out = append(out, st)
		}
	}

	var depsOut map[string]llb.State
	if len(gomods) > 0 {
		st := dalec.MergeAtPath(llb.Scratch(), gomods, dalec.GoModDepsKey)
		depsOut = make(map[string]llb.State, 1)
		depsOut[dalec.GoModDepsKey] = st
	}

	return out, depsOut, nil
}

func getSourceRef(src *dalec.Source) string {
	s := ""
	switch {
	case src.DockerImage != nil:
		s = src.DockerImage.Ref
	case src.Git != nil:
		s = src.Git.URL
	case src.HTTP != nil:
		s = src.HTTP.URL
	case src.Context != nil:
		s = src.Context.Name
	case src.Build != nil:
		s = fmt.Sprintf("%v", src.Build.Source)
	case src.Gomod != nil:
		s = "go module: " + getSourceRef(&src.Gomod.From)
	case src.Inline != nil:
		s = "inline"
	default:
		s = "unknown"
	}
	return s
}

func addDepsToSources(sources []llb.State, deps map[string]llb.State) []llb.State {
	for k, v := range deps {
		st := llb.Scratch().File(
			llb.Copy(v, "/", k),
		)
		sources = append(sources, st)
	}
	return sources
}

func getDefaultSourceWorker(resolver llb.ImageMetaResolver, spec *dalec.Spec) func(opts ...llb.ConstraintsOpt) llb.State {
	return func(opts ...llb.ConstraintsOpt) llb.State {
		opt := dalec.WithConstraints(append(opts, dalec.ProgressGroup("No source worker provided, preparing default worker image"))...)
		return llb.Image("alpine:latest", opt).With(
			func(in llb.State) llb.State {
				if !spec.RequiresGo() {
					return in
				}
				return in.Run(
					shArgs("apk add --update-cache -y ca-certificates git go"),
					llb.AddMount("/var/cache/apk", llb.Scratch(), llb.AsPersistentCacheDir("dalec-alpine-apk-cache", llb.CacheMountShared)),
					opt,
				).Root()
			},
		)
	}
}
