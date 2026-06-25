package proxy

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"freesurf/internal/paths"
)

// sing-box needs the Wintun driver (wintun.dll) sitting next to it on Windows to
// open the TUN device. The sing-box-lx archives don't bundle it, so we fetch it
// from the official WireGuard distribution and pin the version (0.14.1 is the
// final Wintun release).
const (
	wintunVersion = "0.14.1"
	wintunName    = "wintun.dll"
)

func wintunURL() string {
	return fmt.Sprintf("https://www.wintun.net/builds/wintun-%s.zip", wintunVersion)
}

// wintunArchDir maps GOARCH to the directory name used inside the Wintun archive.
func wintunArchDir() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	case "arm":
		return "arm"
	}
	return ""
}

// WintunPath returns where wintun.dll is installed - in the bin dir next to the
// sing-box binary, so sing-box (launched with that dir as its working directory)
// loads it.
func WintunPath() (string, error) {
	bin, err := paths.Bin()
	if err != nil {
		return "", err
	}
	return filepath.Join(bin, wintunName), nil
}

func wintunInstalled() bool {
	p, err := WintunPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// EnsureWintun installs wintun.dll next to the sing-box binary if it is missing.
// It is a no-op on non-Windows platforms.
func EnsureWintun(ctx context.Context) error {
	if runtime.GOOS != "windows" || wintunInstalled() {
		return nil
	}

	archDir := wintunArchDir()
	if archDir == "" {
		return fmt.Errorf("unsupported architecture for wintun: %s", runtime.GOARCH)
	}
	dest, err := WintunPath()
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "wintun-dl-*.zip")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err := httpDownload(ctx, wintunURL(), tmpPath); err != nil {
		return err
	}

	// Archive entries look like "wintun/bin/<arch>/wintun.dll"; match the arch
	// directory so we don't grab the wrong build (every arch ships a file of the
	// same base name).
	wantSuffix := "bin/" + archDir + "/" + wintunName
	return extractZipPath(tmpPath, wantSuffix, dest)
}

// extractZipPath writes the archive entry whose name ends with wantSuffix to dest.
// Unlike extractZipEntry it matches the full (forward-slash) path tail, so it can
// disambiguate same-named files in different directories.
func extractZipPath(archivePath, wantSuffix, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if !strings.HasSuffix(zf.Name, wantSuffix) {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeExecutable(dest, rc)
	}
	return fmt.Errorf("%q not found in archive", wantSuffix)
}
