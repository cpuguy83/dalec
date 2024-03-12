package mariner2

import (
	"context"
	"sort"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func handleDepsOnly(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	rb, err := dc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		spec, err := frontend.LoadSpec(ctx, dc)
		if err != nil {
			return nil, nil, err
		}

		if err := frontend.SubstitutePlatformArgs(spec, &dc.BuildPlatforms[0], platform, spec.Args); err != nil {
			return nil, nil, err
		}

		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		targetKey := frontend.GetTargetKey(dc)

		pg := dalec.ProgressGroup("Build mariner2 deps-only container: " + spec.Name)
		baseImg := getWorkerImage(client, pg)
		rpmDir := baseImg.Run(
			shArgs(`set -ex; dir="/tmp/rpms/RPMS/$(uname -m)"; mkdir -p "${dir}"; tdnf install -y --releasever=2.0 --downloadonly --alldeps --downloaddir "${dir}" `+strings.Join(getRuntimeDeps(spec, targetKey), " ")),
			defaultTndfCacheMount(),
			pg,
		).
			AddMount("/tmp/rpms", llb.Scratch())

		st, err := specToContainerLLB(spec, targetKey, baseImg, rpmDir, sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		def, err := st.Marshal(ctx, pg)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		img, err := buildImageConfig(ctx, spec, client, targetKey)
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		return ref, img, nil
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}

func getRuntimeDeps(spec *dalec.Spec, targetKey string) []string {
	var deps *dalec.PackageDependencies
	if t, ok := spec.Targets[targetKey]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = spec.Dependencies
		if deps == nil {
			return nil
		}
	}

	var out []string
	for p := range deps.Runtime {
		out = append(out, p)
	}

	sort.Strings(out)
	return out
}
