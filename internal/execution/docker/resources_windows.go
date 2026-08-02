//go:build windows

package docker

import (
	"context"
	"errors"
	"os"

	"github.com/moby/moby/client"
	"golang.org/x/sys/windows"
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
	if b.reservedFreeBytes > 0 {
		root := os.Getenv("SystemDrive") + `\`
		path, conversionErr := windows.UTF16PtrFromString(root)
		if conversionErr != nil {
			return conversionErr
		}
		var available uint64
		if diskErr := windows.GetDiskFreeSpaceEx(path, &available, nil, nil); diskErr != nil {
			return errors.New("docker free-space reserve cannot be verified: " + diskErr.Error())
		}
		if available < uint64(b.reservedFreeBytes) {
			return errors.New("docker execution reserved free-space threshold reached")
		}
	}
	return nil
}
