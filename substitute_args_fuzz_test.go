package dalec

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
)

const fuzzArgMaxLen = 32
const fuzzInputMaxLen = 256

func truncateString(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

func sanitizeArgKey(input string) string {
	var b strings.Builder
	b.Grow(fuzzArgMaxLen)

	for _, r := range input {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
		case r == '_':
			b.WriteRune('_')
		}

		if b.Len() >= fuzzArgMaxLen {
			return b.String()
		}
	}

	if b.Len() == 0 {
		return "ARG"
	}
	return b.String()
}

func uniqueArg(base string, taken map[string]struct{}) string {
	if taken == nil {
		taken = make(map[string]struct{})
	}

	base = sanitizeArgKey(base)
	if base == "" {
		base = "ARG"
	}

	if len(base) > fuzzArgMaxLen {
		base = base[:fuzzArgMaxLen]
	}

	candidate := base
	idx := 0

	for {
		if _, exists := taken[candidate]; !exists {
			taken[candidate] = struct{}{}
			return candidate
		}

		suffix := fmt.Sprintf("_%d", idx)
		idx++

		truncated := base
		if len(truncated)+len(suffix) > fuzzArgMaxLen {
			truncated = truncated[:fuzzArgMaxLen-len(suffix)]
		}
		candidate = truncated + suffix
	}
}

func buildSubstitutionSpec(primary, secondary, tertiary, defaultValue string) Spec {
	spec := Spec{
		Name:        "fuzzed",
		Description: "fuzz substitution harness",
		Version:     fmt.Sprintf("${%s}-${%s}", primary, tertiary),
		Revision:    fmt.Sprintf("${%s}", secondary),
		Args: map[string]string{
			primary:   defaultValue,
			secondary: primary,
			tertiary:  secondary,
		},
		Build: ArtifactBuild{
			Env: map[string]string{
				"GLOBAL": fmt.Sprintf("${%s}:${%s}", primary, secondary),
			},
			NetworkMode: fmt.Sprintf("${%s}", tertiary),
			Steps: []BuildStep{
				{
					Env: map[string]string{
						"STEP_ENV": fmt.Sprintf("${%s}-${%s}", secondary, tertiary),
					},
				},
			},
		},
		Sources: map[string]Source{
			"http": {
				Path:     fmt.Sprintf("/src/${%s}", primary),
				Includes: []string{fmt.Sprintf("${%s}", secondary)},
				Excludes: []string{fmt.Sprintf("${%s}", tertiary)},
				HTTP: &SourceHTTP{
					URL: fmt.Sprintf("https://example.com/${%s}/${%s}?q=${%s}", primary, secondary, tertiary),
				},
			},
		},
		Patches: map[string][]PatchSpec{
			"http": {
				{Source: "http", Path: fmt.Sprintf("${%s}/patch.diff", primary)},
			},
		},
		Provides: PackageDependencyList{
			"pkg-" + primary: {
				Version: []string{fmt.Sprintf("${%s}", tertiary)},
			},
		},
		Replaces: PackageDependencyList{
			"rpl-" + secondary: {
				Version: []string{fmt.Sprintf("${%s}", primary)},
			},
		},
		Conflicts: PackageDependencyList{
			"cfl-" + tertiary: {
				Version: []string{fmt.Sprintf("${%s}", secondary)},
			},
		},
		Dependencies: &PackageDependencies{
			Build: PackageDependencyList{
				"build-" + primary: {
					Version: []string{fmt.Sprintf("${%s}", primary)},
				},
			},
			Runtime: PackageDependencyList{
				"run-" + secondary: {
					Version: []string{fmt.Sprintf("${%s}", tertiary)},
				},
			},
		},
		Image: &ImageConfig{
			Labels: map[string]string{
				"org.example/" + primary: fmt.Sprintf("${%s}", secondary),
			},
		},
		Targets: map[string]Target{
			"target-" + primary: {
				Provides: PackageDependencyList{
					"target-provide": {
						Version: []string{fmt.Sprintf("${%s}", primary)},
					},
				},
				Replaces: PackageDependencyList{
					"target-replace": {
						Version: []string{fmt.Sprintf("${%s}", secondary)},
					},
				},
				Conflicts: PackageDependencyList{
					"target-conflict": {
						Version: []string{fmt.Sprintf("${%s}", tertiary)},
					},
				},
				Dependencies: &PackageDependencies{
					Runtime: PackageDependencyList{
						"target-run": {
							Version: []string{fmt.Sprintf("${%s}", tertiary)},
						},
					},
				},
			},
		},
	}

	return spec
}

func FuzzSpecSubstituteArgs(f *testing.F) {
	f.Add("FOO", "BAR", "VALUE", uint8(0))
	f.Add("alpha", "beta", "gamma", uint8(1))

	f.Fuzz(func(t *testing.T, declaredKey, envKey, envValue string, flag uint8) {
		declaredKey = truncateString(declaredKey, fuzzInputMaxLen)
		envKey = truncateString(envKey, fuzzInputMaxLen)
		envValue = truncateString(envValue, fuzzInputMaxLen)

		taken := make(map[string]struct{}, 4)
		primary := uniqueArg(declaredKey, taken)
		secondary := uniqueArg(envKey, taken)
		tertiary := uniqueArg(envValue, taken)

		spec := buildSubstitutionSpec(primary, secondary, tertiary, envValue)

		env := map[string]string{
			primary:   envValue,
			secondary: declaredKey,
			tertiary:  envKey,
		}

		unknownKey := "UNDECL_" + tertiary
		env[unknownKey] = declaredKey
		env["SOURCE_DATE_EPOCH"] = fmt.Sprintf("%d", len(envValue))

		if flag&0x1 == 0x1 {
			env["TARGETOS"] = "linux"
		}

		_ = spec.SubstituteArgs(env)

		if flag&0x2 == 0x2 {
			specAllow := buildSubstitutionSpec(primary, secondary, tertiary, envValue)
			_ = specAllow.SubstituteArgs(env, WithAllowAnyArg)
		}
	})
}
