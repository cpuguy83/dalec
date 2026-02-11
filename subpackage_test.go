package dalec

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestSubPackageResolvedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		sp         SubPackage
		parentName string
		key        string
		expected   string
	}{
		{
			name:       "empty name uses parent-key",
			sp:         SubPackage{},
			parentName: "mypkg",
			key:        "contrib",
			expected:   "mypkg-contrib",
		},
		{
			name:       "name override is returned",
			sp:         SubPackage{Name: "custom-name"},
			parentName: "mypkg",
			key:        "contrib",
			expected:   "custom-name",
		},
		{
			name:       "name override ignores parent and key",
			sp:         SubPackage{Name: "override"},
			parentName: "parent",
			key:        "key",
			expected:   "override",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.sp.ResolvedName(tt.parentName, tt.key)
			assert.Check(t, cmp.Equal(result, tt.expected))
		})
	}
}

func TestSubPackageValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sp   *SubPackage
	}{
		{
			name: "nil subpackage",
			sp:   nil,
		},
		{
			name: "no artifacts",
			sp:   &SubPackage{},
		},
		{
			name: "valid artifacts",
			sp: &SubPackage{
				Artifacts: &Artifacts{
					Binaries: map[string]ArtifactConfig{
						"foo": {},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.sp.validate()
			assert.NilError(t, err)
		})
	}
}

func TestGetSubPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		spec      Spec
		key       string
		targetKey string
		check     func(t *testing.T, sp *SubPackage)
	}{
		{
			name: "key not in packages returns nil",
			spec: Spec{
				Packages: map[string]SubPackage{
					"existing": {Description: "exists"},
				},
			},
			key:       "missing",
			targetKey: "any",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp == nil)
			},
		},
		{
			name: "key present no target returns base",
			spec: Spec{
				Packages: map[string]SubPackage{
					"contrib": {Description: "base description"},
				},
			},
			key:       "contrib",
			targetKey: "nonexistent",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, cmp.Equal(sp.Description, "base description"))
			},
		},
		{
			name: "target exists but does not override package",
			spec: Spec{
				Packages: map[string]SubPackage{
					"contrib": {Description: "base description"},
				},
				Targets: map[string]Target{
					"linux": {},
				},
			},
			key:       "contrib",
			targetKey: "linux",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, cmp.Equal(sp.Description, "base description"))
			},
		},
		{
			name: "target overrides description",
			spec: Spec{
				Packages: map[string]SubPackage{
					"contrib": {Description: "base description"},
				},
				Targets: map[string]Target{
					"linux": {
						Packages: map[string]SubPackage{
							"contrib": {Description: "target description"},
						},
					},
				},
			},
			key:       "contrib",
			targetKey: "linux",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, cmp.Equal(sp.Description, "target description"))
			},
		},
		{
			name: "target overrides artifacts",
			spec: Spec{
				Packages: map[string]SubPackage{
					"contrib": {
						Description: "base",
						Artifacts: &Artifacts{
							Binaries: map[string]ArtifactConfig{"old": {}},
						},
					},
				},
				Targets: map[string]Target{
					"linux": {
						Packages: map[string]SubPackage{
							"contrib": {
								Artifacts: &Artifacts{
									Binaries: map[string]ArtifactConfig{"new": {}},
								},
							},
						},
					},
				},
			},
			key:       "contrib",
			targetKey: "linux",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				// Description should be preserved from base since target didn't set it.
				assert.Check(t, cmp.Equal(sp.Description, "base"))
				// Artifacts should come from the target override.
				assert.Check(t, sp.Artifacts != nil)
				_, hasNew := sp.Artifacts.Binaries["new"]
				assert.Check(t, hasNew)
				_, hasOld := sp.Artifacts.Binaries["old"]
				assert.Check(t, !hasOld)
			},
		},
		{
			name: "target overrides dependencies",
			spec: Spec{
				Packages: map[string]SubPackage{
					"contrib": {
						Dependencies: &SubPackageDependencies{
							Runtime: PackageDependencyList{
								"base-dep": {},
							},
						},
					},
				},
				Targets: map[string]Target{
					"linux": {
						Packages: map[string]SubPackage{
							"contrib": {
								Dependencies: &SubPackageDependencies{
									Runtime: PackageDependencyList{
										"target-dep": {},
									},
								},
							},
						},
					},
				},
			},
			key:       "contrib",
			targetKey: "linux",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, sp.Dependencies != nil)
				_, hasTarget := sp.Dependencies.Runtime["target-dep"]
				assert.Check(t, hasTarget)
				_, hasBase := sp.Dependencies.Runtime["base-dep"]
				assert.Check(t, !hasBase)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := tt.spec.GetSubPackage(tt.key, tt.targetKey)
			tt.check(t, sp)
		})
	}
}

func TestMergeSubPackageDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		spec      Spec
		key       string
		targetKey string
		check     func(t *testing.T, sp *SubPackage)
	}{
		{
			name: "base runtime preserved when target deps empty",
			spec: Spec{
				Packages: map[string]SubPackage{
					"pkg": {
						Dependencies: &SubPackageDependencies{
							Runtime: PackageDependencyList{
								"base-rt": {},
							},
						},
					},
				},
				Targets: map[string]Target{
					"t1": {
						Packages: map[string]SubPackage{
							"pkg": {
								Dependencies: &SubPackageDependencies{},
							},
						},
					},
				},
			},
			key:       "pkg",
			targetKey: "t1",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, sp.Dependencies != nil)
				_, has := sp.Dependencies.Runtime["base-rt"]
				assert.Check(t, has)
			},
		},
		{
			name: "target runtime wins over base runtime",
			spec: Spec{
				Packages: map[string]SubPackage{
					"pkg": {
						Dependencies: &SubPackageDependencies{
							Runtime: PackageDependencyList{
								"base-rt": {},
							},
						},
					},
				},
				Targets: map[string]Target{
					"t1": {
						Packages: map[string]SubPackage{
							"pkg": {
								Dependencies: &SubPackageDependencies{
									Runtime: PackageDependencyList{
										"target-rt": {},
									},
								},
							},
						},
					},
				},
			},
			key:       "pkg",
			targetKey: "t1",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, sp.Dependencies != nil)
				_, hasTarget := sp.Dependencies.Runtime["target-rt"]
				assert.Check(t, hasTarget)
				_, hasBase := sp.Dependencies.Runtime["base-rt"]
				assert.Check(t, !hasBase)
			},
		},
		{
			name: "target recommends wins over base recommends",
			spec: Spec{
				Packages: map[string]SubPackage{
					"pkg": {
						Dependencies: &SubPackageDependencies{
							Recommends: PackageDependencyList{
								"base-rec": {},
							},
						},
					},
				},
				Targets: map[string]Target{
					"t1": {
						Packages: map[string]SubPackage{
							"pkg": {
								Dependencies: &SubPackageDependencies{
									Recommends: PackageDependencyList{
										"target-rec": {},
									},
								},
							},
						},
					},
				},
			},
			key:       "pkg",
			targetKey: "t1",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, sp.Dependencies != nil)
				_, hasTarget := sp.Dependencies.Recommends["target-rec"]
				assert.Check(t, hasTarget)
				_, hasBase := sp.Dependencies.Recommends["base-rec"]
				assert.Check(t, !hasBase)
			},
		},
		{
			name: "both nil deps returns base as-is",
			spec: Spec{
				Packages: map[string]SubPackage{
					"pkg": {Description: "no deps"},
				},
			},
			key:       "pkg",
			targetKey: "nonexistent",
			check: func(t *testing.T, sp *SubPackage) {
				assert.Check(t, sp != nil)
				assert.Check(t, sp.Dependencies == nil)
				assert.Check(t, cmp.Equal(sp.Description, "no deps"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := tt.spec.GetSubPackage(tt.key, tt.targetKey)
			tt.check(t, sp)
		})
	}
}

func TestSubPackageKeys(t *testing.T) {
	t.Parallel()

	spec := Spec{
		Packages: map[string]SubPackage{
			"charlie": {},
			"alpha":   {},
			"bravo":   {},
		},
	}

	keys := spec.SubPackageKeys()
	expected := []string{"alpha", "bravo", "charlie"}
	assert.Check(t, cmp.DeepEqual(keys, expected))
}
