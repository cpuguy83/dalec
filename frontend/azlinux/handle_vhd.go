package azlinux

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/containerd/platforms"
	"github.com/docker/go-units"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func handleVHD(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}

			pg := dalec.ProgressGroup("Create VHD")

			rpmDir, err := specToRpmLLB(ctx, w, client, spec, sOpt, targetKey, pg)
			if err != nil {
				return nil, nil, fmt.Errorf("error creating rpm: %w", err)
			}

			ref, err := runTests(ctx, client, w, spec, sOpt, rpmDir, targetKey)
			if err != nil {
				return nil, nil, err
			}

			st, err := ref.ToState()
			if err != nil {
				return ref, nil, err
			}

			worker, err := w.Base(sOpt, pg)
			if err != nil {
				return nil, nil, err
			}

			worker = worker.Run(
				w.Install([]string{
					"dracut",
					// "dracut-hyperv",
					"e2fsprogs",
					"parted",
					"qemu-img",
				}),
			).Root()

			size, err := units.FromHumanSize("10GB")
			if err != nil {
				return nil, nil, err
			}
			sizeStr := strconv.FormatInt(size, 10)

			raw := worker.
				Run(
					dalec.ShArgs("truncate -s 10GB /tmp/out/rootfs.img"),
				).
				Run(
					dalec.ShArgs("mkfs.ext4 -d /tmp/rootfs /tmp/out/rootfs.img"),
					llb.AddMount("/tmp/rootfs", st),
				).
				AddMount("/tmp/out", llb.Scratch())

			var arch string
			if platform != nil {
				arch = platform.Architecture
			} else {
				p := platforms.DefaultSpec()
				arch = p.Architecture
			}
			name := spec.Name + "-" + spec.Version + "-" + spec.Revision + "_" + arch + ".vhd"
		})
	}
}
