package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xymorphic/morph/internal/fileedit"
)

const (
	ContractProvenanceRelease = "release-provided"
	ContractProvenanceCustom  = "user-managed"
)

type ContractStore struct {
	Root string
}

type ContractProvenance struct {
	Kind           string `json:"kind"`
	Image          string `json:"image"`
	OriginalDigest string `json:"original_digest,omitempty"`
	ActiveDigest   string `json:"active_digest"`
	OriginalPath   string `json:"original_path,omitempty"`
	ActivePath     string `json:"active_path"`
}

func NewContractStore(profileHome string) ContractStore {
	return ContractStore{Root: filepath.Join(profileHome, "sandbox", "contracts")}
}

func (s ContractStore) SaveRelease(image string, contract ImageContract) (string, error) {
	imageDigest, err := getImageDigestHex(image)
	if err != nil {
		return "", err
	}
	contract, data, err := encodeImageContract(contract)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Root, "releases", imageDigest+"-"+contract.Digest()+".json")
	if err := writeImmutableContract(path, data); err != nil {
		return "", err
	}
	return path, nil
}

func (s ContractStore) SaveCustom(contract ImageContract) (string, error) {
	contract, data, err := encodeImageContract(contract)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.Root, "custom", contract.Digest()+".json")
	if err := writeImmutableContract(path, data); err != nil {
		return "", err
	}
	return path, nil
}

func (s ContractStore) SaveActive(image string, contract ImageContract) (string, error) {
	path, err := s.ActivePath(image, contract)
	if err != nil {
		return "", err
	}
	_, data, err := encodeImageContract(contract)
	if err != nil {
		return "", err
	}
	if err := writeImmutableContract(path, data); err != nil {
		return "", fmt.Errorf("save active sandbox contract: %w", err)
	}
	return path, nil
}

func (s ContractStore) ActivePath(image string, contract ImageContract) (string, error) {
	imageDigest, err := getImageDigestHex(image)
	if err != nil {
		return "", err
	}
	contract, _, err = encodeImageContract(contract)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, "active", imageDigest+"-"+contract.Digest()+".json"), nil
}

func (s ContractStore) ResetActive(image string) (string, ImageContract, error) {
	_, contract, err := s.LoadRelease(image)
	if err != nil {
		return "", ImageContract{}, err
	}
	path, err := s.SaveActive(image, contract)
	return path, contract, err
}

func (s ContractStore) Describe(image string, activePath string) (ContractProvenance, error) {
	active, err := loadStoredImageContract(activePath, "")
	if err != nil {
		return ContractProvenance{}, err
	}
	provenance := ContractProvenance{
		Kind:         ContractProvenanceCustom,
		Image:        image,
		ActiveDigest: active.Digest(),
		ActivePath:   activePath,
	}

	releasePath, release, err := s.LoadRelease(image)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return provenance, nil
		}
		return ContractProvenance{}, err
	}
	provenance.OriginalDigest = release.Digest()
	provenance.OriginalPath = releasePath
	if provenance.ActiveDigest == provenance.OriginalDigest {
		provenance.Kind = ContractProvenanceRelease
	}
	return provenance, nil
}

func (s ContractStore) LoadRelease(image string) (string, ImageContract, error) {
	imageDigest, err := getImageDigestHex(image)
	if err != nil {
		return "", ImageContract{}, err
	}
	matches, err := filepath.Glob(filepath.Join(s.Root, "releases", imageDigest+"-*.json"))
	if err != nil {
		return "", ImageContract{}, err
	}
	if len(matches) == 0 {
		return "", ImageContract{}, os.ErrNotExist
	}
	if len(matches) > 1 {
		return "", ImageContract{}, fmt.Errorf("multiple preserved contracts exist for image %s", image)
	}
	name := strings.TrimSuffix(filepath.Base(matches[0]), ".json")
	expectedDigest := strings.TrimPrefix(name, imageDigest+"-")
	if expectedDigest == name || expectedDigest == "" {
		return "", ImageContract{}, fmt.Errorf("preserved sandbox contract filename is invalid: %s", matches[0])
	}
	contract, err := loadStoredImageContract(matches[0], expectedDigest)
	if err != nil {
		return "", ImageContract{}, err
	}
	return matches[0], contract, nil
}

func loadStoredImageContract(path string, expectedDigest string) (ImageContract, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return ImageContract{}, err
	}
	if !info.Mode().IsRegular() {
		return ImageContract{}, fmt.Errorf("sandbox contract %s is not a regular file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ImageContract{}, err
	}
	var contract ImageContract
	if err := json.Unmarshal(data, &contract); err != nil {
		return ImageContract{}, fmt.Errorf("parse sandbox contract: %w", err)
	}
	contract, err = contract.Normalize()
	if err != nil {
		return ImageContract{}, err
	}
	if expectedDigest != "" && contract.Digest() != expectedDigest {
		return ImageContract{}, fmt.Errorf(
			"preserved sandbox contract digest %s does not match filename digest %s",
			contract.Digest(),
			expectedDigest,
		)
	}
	return contract, nil
}

func encodeImageContract(contract ImageContract) (ImageContract, []byte, error) {
	contract, err := contract.Normalize()
	if err != nil {
		return ImageContract{}, nil, err
	}
	data, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return ImageContract{}, nil, err
	}
	return contract, append(data, '\n'), nil
}

func writeImmutableContract(path string, data []byte) error {
	snapshot, err := fileedit.ReadSnapshot(path, nil)
	if err != nil {
		return err
	}
	if snapshot.Exists {
		if !bytes.Equal(snapshot.Data, data) {
			return fmt.Errorf("sandbox contract digest collision at %s", path)
		}
		return nil
	}
	if _, err := fileedit.ReplaceIfUnchanged(snapshot, data); err != nil {
		return fmt.Errorf("write sandbox contract: %w", err)
	}
	return nil
}

func getImageDigestHex(image string) (string, error) {
	_, digest, ok := strings.Cut(strings.TrimSpace(image), "@sha256:")
	if !ok || len(digest) != 64 {
		return "", errors.New("sandbox image must be pinned by sha256 digest")
	}
	for _, char := range digest {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return "", errors.New("sandbox image digest is invalid")
		}
	}
	return digest, nil
}
