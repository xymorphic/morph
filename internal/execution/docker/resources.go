package docker

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/docker/go-units"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
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
			return b.checkReservedFreeBytes(available)
		}
	}
	available, err := b.fetchEngineFreeBytes(ctx, profile)
	if err != nil {
		return errors.New("docker free-space reserve cannot be verified: " + err.Error())
	}
	return b.checkReservedFreeBytes(available)
}

func (b *Backend) checkReservedFreeBytes(available int64) error {
	if available < b.reservedFreeBytes {
		return errors.New("docker execution reserved free-space threshold reached")
	}
	return nil
}

func (b *Backend) fetchEngineFreeBytes(
	ctx context.Context,
	profile string,
) (available int64, resultErr error) {
	engine := b.client.Engine()
	created, err := engine.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &container.Config{
			User:         b.contract.User,
			AttachStdout: true,
			AttachStderr: true,
			Image:        b.image,
			Cmd:          []string{"free-space", b.contract.WorkspacePath},
			WorkingDir:   b.contract.WorkspacePath,
			Labels: map[string]string{
				LabelDaemonIncarnation: b.daemonIncarnation,
				LabelProfile:           profile,
				LabelResourceKind:      "storage-probe",
			},
		},
		HostConfig: &container.HostConfig{
			LogConfig:       container.LogConfig{Type: "none"},
			NetworkMode:     container.NetworkMode("none"),
			RestartPolicy:   container.RestartPolicy{Name: container.RestartPolicyDisabled},
			CapDrop:         []string{"ALL"},
			ReadonlyRootfs:  true,
			SecurityOpt:     []string{"no-new-privileges"},
			Privileged:      false,
			PublishAllPorts: false,
		},
	})
	if err != nil {
		return 0, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, b.removeContainer(created.ID, 0))
	}()
	attached, err := engine.ContainerAttach(ctx, created.ID, client.ContainerAttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return 0, err
	}
	defer attached.Close()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
		copyDone <- copyErr
	}()
	if _, err := engine.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return 0, err
	}
	wait := engine.ContainerWait(
		context.WithoutCancel(ctx),
		created.ID,
		client.ContainerWaitOptions{Condition: container.WaitConditionNotRunning},
	)
	var statusCode int64
	var responseErr error
	select {
	case response := <-wait.Result:
		if response.Error != nil {
			responseErr = errors.New(response.Error.Message)
		}
		statusCode = response.StatusCode
	case err := <-wait.Error:
		return 0, err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	attached.Close()
	select {
	case err := <-copyDone:
		if err != nil {
			return 0, err
		}
	case <-time.After(containerOutputDrainTimeout):
		return 0, errors.New("sandbox free-space probe output did not close")
	}
	if responseErr != nil {
		return 0, responseErr
	}
	if statusCode != 0 {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return 0, errors.New(message)
		}
		return 0, errors.New(
			"sandbox free-space probe exited with status " + strconv.FormatInt(statusCode, 10),
		)
	}
	available, err = strconv.ParseInt(strings.TrimSpace(stdout.String()), 10, 64)
	if err != nil || available < 0 {
		return 0, errors.New("sandbox free-space probe returned an invalid value")
	}
	return available, nil
}
