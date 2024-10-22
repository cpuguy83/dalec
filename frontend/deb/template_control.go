package deb

import (
	_ "embed"
	"fmt"
	"io"
	"maps"
	"strings"
	"text/template"

	"github.com/Azure/dalec"
	"golang.org/x/exp/slices"
)

func WriteControl(spec *dalec.Spec, target string, w io.Writer) error {
	return controlTmpl.Execute(w, &controlWrapper{spec, target})
}

type controlWrapper struct {
	*dalec.Spec
	Target string
}

func (w *controlWrapper) Architecture() string {
	if w.NoArch {
		return "all"
	}
	return "linux-any"
}

// appendConstraints takes an input list of packages and returns a new list of
// packages with the constraints appended for use in a debian/control file.
// The output list is sorted lexicographically.
func appendConstraints(deps map[string]dalec.PackageConstraints) []string {
	if deps == nil {
		return nil
	}
	out := dalec.SortMapKeys(deps)

	for i, dep := range out {
		constraints := deps[dep]
		s := dep
		// Format is specified in https://www.debian.org/doc/debian-policy/ch-relationships.html#syntax-of-relationship-fields
		if len(constraints.Version) > 0 {
			ls := constraints.Version
			slices.Sort(ls)
			s = fmt.Sprintf("%s (%s)", s, strings.Join(ls, ", "))
		}
		if len(constraints.Arch) > 0 {
			ls := constraints.Arch
			slices.Sort(ls)
			s = fmt.Sprintf("%s [%s]", s, strings.Join(ls, ", "))
		}
		out[i] = s
	}

	return out
}

func (w *controlWrapper) depends(buf io.Writer, depsSpec *dalec.PackageDependencies) {
	var (
		needsClone bool
		rtDeps     map[string]dalec.PackageConstraints
	)
	if depsSpec == nil || depsSpec.Runtime == nil {
		rtDeps = make(map[string]dalec.PackageConstraints)
	} else {
		rtDeps = depsSpec.Runtime
		needsClone = true
	}

	// Add in deps vars that will get resolved by debbuild
	// In some cases these are not neccessary (maybe even most), but when they are
	// it is important.
	// When not needed lintian may throw warnings but that's ok.
	// If these aren't actually needed they'll resolve to nothing and don't cause
	// any changes.
	const (
		shlibsDeps = "${shlibs:Depends}"
		miscDeps   = "${misc:Depends}"
	)

	if _, exists := rtDeps[shlibsDeps]; !exists {
		if needsClone {
			rtDeps = maps.Clone(rtDeps)
			needsClone = false
		}

		rtDeps[shlibsDeps] = dalec.PackageConstraints{}
	}

	if _, exists := rtDeps[miscDeps]; !exists {
		if needsClone {
			rtDeps = maps.Clone(rtDeps)
		}
		rtDeps[miscDeps] = dalec.PackageConstraints{}
	}

	deps := appendConstraints(rtDeps)
	fmt.Fprintln(buf, multiline("Depends", deps))
}

// multiline attempts to format a field with multiple values in a way that is more human readable
// with line breaks and indentation.
func multiline(field string, values []string) string {
	return fmt.Sprintf("%s: %s", field, strings.Join(values, ",\n"+strings.Repeat(" ", len(field)+2)))
}

func (w *controlWrapper) recommends(buf io.Writer, depsSpec *dalec.PackageDependencies) {
	if len(depsSpec.Recommends) == 0 {
		return
	}

	deps := appendConstraints(depsSpec.Recommends)
	fmt.Fprintln(buf, multiline("Recommends", deps))
}

func (w *controlWrapper) BuildDeps() fmt.Stringer {
	b := &strings.Builder{}

	depsSpec := w.Spec.GetPackageDeps(w.Target)

	var deps []string
	if depsSpec != nil {
		deps = appendConstraints(depsSpec.Build)
	}

	deps = append(deps, fmt.Sprintf("debhelper-compat (= %s)", DebHelperCompat))

	fmt.Fprintln(b, multiline("Build-Depends", deps))
	return b
}

func (w *controlWrapper) AllRuntimeDeps() fmt.Stringer {
	b := &strings.Builder{}

	deps := w.Spec.GetPackageDeps(w.Target)

	if deps == nil {
		return b
	}

	w.depends(b, deps)
	w.recommends(b, deps)

	return b
}

func (w *controlWrapper) Replaces() fmt.Stringer {
	b := &strings.Builder{}
	if len(w.Spec.Replaces) == 0 {
		return b
	}

	ls := appendConstraints(w.Spec.Replaces)

	fmt.Fprintln(b, multiline("Replaces", ls))
	return b
}

func (w *controlWrapper) Conflicts() fmt.Stringer {
	b := &strings.Builder{}
	if len(w.Spec.Conflicts) == 0 {
		return b
	}

	ls := appendConstraints(w.Spec.Conflicts)
	fmt.Fprintln(b, multiline("Conflicts", ls))
	return b
}

func (w *controlWrapper) Provides() fmt.Stringer {
	b := &strings.Builder{}
	if len(w.Spec.Provides) == 0 {
		return b
	}

	ls := appendConstraints(w.Spec.Provides)
	fmt.Fprintln(b, multiline("Provides", ls))
	return b
}

var (
	//go:embed templates/debian_control.tmpl
	controlTmplContent []byte

	controlTmpl = template.Must(template.New("control").Parse(string(controlTmplContent)))
)
