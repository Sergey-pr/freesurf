package proxy

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"freesurf/internal/paths"
)

// We pin the sing-box-lx fork (same as singbox-launcher): it builds every
// platform and its `version` output carries the full fork tag, so the check is
// exact. The Xray side handles the actual proxy protocols.
const (
	singboxCoreRepo = "Leadaxe/sing-box-lx"

	// RequiredCoreVersion is the pinned sing-box-lx release.
	RequiredCoreVersion = "1.13.13-lx.15"
)

// EnsureCore installs the pinned sing-box binary if missing/out of date, returning
// its path.
func EnsureCore(ctx context.Context) (string, error) {
	path, err := paths.Singbox()
	if err != nil {
		return "", err
	}
	// Windows needs the Wintun driver alongside sing-box; no-op elsewhere.
	if err := EnsureWintun(ctx); err != nil {
		return "", err
	}
	if coreVersionOK(path) {
		return path, nil
	}
	if err := downloadCore(ctx, path); err != nil {
		return "", err
	}
	if !coreVersionOK(path) {
		return "", fmt.Errorf("downloaded core did not report version %s", RequiredCoreVersion)
	}
	return path, nil
}

func coreVersionOK(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	return err == nil && strings.Contains(string(out), RequiredCoreVersion)
}

func coreAssetSuffix() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin-" + runtime.GOARCH + ".tar.gz"
	case "linux":
		switch runtime.GOARCH {
		case "amd64", "arm64":
			return "linux-" + runtime.GOARCH + ".tar.gz"
		case "arm":
			return "linux-armv7.tar.gz"
		}
	case "windows":
		switch runtime.GOARCH {
		case "amd64", "arm64":
			return "windows-" + runtime.GOARCH + ".zip"
		case "386":
			return "windows-386-legacy-windows-7.zip"
		}
	}
	return ""
}

func downloadCore(ctx context.Context, dest string) error {
	suffix := coreAssetSuffix()
	if suffix == "" {
		return fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/v%s", singboxCoreRepo, RequiredCoreVersion)
	rel, err := fetchRelease(ctx, apiURL)
	if err != nil {
		return err
	}
	var dlURL string
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, suffix) {
			dlURL = a.URL
			break
		}
	}
	if dlURL == "" {
		return fmt.Errorf("no asset matching %q in release v%s", suffix, RequiredCoreVersion)
	}

	tmp, err := os.CreateTemp("", "singbox-dl-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := httpDownload(ctx, dlURL, tmpPath); err != nil {
		return err
	}
	if strings.HasSuffix(suffix, ".zip") {
		want := paths.SingboxName
		if runtime.GOOS == "windows" {
			want += ".exe"
		}
		return extractZipEntry(tmpPath, want, dest)
	}
	return extractTarGz(tmpPath, paths.SingboxName, dest)
}

func extractTarGz(archivePath, wantBase, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == wantBase {
			return writeExecutable(dest, tr)
		}
	}
	return fmt.Errorf("%q not found in archive", wantBase)
}

// CheckConfig validates a sing-box config with `sing-box check`.
func CheckConfig(binPath, cfgPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binPath, "check", "-c", cfgPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
