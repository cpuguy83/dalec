package dalec

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestGetImageDefinition(t *testing.T) {
	t.Parallel()

	t.Run("image not in spec returns nil", func(t *testing.T) {
		spec := Spec{
			Images: map[string]ImageDefinition{
				"existing": {},
			},
		}
		def := spec.GetImageDefinition("missing", "any")
		assert.Check(t, def == nil)
	})

	t.Run("no images map returns nil", func(t *testing.T) {
		spec := Spec{}
		def := spec.GetImageDefinition("anything", "any")
		assert.Check(t, def == nil)
	})

	t.Run("spec-level image only inherits root defaults", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
				Env:        []string{"A=1"},
				Post: &PostInstall{
					Symlinks: map[string]SymlinkTarget{
						"/usr/bin/foo": {Path: "/usr/local/bin/foo"},
					},
				},
			},
			Images: map[string]ImageDefinition{
				"default": {},
			},
		}
		def := spec.GetImageDefinition("default", "nonexistent")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.Equal(def.Entrypoint, "/bin/foo"))
		assert.Check(t, cmp.DeepEqual(def.Env, []string{"A=1"}))
		assert.Check(t, def.Post != nil)
		_, hasFoo := def.Post.Symlinks["/usr/bin/foo"]
		assert.Check(t, hasFoo)
		// Packages should be nil (not inherited from root).
		assert.Check(t, def.Packages == nil)
	})

	t.Run("spec-level image overrides root defaults", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
				Env:        []string{"A=1"},
				Labels:     map[string]string{"version": "1"},
			},
			Images: map[string]ImageDefinition{
				"custom": {
					ImageConfig: ImageConfig{
						Entrypoint: "/bin/bar",
						Env:        []string{"B=2"},
						Labels:     map[string]string{"custom": "true"},
					},
					Packages: []string{"pkg-a"},
				},
			},
		}
		def := spec.GetImageDefinition("custom", "nonexistent")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.Equal(def.Entrypoint, "/bin/bar"))
		// Env should be appended: root + spec-level image def.
		assert.Check(t, cmp.DeepEqual(def.Env, []string{"A=1", "B=2"}))
		// Labels should be merged.
		assert.Check(t, cmp.Equal(def.Labels["version"], "1"))
		assert.Check(t, cmp.Equal(def.Labels["custom"], "true"))
		// Packages come from spec-level image def.
		assert.Check(t, cmp.DeepEqual(def.Packages, []string{"pkg-a"}))
	})

	t.Run("no root image defaults", func(t *testing.T) {
		spec := Spec{
			Images: map[string]ImageDefinition{
				"standalone": {
					ImageConfig: ImageConfig{
						Entrypoint: "/bin/standalone",
					},
					Packages: []string{"pkg-standalone"},
				},
			},
		}
		def := spec.GetImageDefinition("standalone", "nonexistent")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.Equal(def.Entrypoint, "/bin/standalone"))
		assert.Check(t, cmp.DeepEqual(def.Packages, []string{"pkg-standalone"}))
	})

	t.Run("target overrides spec-level image", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
				Env:        []string{"A=1"},
			},
			Images: map[string]ImageDefinition{
				"myimg": {
					ImageConfig: ImageConfig{
						Env: []string{"B=2"},
					},
					Packages: []string{"pkg-base"},
				},
			},
			Targets: map[string]Target{
				"azlinux3": {
					Images: map[string]ImageDefinition{
						"myimg": {
							ImageConfig: ImageConfig{
								Entrypoint: "/bin/target",
								Env:        []string{"C=3"},
							},
							Packages: []string{"pkg-target"},
						},
					},
				},
			},
		}
		def := spec.GetImageDefinition("myimg", "azlinux3")
		assert.Check(t, def != nil)
		// Entrypoint: target overrides spec-level (which overrode root).
		assert.Check(t, cmp.Equal(def.Entrypoint, "/bin/target"))
		// Env: root A=1 + spec B=2 + target C=3.
		assert.Check(t, cmp.DeepEqual(def.Env, []string{"A=1", "B=2", "C=3"}))
		// Packages: target overrides spec-level.
		assert.Check(t, cmp.DeepEqual(def.Packages, []string{"pkg-target"}))
	})

	t.Run("target exists but does not override image", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
			},
			Images: map[string]ImageDefinition{
				"myimg": {
					Packages: []string{"pkg-spec"},
				},
			},
			Targets: map[string]Target{
				"azlinux3": {
					// No Images override for myimg.
				},
			},
		}
		def := spec.GetImageDefinition("myimg", "azlinux3")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.Equal(def.Entrypoint, "/bin/foo"))
		assert.Check(t, cmp.DeepEqual(def.Packages, []string{"pkg-spec"}))
	})

	t.Run("packages not inherited from root image", func(t *testing.T) {
		// Packages should NEVER come from root image defaults.
		// Only from spec-level or target-level image definition.
		spec := Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/foo",
			},
			Images: map[string]ImageDefinition{
				"nopackages": {
					// No Packages field set.
				},
			},
		}
		def := spec.GetImageDefinition("nopackages", "nonexistent")
		assert.Check(t, def != nil)
		assert.Check(t, def.Packages == nil)
	})

	t.Run("target nil packages preserves spec packages", func(t *testing.T) {
		spec := Spec{
			Images: map[string]ImageDefinition{
				"img": {
					Packages: []string{"from-spec"},
				},
			},
			Targets: map[string]Target{
				"t1": {
					Images: map[string]ImageDefinition{
						"img": {
							// Packages is nil (not set) — should preserve spec-level.
						},
					},
				},
			},
		}
		def := spec.GetImageDefinition("img", "t1")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.DeepEqual(def.Packages, []string{"from-spec"}))
	})

	t.Run("post full override via spec-level image", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Post: &PostInstall{
					Symlinks: map[string]SymlinkTarget{
						"/usr/bin/foo": {Path: "/usr/local/bin/foo"},
					},
				},
			},
			Images: map[string]ImageDefinition{
				"distroless": {
					ImageConfig: ImageConfig{
						Post: &PostInstall{}, // Empty post clears root.
					},
				},
			},
		}
		def := spec.GetImageDefinition("distroless", "nonexistent")
		assert.Check(t, def != nil)
		assert.Check(t, def.Post != nil)
		assert.Check(t, cmp.Len(def.Post.Symlinks, 0))
	})

	t.Run("tests accumulated from spec and target levels", func(t *testing.T) {
		spec := Spec{
			Images: map[string]ImageDefinition{
				"img": {
					Tests: []*TestSpec{
						{Name: "spec-test"},
					},
				},
			},
			Targets: map[string]Target{
				"t1": {
					Images: map[string]ImageDefinition{
						"img": {
							Tests: []*TestSpec{
								{Name: "target-test"},
							},
						},
					},
				},
			},
		}
		def := spec.GetImageDefinition("img", "t1")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.Len(def.Tests, 2))
		assert.Check(t, cmp.Equal(def.Tests[0].Name, "spec-test"))
		assert.Check(t, cmp.Equal(def.Tests[1].Name, "target-test"))
	})

	t.Run("tests from spec only when no target override", func(t *testing.T) {
		spec := Spec{
			Images: map[string]ImageDefinition{
				"img": {
					Tests: []*TestSpec{
						{Name: "spec-only"},
					},
				},
			},
		}
		def := spec.GetImageDefinition("img", "nonexistent")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.Len(def.Tests, 1))
		assert.Check(t, cmp.Equal(def.Tests[0].Name, "spec-only"))
	})

	t.Run("bases full override via target", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Bases: []BaseImage{
					{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "root-base:1"}}},
				},
			},
			Images: map[string]ImageDefinition{
				"img": {},
			},
			Targets: map[string]Target{
				"t1": {
					Images: map[string]ImageDefinition{
						"img": {
							ImageConfig: ImageConfig{
								Bases: []BaseImage{
									{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "target-base:2"}}},
								},
							},
						},
					},
				},
			},
		}
		def := spec.GetImageDefinition("img", "t1")
		assert.Check(t, def != nil)
		assert.Check(t, cmp.Len(def.Bases, 1))
		assert.Check(t, cmp.Equal(def.Bases[0].Rootfs.DockerImage.Ref, "target-base:2"))
	})
}

func TestGetNamedImagePost(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for missing image", func(t *testing.T) {
		spec := Spec{}
		post := spec.GetNamedImagePost("missing", "any")
		assert.Check(t, post == nil)
	})

	t.Run("returns post from resolved image", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Post: &PostInstall{
					Symlinks: map[string]SymlinkTarget{
						"/usr/bin/foo": {Path: "/usr/local/bin/foo"},
					},
				},
			},
			Images: map[string]ImageDefinition{
				"img": {},
			},
		}
		post := spec.GetNamedImagePost("img", "nonexistent")
		assert.Check(t, post != nil)
		_, has := post.Symlinks["/usr/bin/foo"]
		assert.Check(t, has)
	})

	t.Run("returns nil post when image has no post", func(t *testing.T) {
		spec := Spec{
			Images: map[string]ImageDefinition{
				"img": {},
			},
		}
		post := spec.GetNamedImagePost("img", "nonexistent")
		assert.Check(t, post == nil)
	})
}

func TestGetNamedImageBases(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for missing image", func(t *testing.T) {
		spec := Spec{}
		bases := spec.GetNamedImageBases("missing", "any")
		assert.Check(t, bases == nil)
	})

	t.Run("returns bases from resolved image", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Bases: []BaseImage{
					{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "base:1"}}},
				},
			},
			Images: map[string]ImageDefinition{
				"img": {},
			},
		}
		bases := spec.GetNamedImageBases("img", "nonexistent")
		assert.Check(t, cmp.Len(bases, 1))
		assert.Check(t, cmp.Equal(bases[0].Rootfs.DockerImage.Ref, "base:1"))
	})

	t.Run("returns nil bases when image has no bases", func(t *testing.T) {
		spec := Spec{
			Images: map[string]ImageDefinition{
				"img": {},
			},
		}
		bases := spec.GetNamedImageBases("img", "nonexistent")
		assert.Check(t, bases == nil)
	})

	t.Run("target bases override root bases", func(t *testing.T) {
		spec := Spec{
			Image: &ImageConfig{
				Bases: []BaseImage{
					{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "root:1"}}},
				},
			},
			Images: map[string]ImageDefinition{
				"img": {},
			},
			Targets: map[string]Target{
				"t1": {
					Images: map[string]ImageDefinition{
						"img": {
							ImageConfig: ImageConfig{
								Bases: []BaseImage{
									{Rootfs: Source{DockerImage: &SourceDockerImage{Ref: "target:2"}}},
								},
							},
						},
					},
				},
			},
		}
		bases := spec.GetNamedImageBases("img", "t1")
		assert.Check(t, cmp.Len(bases, 1))
		assert.Check(t, cmp.Equal(bases[0].Rootfs.DockerImage.Ref, "target:2"))
	})
}
