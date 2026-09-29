package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/openaitransportplugin"
	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
)

const defaultPublisherKeyID = "sub2api-openai-transport-v1"

var semanticVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type manifest struct {
	SchemaVersion int                `json:"schema_version"`
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Version       string             `json:"version"`
	Description   string             `json:"description"`
	Author        string             `json:"author"`
	Requires      requirements       `json:"requires"`
	Capabilities  []capability       `json:"capabilities"`
	Runtimes      map[string]runtime `json:"runtimes"`
	UI            uiManifest         `json:"ui"`
	Files         map[string]string  `json:"files"`
}

type requirements struct {
	Sub2API                   string   `json:"sub2api"`
	RecommendedSub2APIVersion string   `json:"recommended_sub2api_version"`
	TestedSub2APIVersions     []string `json:"tested_sub2api_versions"`
	PluginProtocol            int      `json:"plugin_protocol"`
	TransportAPI              int      `json:"transport_api"`
	UIBridge                  int      `json:"ui_bridge"`
}

type capability struct {
	ID          string `json:"id"`
	Platform    string `json:"platform"`
	AccountType string `json:"account_type"`
}

type runtime struct {
	Path string `json:"path"`
}

type uiManifest struct {
	Entrypoint string `json:"entrypoint"`
}

type signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

type packageFile struct {
	logicalPath string
	sourcePath  string
	hash        string
	executable  bool
}

func main() {
	inputDir := flag.String("input", "", "directory containing runtimes/ and ui/")
	outputPath := flag.String("output", "", "output .s2plugin path")
	version := flag.String("version", "", "plugin semantic version")
	hostVersion := flag.String("host-version", "", "tested Sub2API semantic version")
	flag.Parse()
	if err := run(*inputDir, *outputPath, *version, *hostVersion); err != nil {
		fmt.Fprintln(os.Stderr, "package plugin:", err)
		os.Exit(1)
	}
}

func run(inputDir, outputPath, version, hostVersion string) error {
	inputDir = filepath.Clean(strings.TrimSpace(inputDir))
	outputPath = filepath.Clean(strings.TrimSpace(outputPath))
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	hostVersion = strings.TrimPrefix(strings.TrimSpace(hostVersion), "v")
	if inputDir == "." || outputPath == "." || version == "" || hostVersion == "" {
		return errors.New("--input, --output, --version and --host-version are required")
	}
	if !semanticVersion.MatchString(version) {
		return fmt.Errorf("invalid plugin semantic version %q", version)
	}
	constraint, err := hostConstraint(hostVersion)
	if err != nil {
		return err
	}
	files, err := collectFiles(inputDir)
	if err != nil {
		return err
	}
	runtimes, err := packageRuntimes(files)
	if err != nil {
		return err
	}
	manifestFiles := make(map[string]string, len(files))
	for _, file := range files {
		manifestFiles[file.logicalPath] = file.hash
	}
	value := manifest{
		SchemaVersion: 1,
		ID:            openaitransportplugin.PluginID,
		Name:          "OpenAI Transport",
		Version:       version,
		Description:   "OpenAI OAuth 出站 HTTP/TLS 传输插件，支持 Node.js 24.x ClientHello 与账号代理。",
		Author:        "Sub2API",
		Requires: requirements{
			Sub2API: constraint, RecommendedSub2APIVersion: hostVersion,
			TestedSub2APIVersions: []string{hostVersion}, PluginProtocol: pluginv1.ProtocolVersion,
			TransportAPI: pluginv1.TransportAPIVersion, UIBridge: pluginv1.UIBridgeVersion,
		},
		Capabilities: []capability{{ID: openaitransportplugin.Capability, Platform: "openai", AccountType: "oauth"}},
		Runtimes:     runtimes,
		UI:           uiManifest{Entrypoint: "ui/index.html"},
		Files:        manifestFiles,
	}
	manifestRaw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	manifestRaw = append(manifestRaw, '\n')
	privateKey, err := signingKey(os.Getenv("SUB2API_PLUGIN_SIGNING_KEY"))
	if err != nil {
		return err
	}
	publisherKeyID := strings.TrimSpace(os.Getenv("SUB2API_PLUGIN_PUBLISHER_KEY_ID"))
	if publisherKeyID == "" {
		publisherKeyID = defaultPublisherKeyID
	}
	signatureRaw, err := json.MarshalIndent(signature{
		Algorithm: "ed25519", KeyID: publisherKeyID,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestRaw)),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal signature: %w", err)
	}
	signatureRaw = append(signatureRaw, '\n')
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	return writeArchive(outputPath, manifestRaw, signatureRaw, files)
}

func hostConstraint(version string) (string, error) {
	matches := semanticVersion.FindStringSubmatch(version)
	if matches == nil {
		return "", fmt.Errorf("invalid host semantic version %q", version)
	}
	major := atoi(matches[1])
	minor := atoi(matches[2])
	patch := atoi(matches[3])
	lower := fmt.Sprintf("%d.%d.%d", major, minor, patch)
	upper := fmt.Sprintf("%d.0.0", major+1)
	if major == 0 {
		upper = fmt.Sprintf("0.%d.0", minor+1)
	}
	return fmt.Sprintf(">=%s <%s", lower, upper), nil
}

func atoi(value string) int {
	result := 0
	for _, digit := range value {
		result = result*10 + int(digit-'0')
	}
	return result
}

func collectFiles(root string) ([]packageFile, error) {
	var files []packageFile
	for _, subtree := range []string{"runtimes", "ui"} {
		base := filepath.Join(root, subtree)
		if err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("package input is not a regular file: %s", path)
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			logical := filepath.ToSlash(relative)
			hash, err := fileSHA256(path)
			if err != nil {
				return err
			}
			files = append(files, packageFile{
				logicalPath: logical, sourcePath: path, hash: hash,
				executable: strings.HasPrefix(logical, "runtimes/"),
			})
			return nil
		}); err != nil {
			return nil, fmt.Errorf("collect %s: %w", subtree, err)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].logicalPath < files[j].logicalPath })
	if len(files) == 0 {
		return nil, errors.New("package input is empty")
	}
	return files, nil
}

func packageRuntimes(files []packageFile) (map[string]runtime, error) {
	runtimes := make(map[string]runtime)
	for _, file := range files {
		parts := strings.Split(file.logicalPath, "/")
		if len(parts) == 3 && parts[0] == "runtimes" {
			if _, exists := runtimes[parts[1]]; exists {
				return nil, fmt.Errorf("multiple runtime files for %s", parts[1])
			}
			runtimes[parts[1]] = runtime{Path: file.logicalPath}
		}
	}
	for _, target := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64"} {
		if _, exists := runtimes[target]; !exists {
			return nil, fmt.Errorf("missing required runtime %s", target)
		}
	}
	return runtimes, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func signingKey(value string) (ed25519.PrivateKey, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("SUB2API_PLUGIN_SIGNING_KEY is required")
	}
	if block, _ := pem.Decode([]byte(value)); block != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("SUB2API_PLUGIN_SIGNING_KEY PEM is not a valid PKCS#8 key")
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("SUB2API_PLUGIN_SIGNING_KEY PEM is not Ed25519")
		}
		return key, nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("SUB2API_PLUGIN_SIGNING_KEY must be PKCS#8 PEM or Base64")
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	default:
		return nil, fmt.Errorf("SUB2API_PLUGIN_SIGNING_KEY has invalid decoded length %d", len(raw))
	}
}

func writeArchive(outputPath string, manifestRaw, signatureRaw []byte, files []packageFile) (returnErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".openai-transport-*.s2plugin")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if returnErr != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	archive := zip.NewWriter(temporary)
	if err := addBytes(archive, "manifest.json", manifestRaw, false); err != nil {
		return err
	}
	if err := addBytes(archive, "signature.json", signatureRaw, false); err != nil {
		return err
	}
	for _, file := range files {
		if err := addFile(archive, file); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return err
	}
	return nil
}

func zipHeader(name string, executable bool) *zip.FileHeader {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	if executable {
		header.SetMode(0o755)
	} else {
		header.SetMode(0o644)
	}
	return header
}

func addBytes(archive *zip.Writer, name string, content []byte, executable bool) error {
	writer, err := archive.CreateHeader(zipHeader(name, executable))
	if err != nil {
		return err
	}
	_, err = writer.Write(content)
	return err
}

func addFile(archive *zip.Writer, file packageFile) error {
	input, err := os.Open(file.sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	writer, err := archive.CreateHeader(zipHeader(file.logicalPath, file.executable))
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, input)
	return err
}
