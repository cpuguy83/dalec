package distro

import (
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestFilterPackagesDEB(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	c := &Config{}
	pkgState := llb.Scratch().File(llb.Mkfile("/dummy.deb", 0644, nil))

	t.Run("empty_names_returns_original_state", func(t *testing.T) {
		t.Parallel()

		spec := &dalec.Spec{Version: "1.0.0"}
		result := c.FilterPackages(pkgState, spec, nil)

		// Marshal both states and compare — they should be identical.
		wantDef, err := pkgState.Marshal(ctx)
		assert.NilError(t, err)

		gotDef, err := result.Marshal(ctx)
		assert.NilError(t, err)

		assert.Check(t, cmp.DeepEqual(wantDef.ToPB().Def, gotDef.ToPB().Def))
	})

	t.Run("single_package", func(t *testing.T) {
		t.Parallel()

		spec := &dalec.Spec{Version: "2.5.0"}
		result := c.FilterPackages(pkgState, spec, []string{"mypackage"})

		ops, err := test.LLBOpsFromState(ctx, result)
		assert.NilError(t, err)

		includes := findCopyIncludePatterns(t, ops)
		assert.Check(t, cmp.DeepEqual(includes, []string{"mypackage_2.5.0-*.deb"}))
	})

	t.Run("multiple_packages", func(t *testing.T) {
		t.Parallel()

		spec := &dalec.Spec{Version: "1.0.0"}
		result := c.FilterPackages(pkgState, spec, []string{"foo", "foo-contrib"})

		ops, err := test.LLBOpsFromState(ctx, result)
		assert.NilError(t, err)

		includes := findCopyIncludePatterns(t, ops)
		assert.Check(t, cmp.DeepEqual(includes, []string{
			"foo_1.0.0-*.deb",
			"foo-contrib_1.0.0-*.deb",
		}))
	})

	t.Run("pattern_uses_underscore_separator", func(t *testing.T) {
		t.Parallel()

		// Ensures "foo_1.0.0-*.deb" won't accidentally match "foo-contrib_1.0.0-*.deb"
		// because the separator is '_' not '-'.
		spec := &dalec.Spec{Version: "3.0.0"}
		result := c.FilterPackages(pkgState, spec, []string{"foo"})

		ops, err := test.LLBOpsFromState(ctx, result)
		assert.NilError(t, err)

		includes := findCopyIncludePatterns(t, ops)
		assert.Assert(t, len(includes) == 1)
		// The pattern must use underscore, not hyphen, between name and version.
		assert.Equal(t, includes[0], "foo_3.0.0-*.deb")
	})
}

// findCopyIncludePatterns finds the first CopyOp in the ops and returns its IncludePatterns.
func findCopyIncludePatterns(t *testing.T, ops []test.LLBOp) []string {
	t.Helper()
	for _, op := range ops {
		f := op.Op.GetFile()
		if f == nil {
			continue
		}
		for _, a := range f.Actions {
			cp := a.GetCopy()
			if cp != nil {
				return cp.IncludePatterns
			}
		}
	}
	t.Fatal("no CopyOp found in LLB ops")
	return nil
}
