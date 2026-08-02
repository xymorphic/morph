package docker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/xymorphic/morph/internal/execution"
)

const (
	SandboxRepository           = "ghcr.io/xymorphic/morph-sandbox"
	SandboxRuntimeCompatibility = "1"
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

func VerifyImageSignature(ctx context.Context, reference string) error {
	if err := ValidateImageReference(reference); err != nil {
		return err
	}
	path, err := exec.LookPath("cosign")
	if err != nil {
		return errors.New("cosign is required to verify the sandbox image signature")
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

func (b *Backend) verifyImage(ctx context.Context) error {
	if b.allowTestImage {
		return nil
	}
	b.imageVerificationMu.Lock()
	defer b.imageVerificationMu.Unlock()
	if b.imageVerified {
		return nil
	}
	if err := verifySandboxImageSignature(ctx, b.image); err != nil {
		return err
	}
	inspect, err := b.client.Engine().ImageInspect(ctx, b.image)
	if err != nil {
		return err
	}
	if inspect.Os != b.contract.GOOS || !b.contract.SupportsArchitecture(inspect.Architecture) {
		return errors.New("sandbox image platform does not match its contract")
	}
	if inspect.Config == nil || inspect.Config.User != b.contract.User ||
		!slices.Equal([]string(inspect.Config.Entrypoint), []string{b.contract.Helper}) {
		return errors.New("sandbox image runtime configuration does not match its contract")
	}
	b.imageVerified = true
	return nil
}
