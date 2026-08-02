package execution

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

type identityKeyFile interface {
	Write([]byte) (int, error)
	Close() error
}

var (
	readIdentityKey     = os.ReadFile
	statIdentityKey     = os.Stat
	makeIdentityKeyDirs = os.MkdirAll
	readIdentityEntropy = rand.Read
	openIdentityKey     = func(path string, flag int, perm os.FileMode) (identityKeyFile, error) {
		return os.OpenFile(path, flag, perm)
	}
	removeIdentityKey = os.Remove
)

func LoadOrCreateIdentityKey(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("execution identity key path is required")
	}
	if value, err := readIdentityKey(path); err == nil {
		if len(value) < 32 {
			return nil, errors.New("execution identity key is invalid")
		}
		info, statErr := statIdentityKey(path)
		if statErr != nil {
			return nil, statErr
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("execution identity key permissions are too broad")
		}
		return value, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := makeIdentityKeyDirs(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	value := make([]byte, 32)
	if _, err := readIdentityEntropy(value); err != nil {
		return nil, err
	}
	file, err := openIdentityKey(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return LoadOrCreateIdentityKey(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(value); err != nil {
		_ = file.Close()
		_ = removeIdentityKey(path)
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = removeIdentityKey(path)
		return nil, err
	}
	return value, nil
}
