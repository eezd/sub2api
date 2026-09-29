package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunBuildsDeterministicSignedPackage(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	t.Setenv("SUB2API_PLUGIN_SIGNING_KEY", base64.StdEncoding.EncodeToString(seed))
	t.Setenv("SUB2API_PLUGIN_PUBLISHER_KEY_ID", "")
	input := createPackageInput(t)
	first := filepath.Join(t.TempDir(), "first.s2plugin")
	second := filepath.Join(t.TempDir(), "second.s2plugin")

	require.NoError(t, run(input, first, "0.1.179-custom.4", "0.1.179-custom.4"))
	require.NoError(t, run(input, second, "0.1.179-custom.4", "0.1.179-custom.4"))
	firstRaw, err := os.ReadFile(first)
	require.NoError(t, err)
	secondRaw, err := os.ReadFile(second)
	require.NoError(t, err)
	require.Equal(t, firstRaw, secondRaw)

	archive, err := zip.OpenReader(first)
	require.NoError(t, err)
	defer func() { _ = archive.Close() }()
	entries := make(map[string][]byte, len(archive.File))
	for _, file := range archive.File {
		reader, err := file.Open()
		require.NoError(t, err)
		entries[file.Name], err = io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
	}
	var signed signature
	require.NoError(t, json.Unmarshal(entries["signature.json"], &signed))
	require.Equal(t, defaultPublisherKeyID, signed.KeyID)
	signatureBytes, err := base64.StdEncoding.DecodeString(signed.Signature)
	require.NoError(t, err)
	require.True(t, ed25519.Verify(privateKey.Public().(ed25519.PublicKey), entries["manifest.json"], signatureBytes))

	var packaged manifest
	require.NoError(t, json.Unmarshal(entries["manifest.json"], &packaged))
	require.Equal(t, "0.1.179-custom.4", packaged.Version)
	require.Equal(t, ">=0.1.179 <0.2.0", packaged.Requires.Sub2API)
	require.Equal(t, []string{"0.1.179-custom.4"}, packaged.Requires.TestedSub2APIVersions)
	require.Len(t, packaged.Runtimes, 5)
	for path, expectedHash := range packaged.Files {
		require.Equal(t, expectedHash, hashBytes(entries[path]), path)
	}
}

func TestHostConstraintRejectsInvalidVersion(t *testing.T) {
	_, err := hostConstraint("development")
	require.Error(t, err)
}

func createPackageInput(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, target := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"} {
		name := "openai-transport-plugin"
		if target == "windows-amd64" {
			name += ".exe"
		}
		writeTestFile(t, filepath.Join(root, "runtimes", target, name), []byte("runtime-"+target), 0o755)
	}
	writeTestFile(t, filepath.Join(root, "ui", "index.html"), []byte("<!doctype html>"), 0o644)
	return root
}

func writeTestFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, content, mode))
}

func hashBytes(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
