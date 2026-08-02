//go:build !windows

package docker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/docker/go-units"
	"github.com/moby/moby/client"
)

func (b *Backend) checkVolumeAdmission(ctx context.Context, profile string) error {
	filters := make(client.Filters).Add("label", LabelProfile+"="+profile)
	volumes, err := b.client.Engine().VolumeList(ctx, client.VolumeListOptions{Filters: filters})
	if err != nil {
		return err
	}
	if b.maximumVolumes > 0 && len(volumes.Items) >= b.maximumVolumes {
		return errors.New("docker execution volume limit reached")
	}
	if b.reservedFreeBytes <= 0 {
		return nil
	}
	info, err := b.client.Engine().Info(ctx, client.InfoOptions{})
	if err != nil {
		return err
	}
	for _, entry := range info.Info.DriverStatus {
		if strings.EqualFold(strings.TrimSpace(entry[0]), "Data Space Available") {
			available, parseErr := units.RAMInBytes(entry[1])
			if parseErr != nil {
				return errors.New(
					"docker free-space reserve cannot be verified: " + parseErr.Error(),
				)
			}
			if available < b.reservedFreeBytes {
				return errors.New("docker execution reserved free-space threshold reached")
			}
			return nil
		}
	}
	root := info.Info.DockerRootDir
	if !filepath.IsAbs(root) {
		return errors.New("docker free-space reserve cannot be verified for the engine storage")
	}
	var status syscall.Statfs_t
	if err := syscall.Statfs(root, &status); err != nil {
		return errors.New("docker free-space reserve cannot be verified: " + err.Error())
	}
	available := int64(status.Bavail) * int64(status.Bsize)
	if available < b.reservedFreeBytes {
		return errors.New("docker execution reserved free-space threshold reached")
	}
	return nil
}
