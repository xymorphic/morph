package main

import (
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePatchPaths_RejectsUnpreparedTarget(t *testing.T) {
	patch := []byte("--- a/allowed.txt\n+++ b/other.txt\n@@ -1 +1 @@\n-old\n+new\n")
	_, err := validatePatchPaths(patch, 1, []string{"/workspace/allowed.txt"})
	require.EqualError(t, err, "fs-patch path was not authorized")
}

func TestProcessStartTicks_IdentifiesCurrentProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("process start identity is provided by Linux procfs")
	}
	ticks, err := processStartTicks(os.Getpid())
	require.NoError(t, err)
	require.NotEmpty(t, ticks)
}
