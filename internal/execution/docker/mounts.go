package docker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/moby/moby/api/types/mount"

	"github.com/wandxy/morph/internal/execution"
)

var statMountSource = os.Stat

func buildMounts(exposure execution.Exposure, workspaceVolume string) ([]mount.Mount, error) {
	result := make([]mount.Mount, 0, len(exposure.Mounts())+1)
	if exposure.WorkspaceMode() == execution.WorkspaceNone {
		if strings.TrimSpace(workspaceVolume) == "" {
			return nil, errors.New("docker private workspace volume is required")
		}
		result = append(
			result,
			mount.Mount{
				Type:   mount.TypeVolume,
				Source: workspaceVolume,
				Target: "/workspace",
			},
		)
	}
	for _, configured := range exposure.Mounts() {
		source, err := prepareMountSource(configured)
		if err != nil {
			return nil, err
		}
		result = append(result, mount.Mount{
			Type:     mount.TypeBind,
			Source:   source,
			Target:   configured.Target,
			ReadOnly: configured.Mode == execution.MountReadOnly,
			BindOptions: &mount.BindOptions{
				Propagation:            mount.PropagationRPrivate,
				ReadOnlyForceRecursive: configured.Mode == execution.MountReadOnly,
			},
		})
	}
	return result, nil
}

func prepareMountSource(configured execution.Mount) (string, error) {
	if configured.Create {
		if err := os.MkdirAll(configured.SourceIdentity, 0o700); err != nil {
			return "", err
		}
	}
	source, err := canonicalMountSource(configured.SourceIdentity)
	if err != nil {
		return "", err
	}
	if source != filepath.Clean(configured.SourceIdentity) {
		return "", errors.New("docker mount source changed after authorization")
	}
	return source, nil
}

func canonicalMountSource(source string) (string, error) {
	if !filepath.IsAbs(source) {
		return "", errors.New("docker mount source must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(source))
	if err != nil {
		return "", err
	}
	info, err := statMountSource(canonical)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSocket != 0 || info.Mode()&os.ModeDevice != 0 {
		return "", errors.New("docker mount source type is blocked")
	}
	blocked := []string{
		"/",
		"/etc",
		"/proc",
		"/sys",
		"/dev",
		"/run",
		"/tmp",
		"/var/run",
		"/var/lib/docker",
		"/private/etc",
		"/private/tmp",
		"/private/var/run",
		"/private/var/lib/docker",
	}
	if home, err := os.UserHomeDir(); err == nil {
		protectedHomePaths := []string{
			".morph",
			".ssh",
			".aws",
			".azure",
			".kube",
			".docker",
			".config/gcloud",
		}
		for _, relative := range protectedHomePaths {
			blocked = append(blocked, filepath.Join(home, relative))
		}
	}
	for _, path := range blocked {
		if canonical == path || strings.HasPrefix(canonical, path+string(filepath.Separator)) {
			return "", errors.New("docker mount source is protected")
		}
	}
	return canonical, nil
}
