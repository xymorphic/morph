//go:build !windows

package fileedit

import "os"

func activateReplacement(source string, destination string) error {
	return os.Rename(source, destination)
}
