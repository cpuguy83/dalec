package deb

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestDebrootPostinstIncludesDebhelperMarker(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Website:     "https://example.invalid",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Artifacts: dalec.Artifacts{
			Users: []dalec.AddUserConfig{
				{Name: "dalecuser"},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "postinst"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil)

	assert.Equal(t, int32(0o700), mkfile.Mode)
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("#DEBHELPER#")))
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("useradd dalecuser")))
}

func TestGenerateSubPackagePostinst(t *testing.T) {
	t.Parallel()

	t.Run("nil artifacts returns nil", func(t *testing.T) {
		sp := &dalec.SubPackage{}
		result := generateSubPackagePostinst(nil, sp, "test-pkg", "")
		assert.Assert(t, result == nil)
	})

	t.Run("empty artifacts returns nil", func(t *testing.T) {
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{},
		}
		result := generateSubPackagePostinst(nil, sp, "test-pkg", "")
		assert.Assert(t, result == nil)
	})

	t.Run("user creation included", func(t *testing.T) {
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Users: []dalec.AddUserConfig{
					{Name: "spuser"},
				},
			},
		}
		result := generateSubPackagePostinst(nil, sp, "test-contrib", "")
		assert.Assert(t, result != nil)
		assert.Assert(t, bytes.Contains(result, []byte("#DEBHELPER#")))
		assert.Assert(t, bytes.Contains(result, []byte("useradd spuser")))
	})

	t.Run("group creation included", func(t *testing.T) {
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Groups: []dalec.AddGroupConfig{
					{Name: "spgroup"},
				},
			},
		}
		result := generateSubPackagePostinst(nil, sp, "test-contrib", "")
		assert.Assert(t, result != nil)
		assert.Assert(t, bytes.Contains(result, []byte("#DEBHELPER#")))
		assert.Assert(t, bytes.Contains(result, []byte("groupadd --system spgroup")))
	})

	t.Run("ownership included for binaries", func(t *testing.T) {
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"mybin": {User: "svcuser", Group: "svcgroup"},
				},
			},
		}
		result := generateSubPackagePostinst(nil, sp, "test-contrib", "")
		assert.Assert(t, result != nil)
		assert.Assert(t, bytes.Contains(result, []byte(`chown -R svcuser "$DESTDIR/usr/bin/mybin"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chgrp -R svcgroup "$DESTDIR/usr/bin/mybin"`)))
	})

	t.Run("capabilities included for binaries", func(t *testing.T) {
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"mybin": {
						LinuxCapabilities: []dalec.ArtifactCapability{
							{Name: "cap_net_raw", Effective: true, Permitted: true},
						},
					},
				},
			},
		}
		result := generateSubPackagePostinst(nil, sp, "test-contrib", "")
		assert.Assert(t, result != nil)
		assert.Assert(t, bytes.Contains(result, []byte(`setcap 'cap_net_raw=ep' "$DESTDIR/usr/bin/mybin"`)))
	})

	t.Run("combined users groups ownership and capabilities", func(t *testing.T) {
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Users: []dalec.AddUserConfig{
					{Name: "appuser"},
				},
				Groups: []dalec.AddGroupConfig{
					{Name: "appgroup"},
				},
				Binaries: map[string]dalec.ArtifactConfig{
					"app": {
						User:  "appuser",
						Group: "appgroup",
						LinuxCapabilities: []dalec.ArtifactCapability{
							{Name: "cap_net_bind_service", Effective: true, Permitted: true},
						},
					},
				},
				Libs: map[string]dalec.ArtifactConfig{
					"libapp.so": {User: "appuser"},
				},
			},
		}
		result := generateSubPackagePostinst(nil, sp, "test-contrib", "")
		assert.Assert(t, result != nil)
		assert.Assert(t, bytes.Contains(result, []byte("#!/usr/bin/env sh")))
		assert.Assert(t, bytes.Contains(result, []byte("set -e")))
		assert.Assert(t, bytes.Contains(result, []byte("#DEBHELPER#")))
		assert.Assert(t, bytes.Contains(result, []byte("useradd appuser")))
		assert.Assert(t, bytes.Contains(result, []byte("groupadd --system appgroup")))
		assert.Assert(t, bytes.Contains(result, []byte(`chown -R appuser "$DESTDIR/usr/bin/app"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chgrp -R appgroup "$DESTDIR/usr/bin/app"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chown -R appuser "$DESTDIR/usr/lib/libapp.so"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`setcap 'cap_net_bind_service=ep' "$DESTDIR/usr/bin/app"`)))
	})
}

func TestSetSubPackageArtifactOwnershipPostInst(t *testing.T) {
	t.Parallel()

	t.Run("nil artifacts produces no output", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-pkg")
		assert.Equal(t, buf.Len(), 0)
	})

	t.Run("binaries ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"mybin": {User: "svcuser", Group: "svcgrp"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R svcuser "$DESTDIR/usr/bin/mybin"`)))
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R svcgrp "$DESTDIR/usr/bin/mybin"`)))
	})

	t.Run("config files ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				ConfigFiles: map[string]dalec.ArtifactConfig{
					"myapp.conf": {User: "root", Group: "wheel"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R root "$DESTDIR/etc/myapp.conf"`)))
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R wheel "$DESTDIR/etc/myapp.conf"`)))
	})

	t.Run("libs ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"libfoo.so": {User: "libuser"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R libuser "$DESTDIR/usr/lib/libfoo.so"`)))
	})

	t.Run("libexec ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Libexec: map[string]dalec.ArtifactConfig{
					"helper": {Group: "helpergrp"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R helpergrp "$DESTDIR/usr/libexec/helper"`)))
	})

	t.Run("data dirs ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				DataDirs: map[string]dalec.ArtifactConfig{
					"mydata": {User: "datauser", Group: "datagrp"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R datauser "$DESTDIR/usr/share/mydata"`)))
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R datagrp "$DESTDIR/usr/share/mydata"`)))
	})

	t.Run("docs ownership uses resolved name", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"README": {User: "docuser"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R docuser "$DESTDIR/usr/share/doc/test-contrib/README"`)))
	})

	t.Run("licenses ownership uses resolved name", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Licenses: map[string]dalec.ArtifactConfig{
					"LICENSE": {Group: "licgrp"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R licgrp "$DESTDIR/usr/share/doc/test-contrib/LICENSE"`)))
	})

	t.Run("manpages ownership uses resolved name", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Manpages: map[string]dalec.ArtifactConfig{
					"foo.1": {User: "manuser"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R manuser "$DESTDIR/usr/share/doc/manpages/test-contrib/foo.1"`)))
	})

	t.Run("headers ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Headers: map[string]dalec.ArtifactConfig{
					"myheader.h": {User: "hdruser", Group: "hdrgrp"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R hdruser "$DESTDIR/usr/include/myheader.h"`)))
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R hdrgrp "$DESTDIR/usr/include/myheader.h"`)))
	})

	t.Run("config directories ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Directories: &dalec.CreateArtifactDirectories{
					Config: map[string]dalec.ArtifactDirConfig{
						"myapp": {User: "cfguser", Group: "cfggrp"},
					},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R cfguser "$DESTDIR/etc/myapp"`)))
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R cfggrp "$DESTDIR/etc/myapp"`)))
	})

	t.Run("state directories ownership", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Directories: &dalec.CreateArtifactDirectories{
					State: map[string]dalec.ArtifactDirConfig{
						"myapp": {User: "stuser", Group: "stgrp"},
					},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R stuser "$DESTDIR/var/lib/myapp"`)))
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -R stgrp "$DESTDIR/var/lib/myapp"`)))
	})

	t.Run("symlinks use dash h flags", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Links: []dalec.ArtifactSymlinkConfig{
					{Source: "/usr/bin/real", Dest: "/usr/bin/alias", User: "lnuser", Group: "lngrp"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -h lnuser "$DESTDIR/usr/bin/alias"`)))
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chgrp -h lngrp "$DESTDIR/usr/bin/alias"`)))
	})

	t.Run("subpath included in path", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"nested": {SubPath: "deep", User: "subuser"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R subuser "$DESTDIR/usr/bin/deep/nested"`)))
	})

	t.Run("name override in artifact", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"original": {Name: "renamed", User: "rnuser"},
				},
			},
		}
		setSubPackageArtifactOwnershipPostInst(buf, sp, "test-contrib")
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`chown -R rnuser "$DESTDIR/usr/bin/renamed"`)))
		// Should NOT contain the original name in the path
		assert.Assert(t, !bytes.Contains(buf.Bytes(), []byte("/original")))
	})
}

func TestSetSubPackageArtifactCapabilitiesPostInst(t *testing.T) {
	t.Parallel()

	t.Run("nil artifacts produces no output", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{}
		setSubPackageArtifactCapabilitiesPostInst(buf, sp)
		assert.Equal(t, buf.Len(), 0)
	})

	t.Run("binaries with capabilities", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"mybin": {
						LinuxCapabilities: []dalec.ArtifactCapability{
							{Name: "cap_net_raw", Effective: true, Permitted: true},
						},
					},
				},
			},
		}
		setSubPackageArtifactCapabilitiesPostInst(buf, sp)
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`setcap 'cap_net_raw=ep' "$DESTDIR/usr/bin/mybin"`)))
	})

	t.Run("libs with capabilities", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"libfoo.so": {
						LinuxCapabilities: []dalec.ArtifactCapability{
							{Name: "cap_sys_admin", Effective: true, Permitted: true, Inheritable: true},
						},
					},
				},
			},
		}
		setSubPackageArtifactCapabilitiesPostInst(buf, sp)
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`setcap 'cap_sys_admin=eip' "$DESTDIR/usr/lib/libfoo.so"`)))
	})

	t.Run("libexec with capabilities", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Libexec: map[string]dalec.ArtifactConfig{
					"helper": {
						LinuxCapabilities: []dalec.ArtifactCapability{
							{Name: "cap_net_bind_service", Effective: true, Permitted: true},
						},
					},
				},
			},
		}
		setSubPackageArtifactCapabilitiesPostInst(buf, sp)
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`setcap 'cap_net_bind_service=ep' "$DESTDIR/usr/libexec/helper"`)))
	})

	t.Run("artifacts without capabilities produce no output", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"mybin": {},
				},
				Libs: map[string]dalec.ArtifactConfig{
					"libfoo.so": {},
				},
			},
		}
		setSubPackageArtifactCapabilitiesPostInst(buf, sp)
		assert.Equal(t, buf.Len(), 0)
	})

	t.Run("subpath included in capability path", func(t *testing.T) {
		buf := bytes.NewBuffer(nil)
		sp := &dalec.SubPackage{
			Artifacts: &dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"nested": {
						SubPath: "deep",
						LinuxCapabilities: []dalec.ArtifactCapability{
							{Name: "cap_net_raw", Effective: true, Permitted: true},
						},
					},
				},
			},
		}
		setSubPackageArtifactCapabilitiesPostInst(buf, sp)
		assert.Assert(t, bytes.Contains(buf.Bytes(), []byte(`setcap 'cap_net_raw=ep' "$DESTDIR/usr/bin/deep/nested"`)))
	})
}

func TestSubPackagePostinstViaDebroot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Website:     "https://example.invalid",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Packages: map[string]dalec.SubPackage{
			"contrib": {
				Description: "Contrib sub-package",
				Artifacts: &dalec.Artifacts{
					Users: []dalec.AddUserConfig{
						{Name: "contribuser"},
					},
					Groups: []dalec.AddGroupConfig{
						{Name: "contribgroup"},
					},
					Binaries: map[string]dalec.ArtifactConfig{
						"contrib-bin": {User: "contribuser", Group: "contribgroup"},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-contrib.postinst"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected sub-package postinst file to be generated")

	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("#DEBHELPER#")))
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("useradd contribuser")))
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("groupadd --system contribgroup")))
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte(`chown -R contribuser "$DESTDIR/usr/bin/contrib-bin"`)))
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte(`chgrp -R contribgroup "$DESTDIR/usr/bin/contrib-bin"`)))
}

func TestFixupArtifactPermsSubPackage(t *testing.T) {
	t.Parallel()

	t.Run("subpackage with explicit permissions", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "mypkg",
			Version: "1.0.0",
			Packages: map[string]dalec.SubPackage{
				"contrib": {
					Description: "Contrib sub-package",
					Artifacts: &dalec.Artifacts{
						Binaries: map[string]dalec.ArtifactConfig{
							"contrib-bin": {Permissions: 0o755},
						},
						ConfigFiles: map[string]dalec.ArtifactConfig{
							"contrib.conf": {Permissions: 0o644},
						},
					},
				},
			},
		}

		result := fixupArtifactPerms(spec, "", &SourcePkgConfig{})
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 755 "debian/mypkg-contrib/usr/bin/contrib-bin"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 644 "debian/mypkg-contrib/etc/contrib.conf"`)))
	})

	t.Run("subpackage with subpath permissions", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "mypkg",
			Version: "1.0.0",
			Packages: map[string]dalec.SubPackage{
				"contrib": {
					Artifacts: &dalec.Artifacts{
						Binaries: map[string]dalec.ArtifactConfig{
							"nested-bin": {SubPath: "tools", Permissions: 0o755},
						},
					},
				},
			},
		}

		result := fixupArtifactPerms(spec, "", &SourcePkgConfig{})
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 755 "debian/mypkg-contrib/usr/bin/tools/nested-bin"`)))
	})

	t.Run("subpackage with name override uses resolved name for base path", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "mypkg",
			Version: "1.0.0",
			Packages: map[string]dalec.SubPackage{
				"contrib": {
					Name: "custom-name",
					Artifacts: &dalec.Artifacts{
						Binaries: map[string]dalec.ArtifactConfig{
							"mybin": {Permissions: 0o755},
						},
					},
				},
			},
		}

		result := fixupArtifactPerms(spec, "", &SourcePkgConfig{})
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 755 "debian/custom-name/usr/bin/mybin"`)))
		// Should NOT use the default name
		assert.Assert(t, !bytes.Contains(result, []byte("mypkg-contrib")))
	})

	t.Run("subpackage with inline source file permissions", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "mypkg",
			Version: "1.0.0",
			Sources: map[string]dalec.Source{
				"myconf": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "config data",
							Permissions: 0o600,
						},
					},
				},
			},
			Packages: map[string]dalec.SubPackage{
				"contrib": {
					Artifacts: &dalec.Artifacts{
						ConfigFiles: map[string]dalec.ArtifactConfig{
							"myconf": {},
						},
					},
				},
			},
		}

		result := fixupArtifactPerms(spec, "", &SourcePkgConfig{})
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 600 "debian/mypkg-contrib/etc/myconf"`)))
	})

	t.Run("subpackage directory permissions", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "mypkg",
			Version: "1.0.0",
			Packages: map[string]dalec.SubPackage{
				"contrib": {
					Artifacts: &dalec.Artifacts{
						Directories: &dalec.CreateArtifactDirectories{
							Config: map[string]dalec.ArtifactDirConfig{
								"myapp": {Mode: 0o750},
							},
							State: map[string]dalec.ArtifactDirConfig{
								"myapp": {Mode: 0o700},
							},
						},
					},
				},
			},
		}

		result := fixupArtifactPerms(spec, "", &SourcePkgConfig{})
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 750 "debian/mypkg-contrib/etc/myapp"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 700 "debian/mypkg-contrib/var/lib/myapp"`)))
	})

	t.Run("subpackage with no permissions generates nothing for that package", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "mypkg",
			Version: "1.0.0",
			Packages: map[string]dalec.SubPackage{
				"contrib": {
					Artifacts: &dalec.Artifacts{
						Binaries: map[string]dalec.ArtifactConfig{
							"mybin": {}, // no permissions set
						},
					},
				},
			},
		}

		result := fixupArtifactPerms(spec, "", &SourcePkgConfig{})
		assert.Assert(t, !bytes.Contains(result, []byte("mypkg-contrib")))
	})

	t.Run("subpackage covers all artifact types", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "mypkg",
			Version: "1.0.0",
			Packages: map[string]dalec.SubPackage{
				"contrib": {
					Artifacts: &dalec.Artifacts{
						Libs: map[string]dalec.ArtifactConfig{
							"libfoo.so": {Permissions: 0o755},
						},
						Libexec: map[string]dalec.ArtifactConfig{
							"helper": {Permissions: 0o755},
						},
						Headers: map[string]dalec.ArtifactConfig{
							"foo.h": {Permissions: 0o644},
						},
						Docs: map[string]dalec.ArtifactConfig{
							"README": {Permissions: 0o644},
						},
						Licenses: map[string]dalec.ArtifactConfig{
							"LICENSE": {Permissions: 0o644},
						},
						Manpages: map[string]dalec.ArtifactConfig{
							"foo.1": {Permissions: 0o644},
						},
						DataDirs: map[string]dalec.ArtifactConfig{
							"mydata": {Permissions: 0o644},
						},
					},
				},
			},
		}

		result := fixupArtifactPerms(spec, "", &SourcePkgConfig{})
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 755 "debian/mypkg-contrib/usr/lib/libfoo.so"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 755 "debian/mypkg-contrib/usr/libexec/helper"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 644 "debian/mypkg-contrib/usr/include/foo.h"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 644 "debian/mypkg-contrib/usr/share/doc/mypkg-contrib/README"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 644 "debian/mypkg-contrib/usr/share/doc/mypkg-contrib/LICENSE"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 644 "debian/mypkg-contrib/usr/share/doc/manpages/mypkg-contrib/foo.1"`)))
		assert.Assert(t, bytes.Contains(result, []byte(`chmod 644 "debian/mypkg-contrib/usr/share/mydata"`)))
	})
}

func TestSubPackageInstallScriptsViaDebroot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "mypkg",
		Description: "Test package",
		Website:     "https://example.invalid",
		Version:     "2.0.0",
		Revision:    "1",
		License:     "MIT",
		Packages: map[string]dalec.SubPackage{
			"contrib": {
				Description: "Contrib sub-package",
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"contrib-bin": {},
					},
					ConfigFiles: map[string]dalec.ArtifactConfig{
						"contrib.conf": {},
					},
					Headers: map[string]dalec.ArtifactConfig{
						"contrib.h": {},
					},
					DataDirs: map[string]dalec.ArtifactConfig{
						"contrib-data": {SubPath: "mypkg"},
					},
					Libs: map[string]dalec.ArtifactConfig{
						"libcontrib.so": {},
					},
					Libexec: map[string]dalec.ArtifactConfig{
						"contrib-helper": {},
					},
					Directories: &dalec.CreateArtifactDirectories{
						Config: map[string]dalec.ArtifactDirConfig{
							"mypkg-contrib": {},
						},
						State: map[string]dalec.ArtifactDirConfig{
							"mypkg-contrib": {},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	pb := def.ToPB()

	// Check .install file
	installFile, err := findMkfile(t, pb, filepath.Join("/debian", "mypkg-contrib.install"))
	assert.NilError(t, err)
	assert.Assert(t, installFile != nil, "expected .install file for sub-package")
	installContent := string(installFile.Data)

	// Verify install entries for each artifact type
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("do_install")), "expected do_install calls in .install file")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("contrib-bin")), "expected binary in .install file")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("contrib.conf")), "expected config file in .install file")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("contrib.h")), "expected header in .install file")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("contrib-data")), "expected data dir in .install file")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("libcontrib.so")), "expected lib in .install file")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("contrib-helper")), "expected libexec in .install file")

	// Verify correct paths are used
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("/usr/bin")), "expected binary path")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("/etc")), "expected config path")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("/usr/include")), "expected header path")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("/usr/share/mypkg")), "expected data dir subpath")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("/usr/lib")), "expected lib path")
	assert.Assert(t, bytes.Contains(installFile.Data, []byte("/usr/libexec")), "expected libexec path")

	_ = installContent

	// Check .dirs file for directories
	dirsFile, err := findMkfile(t, pb, filepath.Join("/debian", "mypkg-contrib.dirs"))
	assert.NilError(t, err)
	assert.Assert(t, dirsFile != nil, "expected .dirs file for sub-package")
	assert.Assert(t, bytes.Contains(dirsFile.Data, []byte("/etc/mypkg-contrib")), "expected config dir entry")
	assert.Assert(t, bytes.Contains(dirsFile.Data, []byte("/var/lib/mypkg-contrib")), "expected state dir entry")
}

func TestSubPackageInstallScriptsWithNameOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "mypkg",
		Description: "Test package",
		Website:     "https://example.invalid",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "MIT",
		Packages: map[string]dalec.SubPackage{
			"contrib": {
				Name:        "custom-contrib-name",
				Description: "Contrib with name override",
				Artifacts: &dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"mybin": {},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	// The install file should use the resolved name (custom override)
	installFile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "custom-contrib-name.install"))
	assert.NilError(t, err)
	assert.Assert(t, installFile != nil, "expected .install file using overridden name")

	// Should NOT exist with the default name
	defaultFile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "mypkg-contrib.install"))
	assert.NilError(t, err)
	assert.Assert(t, defaultFile == nil, "should not create install file with default name when override is set")
}

func findMkfile(t *testing.T, def *pb.Definition, path string) (*pb.FileActionMkFile, error) {
	for _, dt := range def.Def {
		var op pb.Op
		if err := op.Unmarshal(dt); err != nil {
			return nil, err
		}

		fileOp := op.GetFile()
		if fileOp == nil {
			continue
		}

		for _, action := range fileOp.Actions {
			mkfile := action.GetMkfile()
			if mkfile == nil {
				continue
			}

			t.Log(mkfile.Path)
			if filepath.Clean(mkfile.Path) == filepath.Clean(path) {
				return mkfile, nil
			}
		}
	}

	return nil, nil
}
