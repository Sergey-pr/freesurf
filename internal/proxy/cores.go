// Package proxy manages the sing-box and Xray core binaries (embed, install,
// version, run) and generates their configuration.
package proxy

import (
	"context"
	"fmt"
	"io"
	"os"

	"freesurf/internal/paths"
)

// The proxy cores are not downloaded at runtime: `cmd/fetchcores` fetches the
// pinned releases into cores/<os>-<arch>/ at build time and a build-tagged
// cores_<os>_<arch>.go file embeds that directory (coresFS/coresSubdir). Only
// each directory's README is committed, so the embed pattern always compiles;
// a build made without running fetchcores fails here with a clear error.

//go:generate go run freesurf/cmd/fetchcores

// installEmbeddedCore writes the embedded core binary (base name without .exe,
// e.g. paths.SingboxName) to dest as an executable.
func installEmbeddedCore(name, dest string) error {
	f, err := coresFS.Open(coresSubdir + "/" + name)
	if err != nil {
		return fmt.Errorf("%s core is not embedded in this build (run `go generate ./internal/proxy`, or build via `task build`, and rebuild): %w", name, err)
	}
	defer f.Close()
	return writeExecutable(dest, f)
}

// ReinstallCores force-reinstalls the embedded core binaries (and, on Windows,
// the Wintun driver) into <data>/bin, replacing whatever is on disk. The caller
// must ensure the tunnel is down first — the binaries may be running.
func ReinstallCores(ctx context.Context) error {
	sb, err := paths.Singbox()
	if err != nil {
		return err
	}
	xr, err := paths.Xray()
	if err != nil {
		return err
	}
	// Remove first so a broken binary can't survive a failed install.
	for _, p := range []string{sb, xr} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := installEmbeddedCore(paths.SingboxName, sb); err != nil {
		return err
	}
	if !coreVersionOK(sb) {
		return fmt.Errorf("reinstalled core did not report version %s", RequiredCoreVersion)
	}
	if err := installEmbeddedCore(paths.XrayName, xr); err != nil {
		return err
	}
	if !xrayVersionOK(xr) {
		return fmt.Errorf("reinstalled Xray did not report version %s", RequiredXrayVersion)
	}
	return reinstallWintun(ctx)
}

func writeExecutable(dest string, r io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	const maxSize = 200 * 1024 * 1024
	_, err = io.Copy(out, io.LimitReader(r, maxSize))
	return err
}
