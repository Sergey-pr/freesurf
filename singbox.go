package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// sing-box core download source. We pin the sing-box-lx fork (same as
// singbox-launcher) — it builds every platform and its `sing-box version`
// output contains the full fork tag, so the version check below is exact.
const (
	singboxCoreRepo     = "Leadaxe/sing-box-lx"
	requiredCoreVersion = "1.13.13-lx.15"
	singboxExecName     = "sing-box"
)

// appDataDir returns the FreeSurf data directory (same one db.go uses), creating it.
func appDataDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "FreeSurf")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func binDir() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		return "", err
	}
	return bin, nil
}

func singboxPath() (string, error) {
	bin, err := binDir()
	if err != nil {
		return "", err
	}
	name := singboxExecName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(bin, name), nil
}

func configPath() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

func coreLogPath() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sing-box.log"), nil
}

// sentinelPath is a small file the privileged core watches: while it exists (and
// the launcher is alive) the core keeps running. Removing it — or quitting the
// app — makes the core self-terminate, so stopping never needs a fresh
// privilege prompt.
func sentinelPath() (string, error) {
	dir, err := appDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tunnel.run"), nil
}

// ensureCore makes sure a sing-box binary of the pinned version is installed,
// downloading it from the fork's GitHub release if missing or out of date.
func ensureCore(ctx context.Context) (string, error) {
	path, err := singboxPath()
	if err != nil {
		return "", err
	}
	if coreVersionOK(path) {
		return path, nil
	}
	if err := downloadCore(ctx, path); err != nil {
		return "", err
	}
	if !coreVersionOK(path) {
		return "", fmt.Errorf("downloaded core did not report version %s", requiredCoreVersion)
	}
	return path, nil
}

// coreVersionOK reports whether the binary exists and `sing-box version` mentions
// the pinned version.
func coreVersionOK(path string) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), requiredCoreVersion)
}

// assetSuffix returns the release asset filename suffix for the current platform.
func assetSuffix() string {
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

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// downloadCore fetches the pinned release asset for this platform and installs the
// sing-box binary at dest.
func downloadCore(ctx context.Context, dest string) error {
	suffix := assetSuffix()
	if suffix == "" {
		return fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/v%s", singboxCoreRepo, requiredCoreVersion)
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
		return fmt.Errorf("no asset matching %q in release v%s", suffix, requiredCoreVersion)
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
		return extractZip(tmpPath, dest)
	}
	return extractTarGz(tmpPath, dest)
}

func fetchRelease(ctx context.Context, url string) (*ghRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "free-surf/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github release API returned HTTP %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func httpDownload(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "free-surf/1.0")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	const maxSize = 100 * 1024 * 1024
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxSize)); err != nil {
		return err
	}
	return nil
}

// extractTarGz finds the "sing-box" entry in the tar.gz archive and writes it to dest.
func extractTarGz(archivePath, dest string) error {
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
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != singboxExecName {
			continue
		}
		return writeExecutable(dest, tr)
	}
	return fmt.Errorf("%q not found in archive", singboxExecName)
}

// extractZip finds the "sing-box(.exe)" entry in the zip archive and writes it to dest.
func extractZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()

	want := singboxExecName
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	for _, zf := range zr.File {
		if filepath.Base(zf.Name) != want {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeExecutable(dest, rc)
	}
	return fmt.Errorf("%q not found in archive", want)
}

func writeExecutable(dest string, r io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	const maxSize = 200 * 1024 * 1024
	if _, err := io.Copy(out, io.LimitReader(r, maxSize)); err != nil {
		return err
	}
	return nil
}

// checkConfig runs `sing-box check -c <path>` and returns its combined output on failure.
func checkConfig(binPath, cfgPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binPath, "check", "-c", cfgPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
