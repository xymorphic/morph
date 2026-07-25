//go:build !windows

package credential

import (
	"errors"
	"os"
	"path/filepath"
)

func protectCredentialDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func protectCredentialFile(path string) error {
	return os.Chmod(path, 0o600)
}

func checkCredentialPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("credential store permissions are too broad")
	}
	directory, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	if directory.Mode().Perm()&0o077 != 0 {
		return errors.New("credential store directory permissions are too broad")
	}

	return nil
}

func syncCredentialDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

func replaceCredentialFile(source, destination string) error {
	return os.Rename(source, destination)
}
