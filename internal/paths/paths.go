// Package paths centralizes every on-disk location FreeSurf uses, all rooted at
// the per-user data directory.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

// SingboxName and XrayName are the core binary base names (the engine also matches
// the Xray process by this name for routing).
const (
	SingboxName = "sing-box"
	XrayName    = "xray"
)

// Data returns the FreeSurf data directory, creating it if needed.
func Data() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(configDir, "FreeSurf")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	// Chmod as well as create: MkdirAll leaves an existing directory's mode alone.
	if err := chmodOwnerOnly(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// chmodOwnerOnly narrows a directory to its owner, and does nothing on Windows.
func chmodOwnerOnly(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(dir, 0700)
}

// Bin returns the directory holding the downloaded core binaries.
func Bin() (string, error) {
	dir, err := Data()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		return "", err
	}
	if err := chmodOwnerOnly(bin); err != nil {
		return "", err
	}
	return bin, nil
}

func execName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func inBin(base string) (string, error) {
	bin, err := Bin()
	if err != nil {
		return "", err
	}
	return filepath.Join(bin, execName(base)), nil
}

func inData(name string) (string, error) {
	dir, err := Data()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// Singbox / Xray return the installed core binary paths.
func Singbox() (string, error) { return inBin(SingboxName) }
func Xray() (string, error)    { return inBin(XrayName) }

// XrayConfig returns the generated Xray config path; sing-box's is the supervisor's.
func XrayConfig() (string, error) { return inData("xray.json") }

// XrayLog returns the Xray log path; sing-box logs into the root-owned directory.
func XrayLog() (string, error) { return inData("xray.log") }

// RestrictFile narrows a file to its owner, and does nothing on Windows.
func RestrictFile(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, 0600)
}

// DB returns the SQLite database path.
func DB() (string, error) { return inData("freesurf.db") }

// Sentinel is the request file the supervisor watches; see engine.tunnelRequest.
func Sentinel() (string, error) { return inData("tunnel.run") }
