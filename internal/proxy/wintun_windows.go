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
// on Windows. The sing-box-lx archives don't bundle it, and the official download
// host (wintun.net) is unreachable from many of the networks this VPN targets, so
// we embed the prebuilt, unmodified driver and write it next to the sing-box
// binary at connect time - no runtime network dependency. Redistribution
// alongside software that uses Wintun via its API is permitted by the Wintun
// binaries license (kept in wintun/LICENSE.txt and shipped beside the driver).
//
//go:embed wintun/*.dll wintun/LICENSE.txt
var wintunFS embed.FS

const wintunName = "wintun.dll"

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

	// The Wintun license requires keeping its notices, so ship it alongside the
	// driver; best-effort.
	if lic, err := wintunFS.ReadFile("wintun/LICENSE.txt"); err == nil {
		_ = os.WriteFile(filepath.Join(filepath.Dir(dest), "wintun-LICENSE.txt"), lic, 0644)
	}
	return nil
}
