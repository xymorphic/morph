package browser

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/xymorphic/morph/pkg/nanoid"
)

const uploadStagingPrefix = "upload_"

type stagedUpload struct {
	Path   string
	Size   int64
	Digest [sha256.Size]byte
}

func stageUpload(ctx context.Context, sourcePath, stagingRoot string, maxBytes int64) (stagedUpload, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(stagingRoot) {
		return stagedUpload{}, errors.New("browser upload paths must be absolute")
	}
	if maxBytes <= 0 {
		return stagedUpload{}, errors.New("browser upload size limit must be greater than zero")
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return stagedUpload{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return stagedUpload{}, errors.New("browser upload source must not be a symbolic link or junction")
	}
	sourcePath, err = filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return stagedUpload{}, err
	}
	if err := prepareUploadStagingRoot(stagingRoot); err != nil {
		return stagedUpload{}, err
	}

	source, size, err := openSecureUpload(sourcePath, maxBytes)
	if err != nil {
		return stagedUpload{}, err
	}
	defer func() { _ = source.Close() }()
	name := nanoid.MustGenerate(uploadStagingPrefix) + getSafeUploadExtension(sourcePath)
	destinationPath := filepath.Join(stagingRoot, name)
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return stagedUpload{}, err
	}
	remove := true
	defer func() {
		_ = destination.Close()
		if remove {
			_ = os.Remove(destinationPath)
		}
	}()

	hash := sha256.New()
	written, err := copyUpload(ctx, io.MultiWriter(destination, hash), source, maxBytes)
	if err != nil {
		return stagedUpload{}, err
	}
	if written != size {
		return stagedUpload{}, errors.New("browser upload source changed while staging")
	}
	if err := destination.Sync(); err != nil {
		return stagedUpload{}, err
	}
	if err := destination.Close(); err != nil {
		return stagedUpload{}, err
	}
	remove = false
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))

	return stagedUpload{Path: destinationPath, Size: written, Digest: digest}, nil
}

func prepareUploadStagingRoot(stagingRoot string) error {
	parent := filepath.Dir(stagingRoot)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("browser upload staging parent must be a regular directory")
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if !isSamePath(resolvedParent, parent) {
		return errors.New("browser upload staging path must not traverse a symbolic link or junction")
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(stagingRoot, 0o700); err != nil {
		return err
	}
	return nil
}

func copyUpload(ctx context.Context, destination io.Writer, source io.Reader, maxBytes int64) (int64, error) {
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			total += int64(read)
			if total > maxBytes {
				return total, errors.New("browser upload exceeds the size limit")
			}
			if _, err := destination.Write(buffer[:read]); err != nil {
				return total, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
		if read == 0 {
			return total, errors.New("browser upload source made no progress")
		}
	}
}

func getSafeUploadExtension(path string) string {
	extension := filepath.Ext(filepath.Base(path))
	if len(extension) > 16 {
		return ""
	}
	for _, value := range strings.TrimPrefix(extension, ".") {
		isDigit := value >= '0' && value <= '9'
		isUpper := value >= 'A' && value <= 'Z'
		isLower := value >= 'a' && value <= 'z'
		if !isDigit && !isUpper && !isLower {
			return ""
		}
	}
	return extension
}
