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
	if coreVersionOK(path) {
		return path, nil
	}
	if err := installEmbeddedCore(paths.SingboxName, path); err != nil {
		return "", err
	}
	if !coreVersionOK(path) {
		return "", fmt.Errorf("embedded core did not report version %s", RequiredCoreVersion)
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
