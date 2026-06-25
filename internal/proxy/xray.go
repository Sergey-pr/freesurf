package proxy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"freesurf/internal/paths"
)

// Xray-core provides the proxy protocols sing-box lacks (notably XHTTP/splithttp
// over h2/h3). It runs as a local SOCKS server that sing-box's TUN forwards to.
const (
	xrayCoreRepo = "XTLS/Xray-core"

	// RequiredXrayVersion is the pinned Xray-core release.
	RequiredXrayVersion = "26.3.27"

	socksPort = 10808 // local SOCKS port sing-box forwards to
)

// EnsureXray installs the pinned Xray binary if missing/out of date, returning its path.
func EnsureXray(ctx context.Context) (string, error) {
	path, err := paths.Xray()
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
		return "", fmt.Errorf("downloaded Xray did not report version %s", RequiredXrayVersion)
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
	return err == nil && strings.Contains(string(out), RequiredXrayVersion)
}

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
	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", xrayCoreRepo, RequiredXrayVersion, asset)

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
	want := paths.XrayName
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	return extractZipEntry(tmpPath, want, dest)
}

// RunXray starts the (unprivileged) Xray process writing to logPath, returning the
// running command so the caller can supervise and stop it.
func RunXray(binPath, cfgPath, logPath string) (*exec.Cmd, error) {
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, "run", "-c", cfgPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = hiddenProcAttr() // no console window on Windows
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	go func() { _ = cmd.Wait(); logFile.Close() }()
	return cmd, nil
}
