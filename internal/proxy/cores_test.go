package proxy

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"freesurf/internal/paths"
)

// Verifies the build-time embedded cores extract and report the pinned versions.
// Skipped when the test binary was compiled before running cmd/fetchcores.
func TestInstallEmbeddedCore(t *testing.T) {
	dir := t.TempDir()
	for name, version := range map[string]string{
		paths.SingboxName: RequiredCoreVersion,
		paths.XrayName:    RequiredXrayVersion,
	} {
		dest := filepath.Join(dir, name)
		if err := installEmbeddedCore(name, dest); err != nil {
			t.Skipf("cores not embedded in this test build: %v", err)
		}
		out, err := exec.Command(dest, "version").CombinedOutput()
		if err != nil {
			t.Fatalf("%s version: %v\n%s", name, err, out)
		}
		if !strings.Contains(string(out), version) {
			t.Fatalf("%s reports wrong version (want %s):\n%s", name, version, out)
		}
	}
}
