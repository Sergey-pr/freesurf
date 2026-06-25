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
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// Bin returns the directory holding the downloaded core binaries.
func Bin() (string, error) {
	dir, err := Data()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
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

// Config / XrayConfig return the generated config paths.
func Config() (string, error)     { return inData("config.json") }
func XrayConfig() (string, error) { return inData("xray.json") }

// CoreLog / XrayLog return the core log file paths.
func CoreLog() (string, error) { return inData("sing-box.log") }
func XrayLog() (string, error) { return inData("xray.log") }

// DB returns the SQLite database path.
func DB() (string, error) { return inData("freesurf.db") }

// Sentinel is the run-flag the privileged supervisor watches: sing-box runs while
// it exists and is stopped when it is removed.
func Sentinel() (string, error) { return inData("tunnel.run") }
