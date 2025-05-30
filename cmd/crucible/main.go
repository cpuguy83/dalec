package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec/sessionutil/socketprovider"
	"github.com/Azure/dalec/targets"
	"github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s <spec-file>\n", os.Args[0])
		os.Exit(1)
	}
	specPath := args[0]

	if err := run(ctx, specPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func getBuildkitClient(ctx context.Context) (*client.Client, error) {
	return client.New(ctx, "", client.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		cmd := exec.CommandContext(ctx, "docker", "buildx", "dial-stdio", "--progress=plain")
		cmd.Env = os.Environ()

		c1, c2 := net.Pipe()

		cmd.Stdin = c1
		cmd.Stdout = c1
		errBuf := bytes.NewBuffer(nil)

		r, w := io.Pipe()
		cmd.Stderr = io.MultiWriter(w, errBuf)

		if err := cmd.Start(); err != nil {
			return nil, err
		}

		go func() {
			if err := cmd.Wait(); err != nil {
				r.CloseWithError(fmt.Errorf("%w: %s", err, errBuf))
			} else {
				r.Close()
			}
			c1.Close()
		}()

		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			txt := strings.ToLower(scanner.Text())

			if strings.HasPrefix(txt, "#1 dialing builder") && strings.HasSuffix(txt, "done") {
				go func() {
					// Continue draining stderr so the process does not get blocked
					_, _ = io.Copy(io.Discard, r)
				}()
				break
			}
		}

		return c2, nil
	}))
}

func run(ctx context.Context, specPath string) (retErr error) {
	d, err := progressui.NewDisplay(os.Stderr, progressui.DisplayMode(progressui.AutoMode))
	if err != nil {
		return nil
	}
	chErr := make(chan error, 1)
	ch := make(chan *client.SolveStatus, 1)
	go func() {
		_, err := d.UpdateFrom(ctx, ch)
		chErr <- err
	}()

	defer func() {
		err := <-chErr
		if retErr == nil {
			retErr = err
		}
	}()

	c, err := getBuildkitClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to connect to buildkit: %w", err)
	}

	specDt, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("failed to read spec file %s: %w", specPath, err)
	}
	specSt := llb.Scratch().File(
		llb.Mkfile(dockerui.DefaultLocalNameDockerfile, 0o644, specDt),
		llb.WithCustomName("Prepare spec file"),
	)

	spec, err := specSt.Marshal(ctx)
	if err != nil {
		return fmt.Errorf("failed to marshal spec file: %w", err)
	}

	var attachables []session.Attachable

	auth, err := getRegistryAuth()
	if err != nil {
		return fmt.Errorf("failed to get registry auth: %w", err)
	}

	attachables = append(attachables, auth)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	proxyErr := make(chan error, 1)
	proxy, err := socketprovider.NewProxyHandler([]socketprovider.ProxyConfig{
		{
			ID: "bazel-default",
			Dialer: func(ctx context.Context) (net.Conn, error) {
				cmd := exec.Command("ssh", "4.242.230.225", "socat", "STDIO TCP4:bazel-remote2.orangewater-3888d8b2.westus3.azurecontainerapps.io:9092")
				cmd.Env = os.Environ()
				c1, c2 := net.Pipe()
				cmd.Stdin = c1
				cmd.Stdout = c1

				errBuf := bytes.NewBuffer(nil)
				cmd.Stderr = errBuf

				if err := cmd.Start(); err != nil {
					chErr <- err
					return nil, fmt.Errorf("failed to start bazel remote: %w", err)
				}

				go func() {
					err := cmd.Wait()
					c1.Close()
					if err != nil {
						err = fmt.Errorf("bazel remote connection error: %w: %s", err, errBuf)
					}
					proxyErr <- err
				}()

				return c2, nil
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create socket proxy: %w", err)
	}
	attachables = append(attachables, proxy)

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current working directory: %w", err)
	}

	projectRootPath, err := lookupProjectRoot(cwd)
	if err != nil {
		return fmt.Errorf("failed to lookup project root: %w", err)
	}

	projectRoot, err := fsutil.NewFS(projectRootPath)
	if err != nil {
		return fmt.Errorf("failed to create fs for project root %s: %w", projectRootPath, err)
	}

	f, err := os.Open(filepath.Join(projectRootPath, ".dockerignore"))
	if err != nil {
		return fmt.Errorf("failed to open .dockerignore file: %w", err)
	}
	defer f.Close()
	excludes, err := ignorefile.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read .dockerignore file: %w", err)
	}

	projectRootFiltered, err := fsutil.NewFilterFS(projectRoot, &fsutil.FilterOpt{
		ExcludePatterns: excludes,
	})
	if err != nil {
		return fmt.Errorf("failed to create filtered fs for project root %s: %w", projectRootPath, err)
	}

	solve := client.SolveOpt{
		Session: attachables,
		LocalMounts: map[string]fsutil.FS{
			dockerui.DefaultLocalNameContext:    projectRootFiltered,
			dockerui.DefaultLocalNameDockerfile: projectRoot,
		},
	}

	build := func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		res, err := buildFrontend(ctx, client)
		if err != nil {
			return nil, fmt.Errorf("failed to build frontend: %w", err)
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, err
		}

		st, err := ref.ToState()
		if err != nil {
			return nil, fmt.Errorf("failed to convert ref to state: %w", err)
		}

		imgConfigDt := res.Metadata[exptypes.ExporterImageConfigKey]

		if imgConfigDt != nil {
			st, err = st.WithImageConfig(imgConfigDt)
			if err != nil {
				return nil, err
			}
		}

		meta := map[string][]byte{
			exptypes.ExporterImageConfigKey: imgConfigDt,
		}
		metaDt, err := json.Marshal(meta)
		if err != nil {
			return nil, errors.Wrap(err, "error marshaling local frontend metadata")
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal state: %w", err)
		}

		sr := gwclient.SolveRequest{
			Frontend: "gateway.v0",
			FrontendOpt: map[string]string{
				"dockerfile":                    "input:dockerfile",
				"source":                        "dalec-frontend",
				"input-metadata:dalec-frontend": string(metaDt),
				"context:dalec-frontend":        "input:dalec-frontend",
				"target":                        "azlinux3/rpm",
				"no-cache":                      targets.IgnoreCacheKeyPkg,
			},
			FrontendInputs: map[string]*pb.Definition{
				"dalec-frontend": def.ToPB(),
				"dockerfile":     spec.ToPB(),
			},
		}

		return client.Solve(ctx, sr)
	}

	grp, ctx := errgroup.WithContext(ctx)
	grp.Go(func() error {
		_, err = c.Build(ctx, solve, "the dalec crucible!", build, ch)
		if err != nil {
			return fmt.Errorf("failed to build: %w", err)
		}
		return nil
	})

	grp.Go(func() error {
		select {
		case err := <-proxyErr:
			if err != nil {
				return fmt.Errorf("socket proxy error: %w", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	})
	return grp.Wait()
}

func buildFrontend(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("failed to create dockerui client: %w", err)
	}

	bctx, err := dc.MainContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get main context: %w", err)
	}

	if bctx == nil {
		return nil, errors.New("no main context found")
	}

	def, err := bctx.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal build context: %w", err)
	}

	// Can't use the state from `MainContext` because it filters out
	// whatever was in `.dockerignore`, which may include `Dockerfile`,
	// which we need.
	dockerfileDef, err := llb.Local(dockerui.DefaultLocalNameDockerfile, llb.IncludePatterns([]string{"Dockerfile"})).Marshal(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling Dockerfile context")
	}

	defPB := def.ToPB()
	return client.Solve(ctx, gwclient.SolveRequest{
		Frontend:    "dockerfile.v0",
		FrontendOpt: map[string]string{},
		FrontendInputs: map[string]*pb.Definition{
			dockerui.DefaultLocalNameContext:    defPB,
			dockerui.DefaultLocalNameDockerfile: dockerfileDef.ToPB(),
		},
		Evaluate: true,
	})
}

func getRegistryAuth() (session.Attachable, error) {
	errBuf := bytes.NewBuffer(nil)
	cfg := authprovider.DockerAuthProviderConfig{
		ConfigFile: config.LoadDefaultConfigFile(errBuf),
	}

	auth := authprovider.NewDockerAuthProvider(cfg)
	if errBuf.Len() > 0 {
		return nil, &bufErr{errBuf}
	}
	return auth, nil
}

type bufErr struct {
	fmt.Stringer
}

func (b *bufErr) Error() string {
	return b.String()
}

func lookupProjectRoot(cur string) (string, error) {
	if _, err := os.Stat(filepath.Join(cur, "go.mod")); err != nil {
		if cur == "/" || cur == "." {
			return "", errors.Wrap(err, "could not find project root")
		}
		if os.IsNotExist(err) {
			return lookupProjectRoot(filepath.Dir(cur))
		}
		return "", err
	}

	return cur, nil
}
