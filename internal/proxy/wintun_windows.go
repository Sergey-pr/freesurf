//go:build windows

package proxy

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"freesurf/internal/paths"
)

// wintun.dll is the WireGuard Wintun driver sing-box needs to open the TUN device
// on Windows.
//
//go:embed wintun/*.dll wintun/LICENSE.txt
var wintunFS embed.FS

const wintunName = "wintun.dll"

// WintunPath returns where wintun.dll is installed
func WintunPath() (string, error) {
	bin, err := paths.Bin()
	if err != nil {
		return "", err
	}
	return filepath.Join(bin, wintunName), nil
}

// EnsureWintun writes the embedded wintun.dll (and its license) next to the
// sing-box binary if it is missing.
func EnsureWintun(_ context.Context) error {
	dest, err := WintunPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(dest); err == nil {
		return nil
	}

	data, err := wintunFS.ReadFile("wintun/" + runtime.GOARCH + ".dll")
	if err != nil {
		return fmt.Errorf("no embedded wintun.dll for %s: %w", runtime.GOARCH, err)
	}
	if err := os.WriteFile(dest, data, 0755); err != nil {
		return err
	}

	// The Wintun license requires keeping its notices, so ship it alongside the driver
	if lic, err := wintunFS.ReadFile("wintun/LICENSE.txt"); err == nil {
		_ = os.WriteFile(filepath.Join(filepath.Dir(dest), "wintun-LICENSE.txt"), lic, 0644)
	}
	return nil
}

// reinstallWintun rewrites the embedded wintun.dll even if one is present.
func reinstallWintun(ctx context.Context) error {
	dest, err := WintunPath()
	if err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !os.IsNotExist(err) {
		return err
	}
	return EnsureWintun(ctx)
}
