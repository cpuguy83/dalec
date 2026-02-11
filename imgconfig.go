package dalec

import "github.com/pkg/errors"

// BuildImageConfig applies the merged image configuration from the spec to the
// given OCI image spec. For specs without named images, this uses the
// traditional 2-level merge (image + target.image).
func BuildImageConfig(spec *Spec, targetKey string, img *DockerImageSpec) error {
	cfg := img.Config
	if err := MergeImageConfig(&cfg, MergeSpecImage(spec, targetKey)); err != nil {
		return err
	}

	img.Config = cfg
	return nil
}

// BuildNamedImageConfig applies the merged image configuration for a named
// image definition to the given OCI image spec.
// The merge chain is: image (root defaults) -> images.<name> -> targets.<distro>.images.<name>
func BuildNamedImageConfig(spec *Spec, targetKey, imageName string, img *DockerImageSpec) error {
	resolved := spec.GetImageDefinition(imageName, targetKey)
	if resolved == nil {
		return errors.Errorf("image %q not found", imageName)
	}

	cfg := img.Config
	if err := MergeImageConfig(&cfg, &resolved.ImageConfig); err != nil {
		return err
	}

	img.Config = cfg
	return nil
}

// MergeSpecImage merges the root-level Image with the target-level Image
// override. This is the original 2-level merge used when no named images
// are defined.
func MergeSpecImage(spec *Spec, targetKey string) *ImageConfig {
	var cfg ImageConfig

	if spec.Image != nil {
		cfg = *spec.Image
	}

	if i := spec.Targets[targetKey].Image; i != nil {
		mergeImageConfigLayer(&cfg, i)
	}

	return &cfg
}

// mergeImageConfigLayer merges a source ImageConfig layer on top of a
// destination. This implements the field-level merge semantics described
// in the proposal:
//   - entrypoint, cmd, working_dir, stop_signal, base, user: Override
//   - env: Append
//   - volumes, labels: Map merge
//   - post: Full override (if src specifies post, it replaces dst's post entirely)
//   - bases: Full override (if src specifies bases, it replaces dst's bases entirely)
func mergeImageConfigLayer(dst, src *ImageConfig) {
	if src == nil {
		return
	}

	if src.Entrypoint != "" {
		dst.Entrypoint = src.Entrypoint
	}

	if src.Cmd != "" {
		dst.Cmd = src.Cmd
	}

	dst.Env = append(dst.Env, src.Env...)

	if len(src.Volumes) > 0 {
		if dst.Volumes == nil {
			dst.Volumes = make(map[string]struct{}, len(src.Volumes))
		}
		for k, v := range src.Volumes {
			dst.Volumes[k] = v
		}
	}

	if len(src.Labels) > 0 {
		if dst.Labels == nil {
			dst.Labels = make(map[string]string, len(src.Labels))
		}
		for k, v := range src.Labels {
			dst.Labels[k] = v
		}
	}

	if src.WorkingDir != "" {
		dst.WorkingDir = src.WorkingDir
	}

	if src.StopSignal != "" {
		dst.StopSignal = src.StopSignal
	}

	if src.Base != "" {
		dst.Base = src.Base
	}

	if src.User != "" {
		dst.User = src.User
	}

	// Post uses full-override semantics: if src specifies Post (non-nil),
	// it entirely replaces dst's Post.
	if src.Post != nil {
		dst.Post = src.Post
	}

	// Bases uses full-override semantics: if src specifies Bases (non-nil),
	// it entirely replaces dst's Bases.
	if src.Bases != nil {
		dst.Bases = src.Bases
	}
}
