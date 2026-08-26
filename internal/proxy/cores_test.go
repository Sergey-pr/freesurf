package proxy

import (
	"os"
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

// The whole point of IsEmbeddedCore is that a binary which merely looks right is
// still rejected, so a tampered <data>/bin never reaches the privileged helper.
func TestIsEmbeddedCore(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, paths.SingboxName)
	if err := installEmbeddedCore(paths.SingboxName, dest); err != nil {
		t.Skipf("cores not embedded in this test build: %v", err)
	}

	if !IsEmbeddedCore(paths.SingboxName, dest) {
		t.Fatal("a freshly installed core was not recognised as the embedded one")
	}
	if IsEmbeddedCore(paths.SingboxName, filepath.Join(dir, "absent")) {
		t.Error("a missing file passed as the embedded core")
	}

	// A single flipped byte must be enough to reject it.
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	tampered := filepath.Join(dir, "tampered")
	if err := os.WriteFile(tampered, data, 0755); err != nil {
		t.Fatal(err)
	}
	if IsEmbeddedCore(paths.SingboxName, tampered) {
		t.Error("a modified binary passed as the embedded core")
	}

	// A different core is not interchangeable with this one either.
	xray := filepath.Join(dir, paths.XrayName)
	if err := installEmbeddedCore(paths.XrayName, xray); err != nil {
		t.Fatal(err)
	}
	if IsEmbeddedCore(paths.SingboxName, xray) {
		t.Error("Xray passed as the embedded sing-box")
	}
}
