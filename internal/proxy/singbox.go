package proxy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"freesurf/internal/paths"
)

// We pin the sing-box-lx fork (same as singbox-launcher): it builds every
// platform and its `version` output carries the full fork tag, so the check is
// exact. The Xray side handles the actual proxy protocols.
const (
	// RequiredCoreVersion is the pinned sing-box-lx release. Bumping it requires
	// re-running cmd/fetchcores (the Taskfile build tasks do this automatically).
	RequiredCoreVersion = "1.13.13-lx.15"
)

// EnsureCore installs the pinned sing-box binary (embedded at build time) if
// missing/out of date, returning its path.
func EnsureCore(ctx context.Context) (string, error) {
	path, err := paths.Singbox()
	if err != nil {
		return "", err
	}
	// Windows needs the Wintun driver alongside sing-box; no-op elsewhere.
	if err := EnsureWintun(ctx); err != nil {
		return "", err
	}
	if IsEmbeddedCore(paths.SingboxName, path) {
		return path, nil
	}
	if err := installEmbeddedCore(paths.SingboxName, path); err != nil {
		return "", err
	}
	if !IsEmbeddedCore(paths.SingboxName, path) {
		return "", fmt.Errorf("installed sing-box does not match the core embedded in this build")
	}
	return path, nil
}

// CheckConfig validates a sing-box config document with `sing-box check`. It takes
// the document, not a path, so the caller has nothing to leave lying around: this
// is a pre-flight on what the supervisor will generate, not the config root runs.
func CheckConfig(binPath string, cfg []byte) error {
	f, err := os.CreateTemp("", "freesurf-check-*.json")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(f.Name())
	}()
	if _, err := f.Write(cfg); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binPath, "check", "-c", f.Name()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sing-box check failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}
