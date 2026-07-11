// Command fetchcores downloads the pinned sing-box and Xray release binaries
// into internal/proxy/cores/<os>-<arch>/ so they get embedded into the app at
// build time (see internal/proxy/cores.go). It runs as a build step (Taskfile
// build tasks / `go generate ./internal/proxy`), never at app runtime.
//
// A VERSIONS file in the target directory records what was fetched; when it
// matches the pinned versions the tool exits immediately, so re-running is cheap.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"freesurf/internal/paths"
	"freesurf/internal/proxy"
)

const (
	singboxCoreRepo = "Leadaxe/sing-box-lx"
	xrayCoreRepo    = "XTLS/Xray-core"
)

func main() {
	targetOS := flag.String("os", runtime.GOOS, "target GOOS (darwin, windows)")
	targetArch := flag.String("arch", runtime.GOARCH, "target GOARCH (amd64, arm64, 386)")
	dest := flag.String("dest", "", "cores directory (default: internal/proxy/cores, resolved from the repo root)")
	flag.Parse()

	if *dest == "" {
		root, err := repoRoot()
		if err != nil {
			log.Fatal(err)
		}
		*dest = filepath.Join(root, "internal", "proxy", "cores")
	}
	dir := filepath.Join(*dest, *targetOS+"-"+*targetArch)
	if _, err := os.Stat(dir); err != nil {
		log.Fatalf("unsupported target %s/%s: %v", *targetOS, *targetArch, err)
	}

	want := fmt.Sprintf("sing-box=%s\nxray=%s\n", proxy.RequiredCoreVersion, proxy.RequiredXrayVersion)
	versionsPath := filepath.Join(dir, "VERSIONS")
	if cur, err := os.ReadFile(versionsPath); err == nil && string(cur) == want &&
		fileExists(filepath.Join(dir, paths.SingboxName)) && fileExists(filepath.Join(dir, paths.XrayName)) {
		fmt.Printf("cores for %s/%s already at pinned versions, nothing to do\n", *targetOS, *targetArch)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	log.Printf("fetching sing-box v%s for %s/%s…", proxy.RequiredCoreVersion, *targetOS, *targetArch)
	if err := fetchSingbox(ctx, *targetOS, *targetArch, filepath.Join(dir, paths.SingboxName)); err != nil {
		log.Fatalf("sing-box: %v", err)
	}
	log.Printf("fetching Xray v%s for %s/%s…", proxy.RequiredXrayVersion, *targetOS, *targetArch)
	if err := fetchXray(ctx, *targetOS, *targetArch, filepath.Join(dir, paths.XrayName)); err != nil {
		log.Fatalf("xray: %v", err)
	}
	if err := os.WriteFile(versionsPath, []byte(want), 0644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("cores for %s/%s ready in %s\n", *targetOS, *targetArch, dir)
}

// repoRoot walks up from the working directory to the directory holding go.mod,
// so the tool works both from the repo root and via `go generate` in a package.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fetchSingbox(ctx context.Context, targetOS, targetArch, dest string) error {
	suffix := singboxAssetSuffix(targetOS, targetArch)
	if suffix == "" {
		return fmt.Errorf("unsupported platform %s/%s", targetOS, targetArch)
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/v%s", singboxCoreRepo, proxy.RequiredCoreVersion)
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
		return fmt.Errorf("no asset matching %q in release v%s", suffix, proxy.RequiredCoreVersion)
	}

	want := paths.SingboxName
	if targetOS == "windows" {
		want += ".exe"
	}
	if strings.HasSuffix(suffix, ".zip") {
		return downloadAndExtract(ctx, dlURL, want, dest, extractZipEntry)
	}
	return downloadAndExtract(ctx, dlURL, want, dest, extractTarGz)
}

func fetchXray(ctx context.Context, targetOS, targetArch, dest string) error {
	asset := xrayAssetName(targetOS, targetArch)
	if asset == "" {
		return fmt.Errorf("unsupported platform for Xray: %s/%s", targetOS, targetArch)
	}
	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", xrayCoreRepo, proxy.RequiredXrayVersion, asset)
	want := paths.XrayName
	if targetOS == "windows" {
		want += ".exe"
	}
	return downloadAndExtract(ctx, url, want, dest, extractZipEntry)
}

func singboxAssetSuffix(targetOS, targetArch string) string {
	switch targetOS {
	case "darwin":
		return "darwin-" + targetArch + ".tar.gz"
	case "windows":
		switch targetArch {
		case "amd64", "arm64":
			return "windows-" + targetArch + ".zip"
		case "386":
			return "windows-386-legacy-windows-7.zip"
		}
	}
	return ""
}

func xrayAssetName(targetOS, targetArch string) string {
	osPart := map[string]string{"darwin": "macos", "windows": "windows"}[targetOS]
	if osPart == "" {
		return ""
	}
	switch targetArch {
	case "amd64":
		return fmt.Sprintf("Xray-%s-64.zip", osPart)
	case "arm64":
		return fmt.Sprintf("Xray-%s-arm64-v8a.zip", osPart)
	case "386":
		return fmt.Sprintf("Xray-%s-32.zip", osPart)
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

func fetchRelease(ctx context.Context, url string) (*ghRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "freesurf/1.0")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
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

// downloadAndExtract fetches the archive at url to a temp file and writes the
// entry named wantBase to dest via the given extractor.
func downloadAndExtract(ctx context.Context, url, wantBase, dest string, extract func(archivePath, wantBase, dest string) error) error {
	tmp, err := os.CreateTemp("", "fetchcores-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := httpDownload(ctx, url, tmpPath); err != nil {
		return err
	}
	return extract(tmpPath, wantBase, dest)
}

func httpDownload(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "freesurf/1.0")

	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	const maxSize = 100 * 1024 * 1024
	_, err = io.Copy(f, io.LimitReader(resp.Body, maxSize))
	return err
}

func writeFile(dest string, r io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	const maxSize = 200 * 1024 * 1024
	_, err = io.Copy(out, io.LimitReader(r, maxSize))
	return err
}

// extractZipEntry writes the archive entry whose base name is wantBase to dest.
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
		return writeFile(dest, rc)
	}
	return fmt.Errorf("%q not found in archive", wantBase)
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
			return writeFile(dest, tr)
		}
	}
	return fmt.Errorf("%q not found in archive", wantBase)
}
