package dalec

import (
	"context"
	"testing"
)

func TestSourceBuild(t *testing.T) {
	type testCase struct {
		name string
		src  Source
	}

	cases := []testCase{
		{
			name: "Source only",
			src: Source{
				Build: &SourceBuild{
					Source: &Source{
						Inline: &SourceInline{
							File: &SourceInlineFile{
								Contents: `
								FROM busybox:latest
								RUN echo hello
								`,
							},
						},
					},
				},
			},
		},
		{
			name: "Dockerfile only",
			src: Source{
				Build: &SourceBuild{
					Dockerfile: &Source{
						Inline: &SourceInline{
							File: &SourceInlineFile{
								Contents: `
								FROM busybox:latest
								RUN echo hello
								`,
							},
						},
					},
				},
			},
		},
	}

	ctx := context.Background()

	// To reuse code, let's craft an equivelant SourceDockerImage with cmd's
	// We'll use that to validate the ops we got from the build source with [checkCmd]
	xID := "docker-image://docker.io/library/busybox:latest"
	srcDI := SourceDockerImage{
		Ref: xID,
		Cmd: &Command{
			Dir: "/", // Dockerfile defaults to /
			Env: map[string]string{
				// The dockerfile frontend auto-injects these env vars
				"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
			},
			Steps: []*BuildStep{
				{Command: "echo hello"},
			},
		},
	}

	for _, tc := range cases {
		src := tc.src

		t.Run(tc.name, func(t *testing.T) {
			t.Run("no filters", func(t *testing.T) {
				ops := getSourceOp(ctx, t, tc.src)

				id := ops[0].GetSource().Identifier
				if id != xID {
					t.Errorf("expected identifier %q, got %q", xID, id)
				}
				checkCmd(t, ops[1:], &Source{DockerImage: &srcDI}, [][]expectMount{noMountCheck, noMountCheck})
			})

			t.Run("with filters", func(t *testing.T) {
				t.Run("subdir", func(t *testing.T) {
					src := src
					src.Path = "subdir"

					// for build soruce, we expect to have a copy operation as the last op
					ops := getSourceOp(ctx, t, src)
					checkCmd(t, ops[1:len(ops)-1], &Source{DockerImage: &srcDI}, [][]expectMount{{rootMount}, {rootMount, expectMount{dest: "subdir"}}})
					checkFilter(t, ops[len(ops)-1].GetFile(), &src)
				})

				t.Run("include and exclude", func(t *testing.T) {
					src := src
					src.Includes = []string{"foo", "bar"}
					src.Excludes = []string{"baz"}

					// for build soruce, we expect to have a copy operation as the last op
					ops := getSourceOp(ctx, t, src)
					checkCmd(t, ops[1:len(ops)-1], &Source{DockerImage: &srcDI}, [][]expectMount{noMountCheck, noMountCheck})
					checkFilter(t, ops[len(ops)-1].GetFile(), &src)
				})

				t.Run("subpath with include-exclude", func(t *testing.T) {
					src := src
					src.Path = "subdir"
					src.Includes = []string{"foo", "bar"}
					src.Excludes = []string{"baz"}

					// for build source, we expect to have a copy operation as the last op
					ops := getSourceOp(ctx, t, src)
					checkCmd(t, ops[1:len(ops)-1], &Source{DockerImage: &srcDI}, [][]expectMount{{rootMount}, {rootMount, expectMount{dest: "subdir"}}})
					checkFilter(t, ops[len(ops)-1].GetFile(), &src)
				})
			})
		})
	}
}
