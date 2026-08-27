// Package proxy manages the sing-box and Xray core binaries (embed, install,
// version, run) and generates their configuration.
package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"

	"freesurf/internal/paths"
)

// The proxy cores are not downloaded at runtime: `cmd/fetchcores` fetches the
// pinned releases into cores/<os>-<arch>/ at build time and a build-tagged
// cores_<os>_<arch>.go file embeds that directory (coresFS/coresSubdir). Only
// each directory's README is committed, so the embed pattern always compiles;
// a build made without running fetchcores fails here with a clear error.

//go:generate go run freesurf/cmd/fetchcores

// embeddedDigests caches each embedded core's SHA-256, which cannot change.
var embeddedDigests sync.Map

// IsEmbeddedCore digests the binary at path against this build's embedded core.
func IsEmbeddedCore(name, path string) bool {
	want, err := embeddedDigest(name)
	if err != nil {
		return false
	}
	got, err := fileDigest(path)
	return err == nil && got == want
}

func embeddedDigest(name string) (string, error) {
	if sum, ok := embeddedDigests.Load(name); ok {
		return sum.(string), nil
	}
	f, err := coresFS.Open(coresSubdir + "/" + name)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	sum, err := hashReader(f)
	if err != nil {
		return "", err
	}
	embeddedDigests.Store(name, sum)
	return sum, nil
}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	return hashReader(f)
}

func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// installEmbeddedCore writes the embedded core binary (base name without .exe,
// e.g. paths.SingboxName) to dest as an executable.
func installEmbeddedCore(name, dest string) error {
	f, err := coresFS.Open(coresSubdir + "/" + name)
	if err != nil {
		return fmt.Errorf("%s core is not embedded in this build (run `go generate ./internal/proxy`, or build via `task build`, and rebuild): %w", name, err)
	}
	defer func() {
		_ = f.Close()
	}()
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
	if !IsEmbeddedCore(paths.SingboxName, sb) {
		return fmt.Errorf("reinstalled sing-box does not match the core embedded in this build")
	}
	if err := installEmbeddedCore(paths.XrayName, xr); err != nil {
		return err
	}
	if !IsEmbeddedCore(paths.XrayName, xr) {
		return fmt.Errorf("reinstalled Xray does not match the core embedded in this build")
	}
	return reinstallWintun(ctx)
}

func writeExecutable(dest string, r io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()
	const maxSize = 200 * 1024 * 1024
	_, err = io.Copy(out, io.LimitReader(r, maxSize))
	return err
}
