package docker

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/xymorphic/morph/internal/execution"
)

const (
	SandboxRepository           = "ghcr.io/xymorphic/morph-sandbox"
	SandboxRuntimeCompatibility = "1"
	SandboxContractPath         = "/usr/local/share/morph/contract.json"
)

type ImageVerificationMode string

const (
	ImageVerificationSignature ImageVerificationMode = "signature"
	ImageVerificationDigest    ImageVerificationMode = "digest"
)

var verifySandboxImageSignature = VerifyImageSignature

func LoadImageContract(path string) (execution.ImageContract, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return execution.ImageContract{}, errors.New("sandbox image contract path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return execution.ImageContract{}, err
	}
	var contract execution.ImageContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		return execution.ImageContract{}, err
	}
	return contract.Normalize()
}

func ValidateImageReference(reference string) error {
	parts := strings.Split(strings.TrimSpace(reference), "@sha256:")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 {
		return errors.New("sandbox image must be pinned by sha256 digest")
	}
	for _, char := range parts[1] {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return errors.New("sandbox image digest is invalid")
		}
	}
	return nil
}

func normalizeImageVerificationMode(value ImageVerificationMode) (ImageVerificationMode, error) {
	value = ImageVerificationMode(strings.TrimSpace(strings.ToLower(string(value))))
	if value == "" {
		return ImageVerificationSignature, nil
	}
	if value != ImageVerificationSignature && value != ImageVerificationDigest {
		return "", errors.New("sandbox image verification must be signature or digest")
	}
	return value, nil
}

func VerifyImageSignature(ctx context.Context, reference string) error {
	if err := ValidateImageReference(reference); err != nil {
		return err
	}
	path, err := checkCosignPrerequisite()
	if err != nil {
		return err
	}
	command := exec.CommandContext(
		ctx,
		path,
		"verify",
		"--certificate-identity-regexp",
		`^https://github\.com/xymorphic/morph/\.github/workflows/sandbox-image\.yml@refs/tags/v`,
		"--certificate-oidc-issuer",
		"https://token.actions.githubusercontent.com",
		reference,
	)
	if output, err := command.CombinedOutput(); err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New("sandbox image signature verification failed: " + message)
	}
	return nil
}

func CheckCosignPrerequisite() error {
	_, err := checkCosignPrerequisite()
	if err != nil {
		return errors.New("cosign is required to verify the sandbox image signature; install cosign and ensure it is on PATH")
	}
	return nil
}

func checkCosignPrerequisite() (string, error) {
	path, err := exec.LookPath("cosign")
	if err != nil {
		return "", errors.New("cosign is required to verify the sandbox image signature")
	}
	return path, nil
}

func PullImage(ctx context.Context, dockerClient *Client, reference string) error {
	if dockerClient == nil || dockerClient.Engine() == nil {
		return errors.New("docker client is required")
	}
	response, err := dockerClient.Engine().ImagePull(ctx, strings.TrimSpace(reference), client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pull sandbox image: %w", err)
	}
	defer func() { _ = response.Close() }()
	if err := response.Wait(ctx); err != nil {
		return fmt.Errorf("pull sandbox image: %w", err)
	}
	return nil
}

func ResolveImageReference(ctx context.Context, dockerClient *Client, reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return "", errors.New("sandbox image reference is required")
	}
	if err := ValidateImageReference(reference); err == nil {
		return reference, nil
	}
	if err := PullImage(ctx, dockerClient, reference); err != nil {
		return "", err
	}
	inspect, err := dockerClient.Engine().ImageInspect(ctx, reference)
	if err != nil {
		return "", fmt.Errorf("inspect sandbox image: %w", err)
	}
	repository := getImageRepository(reference)
	for _, digest := range inspect.RepoDigests {
		if getImageRepository(digest) == repository && ValidateImageReference(digest) == nil {
			return digest, nil
		}
	}
	return "", fmt.Errorf("sandbox image %q did not resolve to a repository digest", reference)
}

func InspectImageContract(ctx context.Context, dockerClient *Client, reference string) (execution.ImageContract, error) {
	if dockerClient == nil || dockerClient.Engine() == nil {
		return execution.ImageContract{}, errors.New("docker client is required")
	}
	inspect, err := dockerClient.Engine().ImageInspect(ctx, reference)
	if err != nil {
		return execution.ImageContract{}, fmt.Errorf("inspect sandbox image: %w", err)
	}
	if inspect.Config == nil {
		return execution.ImageContract{}, errors.New("sandbox image runtime configuration is unavailable")
	}
	helper := ""
	if len(inspect.Config.Entrypoint) > 0 {
		helper = inspect.Config.Entrypoint[0]
	}
	shell := "/bin/sh"
	if len(inspect.Config.Shell) > 0 {
		shell = inspect.Config.Shell[0]
	}
	pathEntries := []string{"/usr/local/bin", "/usr/bin", "/bin"}
	for _, env := range inspect.Config.Env {
		if value, ok := strings.CutPrefix(env, "PATH="); ok && strings.TrimSpace(value) != "" {
			pathEntries = strings.Split(value, ":")
			break
		}
	}
	executables := map[string]string{}
	if helper != "" {
		executables[filepath.Base(helper)] = helper
	}
	executables[filepath.Base(shell)] = shell
	return execution.ImageContract{
		Version:       SandboxRuntimeCompatibility,
		GOOS:          inspect.Os,
		Architecture:  inspect.Architecture,
		User:          inspect.Config.User,
		Shell:         shell,
		PATH:          pathEntries,
		Executables:   executables,
		Helper:        helper,
		WorkspacePath: "/workspace",
		HomePath:      "/home/morph",
		TemporaryPath: "/tmp",
		ControlPath:   "/run/morph",
	}.Normalize()
}

func VerifyImage(
	ctx context.Context,
	dockerClient *Client,
	reference string,
	mode ImageVerificationMode,
	contract execution.ImageContract,
) error {
	if dockerClient == nil || dockerClient.Engine() == nil {
		return errors.New("docker client is required")
	}
	if err := ValidateImageReference(reference); err != nil {
		return err
	}
	mode, err := normalizeImageVerificationMode(mode)
	if err != nil {
		return err
	}
	contract, err = contract.Normalize()
	if err != nil {
		return err
	}
	if mode == ImageVerificationSignature {
		if err := verifySandboxImageSignature(ctx, reference); err != nil {
			return err
		}
	}
	return CheckImageContract(ctx, dockerClient, reference, contract)
}

func CheckImageContract(
	ctx context.Context,
	dockerClient *Client,
	reference string,
	contract execution.ImageContract,
) error {
	if dockerClient == nil || dockerClient.Engine() == nil {
		return errors.New("docker client is required")
	}
	if err := ValidateImageReference(reference); err != nil {
		return err
	}
	contract, err := contract.Normalize()
	if err != nil {
		return err
	}
	if err := CheckImageContractCompatibility(contract); err != nil {
		return err
	}
	if err := checkImageMetadata(ctx, dockerClient, reference, contract); err != nil {
		return err
	}
	return checkImageContractPaths(ctx, dockerClient, reference, contract)
}

func CheckImageContractCompatibility(contract execution.ImageContract) error {
	if contract.Version != SandboxRuntimeCompatibility {
		return fmt.Errorf(
			"sandbox contract runtime compatibility %q is unsupported; expected %q",
			contract.Version,
			SandboxRuntimeCompatibility,
		)
	}
	return nil
}

func checkImageMetadata(
	ctx context.Context,
	dockerClient *Client,
	reference string,
	contract execution.ImageContract,
) error {
	inspect, err := dockerClient.Engine().ImageInspect(ctx, reference)
	if err != nil {
		return fmt.Errorf("inspect sandbox image: %w", err)
	}
	if inspect.Os != contract.GOOS || !contract.SupportsArchitecture(inspect.Architecture) {
		return errors.New("sandbox image platform does not match its contract")
	}
	if inspect.Config == nil || inspect.Config.User != contract.User ||
		!slices.Equal([]string(inspect.Config.Entrypoint), []string{contract.Helper}) {
		return errors.New("sandbox image runtime configuration does not match its contract")
	}
	return nil
}

func ExtractImageFile(ctx context.Context, dockerClient *Client, reference string, sourcePath string) ([]byte, error) {
	if dockerClient == nil || dockerClient.Engine() == nil {
		return nil, errors.New("docker client is required")
	}
	created, err := dockerClient.Engine().ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &containertypes.Config{
			Image:           reference,
			NetworkDisabled: true,
		},
		HostConfig: &containertypes.HostConfig{
			ReadonlyRootfs: true,
			NetworkMode:    "none",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create image inspection container: %w", err)
	}
	defer func() {
		_, _ = dockerClient.Engine().ContainerRemove(
			context.WithoutCancel(ctx),
			created.ID,
			client.ContainerRemoveOptions{Force: true},
		)
	}()

	archive, err := dockerClient.Engine().CopyFromContainer(ctx, created.ID, client.CopyFromContainerOptions{
		SourcePath: sourcePath,
	})
	if err != nil {
		return nil, fmt.Errorf("extract %s from sandbox image: %w", sourcePath, err)
	}
	defer func() { _ = archive.Content.Close() }()

	reader := tar.NewReader(archive.Content)
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, fmt.Errorf("read sandbox image archive: %w", nextErr)
		}
		if filepath.Base(header.Name) != filepath.Base(sourcePath) || !header.FileInfo().Mode().IsRegular() {
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, 1<<20))
		if readErr != nil {
			return nil, fmt.Errorf("read %s from sandbox image: %w", sourcePath, readErr)
		}
		return data, nil
	}
	return nil, fmt.Errorf("sandbox image does not contain %s", sourcePath)
}

func checkImageContractPaths(
	ctx context.Context,
	dockerClient *Client,
	reference string,
	contract execution.ImageContract,
) error {
	created, err := dockerClient.Engine().ContainerCreate(ctx, client.ContainerCreateOptions{
		Config: &containertypes.Config{
			Image:           reference,
			NetworkDisabled: true,
		},
		HostConfig: &containertypes.HostConfig{
			ReadonlyRootfs: true,
			NetworkMode:    "none",
		},
	})
	if err != nil {
		return fmt.Errorf("create contract validation container: %w", err)
	}
	defer func() {
		_, _ = dockerClient.Engine().ContainerRemove(
			context.WithoutCancel(ctx),
			created.ID,
			client.ContainerRemoveOptions{Force: true},
		)
	}()

	executables := []string{contract.Shell, contract.Helper}
	for _, path := range contract.Executables {
		executables = append(executables, path)
	}
	slices.Sort(executables)
	executables = slices.Compact(executables)
	for _, path := range executables {
		stat, statErr := dockerClient.Engine().ContainerStatPath(ctx, created.ID, client.ContainerStatPathOptions{Path: path})
		if statErr != nil {
			return fmt.Errorf("sandbox contract executable %s is unavailable: %w", path, statErr)
		}
		if stat.Stat.Mode.IsDir() || stat.Stat.Mode.Perm()&0o111 == 0 {
			return fmt.Errorf("sandbox contract executable %s is not executable", path)
		}
	}

	directories := append([]string{}, contract.PATH...)
	directories = append(directories, contract.WorkspacePath, contract.HomePath, contract.TemporaryPath, contract.ControlPath)
	slices.Sort(directories)
	directories = slices.Compact(directories)
	for _, path := range directories {
		stat, statErr := dockerClient.Engine().ContainerStatPath(ctx, created.ID, client.ContainerStatPathOptions{Path: path})
		if statErr != nil {
			return fmt.Errorf("sandbox contract directory %s is unavailable: %w", path, statErr)
		}
		if !stat.Stat.Mode.IsDir() {
			return fmt.Errorf("sandbox contract directory %s is not a directory", path)
		}
	}
	return nil
}

func getImageRepository(reference string) string {
	reference = strings.TrimSpace(reference)
	if index := strings.Index(reference, "@"); index >= 0 {
		return reference[:index]
	}
	lastSlash := strings.LastIndex(reference, "/")
	if lastColon := strings.LastIndex(reference, ":"); lastColon > lastSlash {
		return reference[:lastColon]
	}
	return reference
}

func (b *Backend) verifyImage(ctx context.Context) error {
	if b.allowTestImage {
		return nil
	}
	b.imageVerificationMu.Lock()
	defer b.imageVerificationMu.Unlock()
	if b.imageVerified {
		return nil
	}
	if b.imageVerification != ImageVerificationDigest {
		if err := verifySandboxImageSignature(ctx, b.image); err != nil {
			return err
		}
	}
	if err := checkImageMetadata(ctx, b.client, b.image, b.contract); err != nil {
		return err
	}
	b.imageVerified = true
	return nil
}
