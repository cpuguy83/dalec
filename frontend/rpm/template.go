package rpm

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/Azure/dalec"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
)

const gomodsName = "__gomods"
const buildScriptName = "build.sh"

var specTmpl = template.Must(template.New("spec").Funcs(tmplFuncs).Parse(strings.TrimSpace(`
Name: {{.Name}}
Version: {{.Version}}
Release: {{.Release}}%{?dist}
License: {{ .License }}
Summary: {{ .Description }}
{{ optionalField "URL" .Website -}}
{{ optionalField "Vendor" .Vendor -}}
{{ optionalField "Packager" .Packager -}}
{{ if .NoArch }}
BuildArch: noarch
{{ end }}
{{- .Sources -}}
{{- .Conflicts -}}
{{- .Provides -}}
{{- .Replaces -}}
{{- .Requires -}}

%description
{{.Description}}

{{ .PrepareSources -}}
{{ .BuildSteps -}}
{{ .Install -}}
{{ .Post -}}
{{ .PreUn -}}
{{ .PostUn -}}
{{ .Files -}}
{{ .Changelog -}}
`)))

func optionalField(key, value string) string {
	if value == "" {
		return ""
	}
	return key + ": " + value + "\n"
}

var tmplFuncs = map[string]any{
	"optionalField": optionalField,
}

type specWrapper struct {
	*dalec.Spec
	Target string
}

func (w *specWrapper) Changelog() (fmt.Stringer, error) {
	b := &strings.Builder{}

	if len(w.Spec.Changelog) == 0 {
		return b, nil
	}

	fmt.Fprintf(b, "%%changelog\n")
	for _, log := range w.Spec.Changelog {
		fmt.Fprintln(b, "* "+log.Date.Format("Mon Jan 2 2006")+" "+log.Author)

		for _, change := range log.Changes {
			fmt.Fprintln(b, "- "+change)
		}
	}

	b.WriteString("\n")
	return b, nil
}

func (w *specWrapper) Provides() fmt.Stringer {
	b := &strings.Builder{}

	ls := maps.Keys(w.Spec.Provides)
	slices.Sort(ls)

	for _, name := range ls {
		writeDep(b, "Provides", name, w.Spec.Replaces[name])
	}
	b.WriteString("\n")
	return b
}

func (w *specWrapper) Replaces() fmt.Stringer {
	b := &strings.Builder{}

	keys := dalec.SortMapKeys(w.Spec.Replaces)
	for _, name := range keys {
		writeDep(b, "Replaces", name, w.Spec.Replaces[name])
	}
	return b
}

func getSystemdRequires(cfg *dalec.SystemdConfiguration) string {
	var requires, orderRequires string
	if cfg.IsEmpty() {
		return ""
	}

	enabledUnits := cfg.EnabledUnits()
	if len(enabledUnits) > 0 {
		// if we are enabling any units, we need to require systemd
		// specifically for %post
		requires += "Requires(post): systemd\n"
		orderRequires += "OrderWithRequires(post): systemd\n"
	}

	// in any case where we have units as artifacts, we must require systemd
	// for %preun and %postun, as we are using the rpm systemd macros
	// in those stages which depend on systemctl
	requires +=
		`Requires(preun): systemd
Requires(postun): systemd
`

	orderRequires +=
		`OrderWithRequires(preun): systemd
OrderWithRequires(postun): systemd
`

	return requires + orderRequires
}

func (w *specWrapper) Requires() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.GetArtifacts(w.Target)

	// first write systemd requires if they exist,
	// as these do not come from dependencies in the spec
	b.WriteString(getSystemdRequires(artifacts.Systemd))

	deps := w.GetPackageDeps(w.Target)
	if deps == nil {
		return b
	}
	buildKeys := dalec.SortMapKeys(deps.Build)
	for _, name := range buildKeys {
		constraints := deps.Build[name]
		writeDep(b, "BuildRequires", name, constraints)
	}

	if len(deps.Build) > 0 && len(deps.Runtime) > 0 {
		b.WriteString("\n")
	}

	runtimeKeys := dalec.SortMapKeys(deps.Runtime)
	for _, name := range runtimeKeys {
		constraints := deps.Runtime[name]
		// TODO: consider if it makes sense to support sources satisfying runtime deps
		writeDep(b, "Requires", name, constraints)
	}

	b.WriteString("\n")
	return b
}

func writeDep(b *strings.Builder, kind, name string, constraints dalec.PackageConstraints) {
	do := func() {
		if len(constraints.Version) == 0 {
			fmt.Fprintf(b, "%s: %s\n", kind, name)
			return
		}

		for _, c := range constraints.Version {
			fmt.Fprintf(b, "%s: %s %s\n", kind, name, c)
		}
	}

	if len(constraints.Arch) == 0 {
		do()
		return
	}

	for _, arch := range constraints.Arch {
		fmt.Fprintf(b, "%%ifarch %s\n", arch)
		do()
		fmt.Fprintf(b, "%%endif\n")
	}
}

func (w *specWrapper) Conflicts() string {
	b := &strings.Builder{}

	keys := dalec.SortMapKeys(w.Spec.Conflicts)
	for _, name := range keys {
		constraints := w.Spec.Conflicts[name]
		writeDep(b, "Conflicts", name, constraints)
	}
	b.WriteString("\n")
	return b.String()
}

func (w *specWrapper) Sources() (fmt.Stringer, error) {
	b := &strings.Builder{}

	// Sort keys for consistent output
	keys := dalec.SortMapKeys(w.Spec.Sources)

	for idx, name := range keys {
		src := w.Spec.Sources[name]
		ref := name
		isDir := dalec.SourceIsDir(src)

		if isDir {
			ref += ".tar.gz"
		}

		doc, err := src.Doc(name)
		if err != nil {
			return nil, fmt.Errorf("error getting doc for source %s: %w", name, err)
		}

		scanner := bufio.NewScanner(doc)
		for scanner.Scan() {
			fmt.Fprintf(b, "# %s\n", scanner.Text())
		}
		if scanner.Err() != nil {
			return nil, scanner.Err()
		}
		fmt.Fprintf(b, "Source%d: %s\n", idx, ref)
	}

	sourceIdx := len(keys)

	if w.Spec.HasGomods() {
		fmt.Fprintf(b, "Source%d: %s.tar.gz\n", sourceIdx, gomodsName)
		sourceIdx += 1
	}

	if len(w.Spec.Build.Steps) > 0 {
		fmt.Fprintf(b, "Source%d: %s\n", sourceIdx, buildScriptName)
	}

	if len(keys) > 0 {
		b.WriteString("\n")
	}
	return b, nil
}

func (w *specWrapper) Release() string {
	if w.Spec.Revision == "" {
		return "1"
	}
	return w.Spec.Revision
}

func (w *specWrapper) PrepareSources() (fmt.Stringer, error) {
	b := &strings.Builder{}
	if len(w.Spec.Sources) == 0 {
		return b, nil
	}

	fmt.Fprintf(b, "%%prep\n")

	patches := make(map[string]bool)

	for _, v := range w.Spec.Patches {
		for _, p := range v {
			patches[p.Source] = true
		}
	}

	// Sort keys for consistent output
	keys := dalec.SortMapKeys(w.Spec.Sources)

	prepareGomods := sync.OnceFunc(func() {
		if !w.Spec.HasGomods() {
			return
		}
		fmt.Fprintf(b, "mkdir -p \"%%{_builddir}/%s\"\n", gomodsName)
		fmt.Fprintf(b, "tar -C \"%%{_builddir}/%s\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", gomodsName, gomodsName)
	})

	// Extract all the sources from the rpm source dir
	for _, key := range keys {
		if !dalec.SourceIsDir(w.Spec.Sources[key]) {
			// This is a file, nothing to extract, but we need to put it into place
			// in  the rpm build dir
			fmt.Fprintf(b, "cp -a \"%%{_sourcedir}/%s\" .\n", key)
			continue
		}
		// This is a directory source so it needs to be untarred into the rpm build dir.
		fmt.Fprintf(b, "mkdir -p \"%%{_builddir}/%s\"\n", key)
		fmt.Fprintf(b, "tar -C \"%%{_builddir}/%s\" -xzf \"%%{_sourcedir}/%s.tar.gz\"\n", key, key)
	}
	prepareGomods()

	// Apply patches to all sources.
	// Note: These are applied based on the key sorting algorithm (lexicographic).
	//  Using one patch to patch another patch is not supported, except that it may
	//  occur if they happen to be sorted lexicographically.
	patchKeys := dalec.SortMapKeys(w.Spec.Patches)
	for _, key := range patchKeys {
		for _, patch := range w.Spec.Patches[key] {
			fmt.Fprintf(b, "patch -d %q -p%d -s --input \"%%{_builddir}/%s\"\n", key, *patch.Strip, filepath.Join(patch.Source, patch.Path))
		}
	}

	if len(keys) > 0 {
		b.WriteString("\n")
	}
	return b, nil
}

func writeStep(b *strings.Builder, step dalec.BuildStep) {
	envKeys := dalec.SortMapKeys(step.Env)
	// Wrap commands in a subshell so any environment variables that are set
	// will be available to every command in the BuildStep
	fmt.Fprintln(b, "(") // begin subshell
	for _, k := range envKeys {
		fmt.Fprintf(b, "export %s=\"%s\"\n", k, step.Env[k])
	}
	fmt.Fprintf(b, "%s", step.Command)
	fmt.Fprintln(b, ")") // end subshell
}

func (w *specWrapper) BuildSteps() fmt.Stringer {
	b := &strings.Builder{}

	if len(w.Spec.Build.Steps) == 0 {
		return b
	}

	fmt.Fprintf(b, "%%build\n")
	fmt.Fprintf(b, "%%{_sourcedir}/%s\n", buildScriptName)
	b.WriteString("\n")

	return b
}

func (w *specWrapper) PreUn() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.GetArtifacts(w.Target)
	if artifacts.Systemd.IsEmpty() {
		return b
	}

	b.WriteString("%preun\n")
	keys := dalec.SortMapKeys(artifacts.Systemd.Units)
	for _, servicePath := range keys {
		serviceName := filepath.Base(servicePath)
		fmt.Fprintf(b, "%%systemd_preun %s\n", serviceName)
	}
	b.WriteString("\n")
	return b
}

func systemdPostScript(unitName string, cfg dalec.SystemdUnitConfig) string {
	// if service isn't explicitly specified as enabled in the spec,
	// then we don't need to do anything in the post script
	if !cfg.Enable {
		return ""
	}

	// should be equivalent to the systemd_post scriptlet in the rpm spec,
	// but without the use of a .preset file
	return fmt.Sprintf(`
if [ $1 -eq 1 ]; then
    # initial installation
    systemctl enable %s
fi
`, unitName)
}

func (w *specWrapper) Post() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.GetArtifacts(w.Target)
	if artifacts.Systemd.IsEmpty() {
		return b
	}

	enabledUnits := artifacts.Systemd.EnabledUnits()
	if len(enabledUnits) == 0 {
		// if we have no enabled units, we don't need to do anything systemd related
		// in the post script. In this case, we shouldn't emit '%post'
		// as this eliminates the need for extra dependencies in the target container
		return b
	}

	b.WriteString("%post\n")
	// TODO: can inject other post install steps here in the future

	keys := dalec.SortMapKeys(enabledUnits)
	for _, servicePath := range keys {
		unitConf := artifacts.Systemd.Units[servicePath]
		artifact := unitConf.Artifact()
		b.WriteString(
			systemdPostScript(artifact.ResolveName(servicePath), unitConf),
		)
	}

	b.WriteString("\n")
	return b
}

func (w *specWrapper) PostUn() fmt.Stringer {
	b := &strings.Builder{}

	artifacts := w.GetArtifacts(w.Target)
	if artifacts.Systemd.IsEmpty() {
		return b
	}

	b.WriteString("%postun\n")
	keys := dalec.SortMapKeys(artifacts.Systemd.Units)
	for _, servicePath := range keys {
		cfg := artifacts.Systemd.Units[servicePath]
		a := cfg.Artifact()
		serviceName := a.ResolveName(servicePath)
		fmt.Fprintf(b, "%%systemd_postun %s\n", serviceName)
	}

	return b
}

func (w *specWrapper) Install() fmt.Stringer {
	b := &strings.Builder{}
	fmt.Fprintln(b, "%install")

	artifacts := w.GetArtifacts(w.Target)

	copyArtifact := func(root, p string, cfg *dalec.ArtifactConfig) {
		if cfg == nil {
			return
		}
		targetDir := filepath.Join(root, cfg.SubPath)
		fmt.Fprintln(b, "mkdir -p", targetDir)

		var targetPath string
		file := cfg.ResolveName(p)
		if !strings.Contains(file, "*") {
			targetPath = filepath.Join(targetDir, file)
		} else {
			targetPath = targetDir + "/"
		}
		fmt.Fprintln(b, "cp -r", p, targetPath)
	}

	if len(artifacts.Binaries) > 0 {
		binKeys := dalec.SortMapKeys(artifacts.Binaries)
		for _, p := range binKeys {
			cfg := artifacts.Binaries[p]
			copyArtifact(`%{buildroot}/%{_bindir}`, p, &cfg)
		}
	}

	if len(artifacts.Manpages) > 0 {
		manKeys := dalec.SortMapKeys(artifacts.Manpages)
		for _, p := range manKeys {
			cfg := artifacts.Manpages[p]
			copyArtifact(`%{buildroot}/%{_mandir}`, p, &cfg)
		}
	}

	createArtifactDir := func(root, p string, cfg dalec.ArtifactDirConfig) {
		dir := filepath.Join(root, p)
		mkdirCmd := "mkdir"
		perms := cfg.Mode.Perm()
		if perms != 0 {
			mkdirCmd += fmt.Sprintf(" -m %o", cfg.Mode)
		}
		fmt.Fprintf(b, "%s -p %q\n", mkdirCmd, dir)
	}

	if artifacts.Directories != nil {
		configKeys := dalec.SortMapKeys(artifacts.Directories.Config)
		for _, p := range configKeys {
			cfg := artifacts.Directories.Config[p]
			createArtifactDir(`%{buildroot}/%{_sysconfdir}`, p, cfg)
		}

		stateKeys := dalec.SortMapKeys(artifacts.Directories.State)
		for _, p := range stateKeys {
			cfg := artifacts.Directories.State[p]
			createArtifactDir(`%{buildroot}/%{_sharedstatedir}`, p, cfg)
		}
	}

	if len(artifacts.DataDirs) > 0 {
		dataFileKeys := dalec.SortMapKeys(artifacts.DataDirs)
		for _, k := range dataFileKeys {
			df := artifacts.DataDirs[k]
			copyArtifact(`%{buildroot}/%{_datadir}`, k, &df)
		}
	}

	if artifacts.Libexec != nil {
		libexecFileKeys := dalec.SortMapKeys(artifacts.Libexec)
		for _, k := range libexecFileKeys {
			le := artifacts.Libexec[k]
			copyArtifact(`%{buildroot}/%{_libexecdir}`, k, &le)
		}
	}

	configKeys := dalec.SortMapKeys(artifacts.ConfigFiles)
	for _, c := range configKeys {
		cfg := artifacts.ConfigFiles[c]
		copyArtifact(`%{buildroot}/%{_sysconfdir}`, c, &cfg)
	}

	if artifacts.Systemd != nil {
		serviceKeys := dalec.SortMapKeys(artifacts.Systemd.Units)
		for _, p := range serviceKeys {
			cfg := artifacts.Systemd.Units[p]
			// must include systemd unit extension (.service, .socket, .timer, etc.) in name
			copyArtifact(`%{buildroot}/%{_unitdir}`, p, cfg.Artifact())
		}

		dropinKeys := dalec.SortMapKeys(artifacts.Systemd.Dropins)
		for _, d := range dropinKeys {
			cfg := artifacts.Systemd.Dropins[d]
			copyArtifact(`%{buildroot}/%{_unitdir}`, d, cfg.Artifact())
		}
	}

	docKeys := dalec.SortMapKeys(artifacts.Docs)
	for _, d := range docKeys {
		cfg := artifacts.Docs[d]
		root := filepath.Join(`%{buildroot}/%{_docdir}`, w.Name)
		copyArtifact(root, d, &cfg)
	}

	licenseKeys := dalec.SortMapKeys(artifacts.Licenses)
	for _, l := range licenseKeys {
		cfg := artifacts.Licenses[l]
		root := filepath.Join(`%{buildroot}/%{_licensedir}`, w.Name)
		copyArtifact(root, l, &cfg)
	}

	libs := dalec.SortMapKeys(artifacts.Libs)
	for _, l := range libs {
		cfg := artifacts.Libs[l]
		root := filepath.Join(`%{buildroot}/%{_libdir}`, w.Name)
		copyArtifact(root, l, &cfg)
	}

	for _, l := range artifacts.Links {
		fmt.Fprintln(b, "mkdir -p", filepath.Dir(filepath.Join("%{buildroot}", l.Dest)))
		fmt.Fprintln(b, "ln -sf", l.Source, "%{buildroot}/"+l.Dest)
	}

	headersKeys := dalec.SortMapKeys(artifacts.Headers)
	for _, h := range headersKeys {
		cfg := artifacts.Headers[h]
		copyArtifact(`%{buildroot}/%{_includedir}`, h, &cfg)
	}
	b.WriteString("\n")
	return b
}

func (w *specWrapper) Files() fmt.Stringer {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%%files\n")

	artifacts := w.GetArtifacts(w.Target)

	if len(artifacts.Binaries) > 0 {
		binKeys := dalec.SortMapKeys(artifacts.Binaries)
		for _, p := range binKeys {
			cfg := artifacts.Binaries[p]
			full := filepath.Join(`%{_bindir}/`, cfg.SubPath, cfg.ResolveName(p))
			fmt.Fprintln(b, full)
		}
	}

	if len(artifacts.Manpages) > 0 {
		fmt.Fprintln(b, `%{_mandir}/*/*`)
	}

	if artifacts.Directories != nil {
		configKeys := dalec.SortMapKeys(artifacts.Directories.Config)
		for _, p := range configKeys {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sysconfdir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}

		stateKeys := dalec.SortMapKeys(artifacts.Directories.State)
		for _, p := range stateKeys {
			dir := strings.Join([]string{`%dir`, filepath.Join(`%{_sharedstatedir}`, p)}, " ")
			fmt.Fprintln(b, dir)
		}
	}

	if artifacts.DataDirs != nil {
		dataKeys := dalec.SortMapKeys(artifacts.DataDirs)
		for _, k := range dataKeys {
			df := artifacts.DataDirs[k]
			fullPath := filepath.Join(`%{_datadir}`, df.SubPath, df.ResolveName(k))
			fmt.Fprintln(b, fullPath)
		}
	}

	if artifacts.Libexec != nil {
		dataKeys := dalec.SortMapKeys(artifacts.Libexec)
		for _, k := range dataKeys {
			le := artifacts.Libexec[k]
			targetDir := filepath.Join(`%{_libexecdir}`, le.SubPath)
			fullPath := filepath.Join(targetDir, le.ResolveName(k))
			fmt.Fprintln(b, fullPath)
		}
	}

	configKeys := dalec.SortMapKeys(artifacts.ConfigFiles)
	for _, c := range configKeys {
		cfg := artifacts.ConfigFiles[c]
		fullPath := filepath.Join(`%{_sysconfdir}`, cfg.SubPath, cfg.ResolveName(c))
		fullDirective := strings.Join([]string{`%config(noreplace)`, fullPath}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	if artifacts.Systemd != nil {
		serviceKeys := dalec.SortMapKeys(artifacts.Systemd.Units)
		for _, p := range serviceKeys {
			cfg := artifacts.Systemd.Units[p]
			a := cfg.Artifact()
			unitPath := filepath.Join(`%{_unitdir}/`, a.SubPath, a.ResolveName(p))
			fmt.Fprintln(b, unitPath)
		}

		dropins := make(map[string][]string)
		// process these to get a unique list of files per unit name.
		// we need a single dir entry for the directory
		// need a file entry for each of files
		dropinKeys := dalec.SortMapKeys(artifacts.Systemd.Dropins)
		for _, d := range dropinKeys {
			cfg := artifacts.Systemd.Dropins[d]
			art := cfg.Artifact()
			files, ok := dropins[cfg.Unit]
			if !ok {
				files = []string{}
			}
			p := filepath.Join(
				`%{_unitdir}`,
				fmt.Sprintf("%s.d", cfg.Unit),
				art.ResolveName(d),
			)
			dropins[cfg.Unit] = append(files, p)
		}
		unitNames := dalec.SortMapKeys(dropins)
		for _, u := range unitNames {
			dir := strings.Join([]string{
				`%dir`,
				filepath.Join(
					`%{_unitdir}`,
					fmt.Sprintf("%s.d", u),
				),
			}, " ")
			fmt.Fprintln(b, dir)

			for _, file := range dropins[u] {
				fmt.Fprintln(b, file)
			}
		}
	}

	docKeys := dalec.SortMapKeys(artifacts.Docs)
	for _, d := range docKeys {
		cfg := artifacts.Docs[d]
		path := filepath.Join(`%{_docdir}`, w.Name, cfg.SubPath, cfg.ResolveName(d))
		fullDirective := strings.Join([]string{`%doc`, path}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	licenseKeys := dalec.SortMapKeys(artifacts.Licenses)
	for _, l := range licenseKeys {
		cfg := artifacts.Licenses[l]
		path := filepath.Join(`%{_licensedir}`, w.Name, cfg.SubPath, cfg.ResolveName(l))
		fullDirective := strings.Join([]string{`%license`, path}, " ")
		fmt.Fprintln(b, fullDirective)
	}

	libKeys := dalec.SortMapKeys(artifacts.Libs)
	for _, l := range libKeys {
		cfg := artifacts.Libs[l]
		path := filepath.Join(`%{_libdir}`, w.Name, cfg.SubPath, cfg.ResolveName(l))
		fmt.Fprintln(b, path)
	}

	for _, l := range artifacts.Links {
		fmt.Fprintln(b, l.Dest)
	}

	if len(artifacts.Headers) > 0 {
		headersKeys := dalec.SortMapKeys(artifacts.Headers)
		for _, h := range headersKeys {
			hf := artifacts.Headers[h]
			path := filepath.Join(`%{_includedir}`, hf.SubPath, hf.ResolveName(h))
			fmt.Fprintln(b, path)
		}
	}
	b.WriteString("\n")
	return b
}

// WriteSpec generates an rpm spec from the provided [dalec.Spec] and distro target and writes it to the passed in writer
func WriteSpec(spec *dalec.Spec, target string, w io.Writer) error {
	s := &specWrapper{spec, target}

	err := specTmpl.Execute(w, s)
	if err != nil {
		return fmt.Errorf("error executing spec template: %w", err)
	}
	return nil
}
