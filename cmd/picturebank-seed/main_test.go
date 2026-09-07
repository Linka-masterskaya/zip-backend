package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadSeedFileIsBounded(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "image.bin")
	require.NoError(t, os.WriteFile(filePath, []byte{1, 2, 3, 4}, 0o600))

	data, err := readSeedFile(filePath, 4)
	require.NoError(t, err)
	assert.Equal(t, []byte{1, 2, 3, 4}, data)

	_, err = readSeedFile(filePath, 3)
	require.ErrorContains(t, err, "exceeds maximum size")
}

func TestRunRejectsUnknownCommandWithoutInfrastructure(t *testing.T) {
	err := run(t.Context(), []string{"unknown"}, bytes.NewBuffer(nil))

	require.ErrorContains(t, err, "unknown command")
}
