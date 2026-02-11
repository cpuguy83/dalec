package dalec

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestMergeImageConfigLayer(t *testing.T) {
	t.Parallel()

	t.Run("nil src is no-op", func(t *testing.T) {
		dst := &ImageConfig{
			Entrypoint: "/bin/foo",
			Cmd:        "--help",
		}
		mergeImageConfigLayer(dst, nil)
		assert.Check(t, cmp.Equal(dst.Entrypoint, "/bin/foo"))
		assert.Check(t, cmp.Equal(dst.Cmd, "--help"))
	})

	t.Run("override scalar fields", func(t *testing.T) {
		dst := &ImageConfig{
			Entrypoint: "/bin/foo",
			Cmd:        "--help",
			WorkingDir: "/app",
			StopSignal: "SIGTERM",
			Base:       "ubuntu:20.04",
			User:       "root",
		}
		src := &ImageConfig{
			Entrypoint: "/bin/bar",
			Cmd:        "--version",
			WorkingDir: "/opt",
			StopSignal: "SIGKILL",
			Base:       "alpine:3.18",
			User:       "nobody",
		}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.Equal(dst.Entrypoint, "/bin/bar"))
		assert.Check(t, cmp.Equal(dst.Cmd, "--version"))
		assert.Check(t, cmp.Equal(dst.WorkingDir, "/opt"))
		assert.Check(t, cmp.Equal(dst.StopSignal, "SIGKILL"))
		assert.Check(t, cmp.Equal(dst.Base, "alpine:3.18"))
		assert.Check(t, cmp.Equal(dst.User, "nobody"))
	})

	t.Run("empty src scalars preserve dst", func(t *testing.T) {
		dst := &ImageConfig{
			Entrypoint: "/bin/foo",
			Cmd:        "--help",
			WorkingDir: "/app",
			StopSignal: "SIGTERM",
			Base:       "ubuntu:20.04",
			User:       "root",
		}
		src := &ImageConfig{}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.Equal(dst.Entrypoint, "/bin/foo"))
		assert.Check(t, cmp.Equal(dst.Cmd, "--help"))
		assert.Check(t, cmp.Equal(dst.WorkingDir, "/app"))
		assert.Check(t, cmp.Equal(dst.StopSignal, "SIGTERM"))
		assert.Check(t, cmp.Equal(dst.Base, "ubuntu:20.04"))
		assert.Check(t, cmp.Equal(dst.User, "root"))
	})

	t.Run("env is appended", func(t *testing.T) {
		dst := &ImageConfig{
			Env: []string{"A=1", "B=2"},
		}
		src := &ImageConfig{
			Env: []string{"C=3"},
		}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.DeepEqual(dst.Env, []string{"A=1", "B=2", "C=3"}))
	})

	t.Run("env appends with no dst env", func(t *testing.T) {
		dst := &ImageConfig{}
		src := &ImageConfig{
			Env: []string{"A=1"},
		}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.DeepEqual(dst.Env, []string{"A=1"}))
	})

	t.Run("labels are map-merged", func(t *testing.T) {
		dst := &ImageConfig{
			Labels: map[string]string{"a": "1", "b": "2"},
		}
		src := &ImageConfig{
			Labels: map[string]string{"b": "override", "c": "3"},
		}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.Equal(dst.Labels["a"], "1"))
		assert.Check(t, cmp.Equal(dst.Labels["b"], "override"))
		assert.Check(t, cmp.Equal(dst.Labels["c"], "3"))
	})

	t.Run("labels merge into nil dst", func(t *testing.T) {
		dst := &ImageConfig{}
		src := &ImageConfig{
			Labels: map[string]string{"x": "y"},
		}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.Equal(dst.Labels["x"], "y"))
	})

	t.Run("volumes are map-merged", func(t *testing.T) {
		dst := &ImageConfig{
			Volumes: map[string]struct{}{"/data": {}},
		}
		src := &ImageConfig{
			Volumes: map[string]struct{}{"/logs": {}},
		}
		mergeImageConfigLayer(dst, src)
		_, hasData := dst.Volumes["/data"]
		_, hasLogs := dst.Volumes["/logs"]
		assert.Check(t, hasData)
		assert.Check(t, hasLogs)
	})

	t.Run("volumes merge into nil dst", func(t *testing.T) {
		dst := &ImageConfig{}
		src := &ImageConfig{
			Volumes: map[string]struct{}{"/vol": {}},
		}
		mergeImageConfigLayer(dst, src)
		_, has := dst.Volumes["/vol"]
		assert.Check(t, has)
	})

	t.Run("post full override replaces entirely", func(t *testing.T) {
		dst := &ImageConfig{
			Post: &PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"/usr/bin/foo": {Path: "/usr/local/bin/foo"},
				},
			},
		}
		src := &ImageConfig{
			Post: &PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"/usr/bin/bar": {Path: "/usr/local/bin/bar"},
				},
			},
		}
		mergeImageConfigLayer(dst, src)
		// dst.Post should be entirely replaced by src.Post.
		_, hasFoo := dst.Post.Symlinks["/usr/bin/foo"]
		assert.Check(t, !hasFoo)
		_, hasBar := dst.Post.Symlinks["/usr/bin/bar"]
		assert.Check(t, hasBar)
	})

	t.Run("post nil in src preserves dst post", func(t *testing.T) {
		dst := &ImageConfig{
			Post: &PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"/usr/bin/foo": {Path: "/usr/local/bin/foo"},
				},
			},
		}
		src := &ImageConfig{}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, dst.Post != nil)
		_, hasFoo := dst.Post.Symlinks["/usr/bin/foo"]
		assert.Check(t, hasFoo)
	})

	t.Run("post empty struct clears dst post", func(t *testing.T) {
		dst := &ImageConfig{
			Post: &PostInstall{
				Symlinks: map[string]SymlinkTarget{
					"/usr/bin/foo": {Path: "/usr/local/bin/foo"},
				},
			},
		}
		// An empty PostInstall (non-nil pointer) clears the dst's Post entirely.
		src := &ImageConfig{
			Post: &PostInstall{},
		}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, dst.Post != nil)
		assert.Check(t, cmp.Len(dst.Post.Symlinks, 0))
	})

	t.Run("bases full override replaces entirely", func(t *testing.T) {
		dst := &ImageConfig{
			Bases: []BaseImage{
				{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "old:1"}}},
			},
		}
		src := &ImageConfig{
			Bases: []BaseImage{
				{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "new:2"}}},
			},
		}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.Len(dst.Bases, 1))
		assert.Check(t, cmp.Equal(dst.Bases[0].Rootfs.DockerImage.Ref, "new:2"))
	})

	t.Run("bases nil in src preserves dst bases", func(t *testing.T) {
		dst := &ImageConfig{
			Bases: []BaseImage{
				{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "keep:1"}}},
			},
		}
		src := &ImageConfig{}
		mergeImageConfigLayer(dst, src)
		assert.Check(t, cmp.Len(dst.Bases, 1))
		assert.Check(t, cmp.Equal(dst.Bases[0].Rootfs.DockerImage.Ref, "keep:1"))
	})
}

func TestMergeSpecImage(t *testing.T) {
	t.Parallel()

	t.Run("nil root image and no target", func(t *testing.T) {
		spec := &Spec{}
		cfg := MergeSpecImage(spec, "nonexistent")
		assert.Check(t, cfg != nil)
		assert.Check(t, cmp.Equal(cfg.Entrypoint, ""))
	})

	t.Run("root image only no target", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
				Env:        []string{"A=1"},
			},
		}
		cfg := MergeSpecImage(spec, "nonexistent")
		assert.Check(t, cmp.Equal(cfg.Entrypoint, "/bin/foo"))
		assert.Check(t, cmp.DeepEqual(cfg.Env, []string{"A=1"}))
	})

	t.Run("target overrides root", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
				Env:        []string{"A=1"},
				Labels:     map[string]string{"x": "y"},
			},
			Targets: map[string]Target{
				"azlinux3": {
					Image: &ImageConfig{
						Entrypoint: "/bin/bar",
						Env:        []string{"B=2"},
						Labels:     map[string]string{"z": "w"},
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "azlinux3")
		assert.Check(t, cmp.Equal(cfg.Entrypoint, "/bin/bar"))
		// Env should be appended.
		assert.Check(t, cmp.DeepEqual(cfg.Env, []string{"A=1", "B=2"}))
		// Labels should be merged.
		assert.Check(t, cmp.Equal(cfg.Labels["x"], "y"))
		assert.Check(t, cmp.Equal(cfg.Labels["z"], "w"))
	})

	t.Run("target with nil image preserves root", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
			},
			Targets: map[string]Target{
				"azlinux3": {},
			},
		}
		cfg := MergeSpecImage(spec, "azlinux3")
		assert.Check(t, cmp.Equal(cfg.Entrypoint, "/bin/foo"))
	})
}
