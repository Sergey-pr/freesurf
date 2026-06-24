package main

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Xray-core provides the proxy protocols sing-box lacks (notably XHTTP/splithttp
// over h2/h3). We run it as a local SOCKS server and let sing-box's TUN forward
// to it. Pinned, downloaded on demand — same model as the sing-box core.
const (
	xrayCoreRepo        = "XTLS/Xray-core"
	requiredXrayVersion = "26.3.27"
	xrayExecName        = "xray"
	xraySocksPort       = 10808 // local SOCKS that sing-box forwards to
)

func xrayPath() (string, error) {
	bin, err := binDir()
	if err != nil {
		return "", err
	}
	name := xrayExecName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(bin, name), nil
}

func xrayConfigPath() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "xray.json"), nil
}

func xrayLogPath() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "xray.log"), nil
}

// ensureXray makes sure a pinned Xray binary is installed, downloading it if needed.
func ensureXray(ctx context.Context) (string, error) {
	path, err := xrayPath()
	if err != nil {
		return "", err
	}
	if xrayVersionOK(path) {
		return path, nil
	}
	if err := downloadXray(ctx, path); err != nil {
		return "", err
	}
	if !xrayVersionOK(path) {
		return "", fmt.Errorf("downloaded Xray did not report version %s", requiredXrayVersion)
	}
	return path, nil
}

func xrayVersionOK(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), requiredXrayVersion)
}

// xrayAssetName returns the release asset filename for the current platform.
func xrayAssetName() string {
	osPart := map[string]string{"darwin": "macos", "linux": "linux", "windows": "windows"}[runtime.GOOS]
	if osPart == "" {
		return ""
	}
	switch runtime.GOARCH {
	case "amd64":
		return fmt.Sprintf("Xray-%s-64.zip", osPart)
	case "arm64":
		return fmt.Sprintf("Xray-%s-arm64-v8a.zip", osPart)
	case "386":
		return fmt.Sprintf("Xray-%s-32.zip", osPart)
	}
	return ""
}

func downloadXray(ctx context.Context, dest string) error {
	asset := xrayAssetName()
	if asset == "" {
		return fmt.Errorf("unsupported platform for Xray: %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", xrayCoreRepo, requiredXrayVersion, asset)

	tmp, err := os.CreateTemp("", "xray-dl-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := httpDownload(ctx, url, tmpPath); err != nil {
		return err
	}

	want := xrayExecName
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	return extractZipEntry(tmpPath, want, dest)
}

// extractZipEntry writes the archive entry whose base name is wantBase to dest (0755).
func extractZipEntry(archivePath, wantBase, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if filepath.Base(zf.Name) != wantBase {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeExecutable(dest, rc)
	}
	return fmt.Errorf("%q not found in archive", wantBase)
}

// writeXrayExecutable is intentionally omitted: writeExecutable (singbox.go) is reused.

// runXray starts the Xray process (unprivileged) writing to its log, returning the
// running command so the engine can supervise and stop it.
func runXray(binPath, cfgPath, logPath string) (*exec.Cmd, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, "run", "-c", cfgPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	// Close our handle once the process exits (best-effort).
	go func() { _ = cmd.Wait(); logFile.Close() }()
	return cmd, nil
}
