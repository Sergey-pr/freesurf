//go:build darwin

package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"freesurf/internal/paths"
	"freesurf/internal/proxy"
)

// macOS runs the TUN core (which needs root) via a launchd LaunchDaemon installed
// once. The daemon is this same binary in supervisor mode: while the request file
// exists it keeps sing-box running (restarting it on crash); remove the request and
// it kills sing-box within ~1s, restoring routing. So after the one-time install (a
// single password prompt) the app starts/stops the tunnel by writing a file - no
// further prompts, even across app restarts and reboots.
//
// (launchd's own KeepAlive/PathState only governs restart, not stopping a running
// job, which is why we supervise sing-box ourselves.)
//
// Everything root executes or reads lives in rootHelperDir, owned by root:wheel.
const (
	helperLabel      = "com.freesurf.helper"
	helperPlistPath  = "/Library/LaunchDaemons/com.freesurf.helper.plist"
	rootHelperDir    = "/Library/Application Support/FreeSurf"
	rootSingboxPath  = rootHelperDir + "/sing-box"
	rootExePath      = rootHelperDir + "/freesurf"
	rootConfigPath   = rootHelperDir + "/config.json"
	rootCoreLogPath  = rootHelperDir + "/sing-box.log"
	rootStatusPath   = rootHelperDir + "/status.json"
	rootVersionFile  = rootHelperDir + "/helper.version"
	rootSupervisorLg = rootHelperDir + "/supervisor.log"

	// Bump when the plist/supervisor format changes to force a one-time reinstall.
	helperVersion = "3"
)

func darwinRootFiles() rootFiles {
	return rootFiles{
		exe:     rootExePath,
		singbox: rootSingboxPath,
		config:  rootConfigPath,
		log:     rootCoreLogPath,
		status:  rootStatusPath,
	}
}

// coreLogPath and statusPath are the root-owned files the app reads.
func coreLogPath() (string, error) { return rootCoreLogPath, nil }
func statusPath() string           { return rootStatusPath }

func HelperInstalled() bool {
	_, err := os.Stat(helperPlistPath)
	return err == nil
}

// rootSingboxOK digests the core launchd runs as root, rather than executing it.
func rootSingboxOK() bool {
	return proxy.IsEmbeddedCore(paths.SingboxName, rootSingboxPath)
}

// rootExeOK reports whether the installed supervisor is this build of the app.
func rootExeOK() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return sameFile(exe, rootExePath)
}

// helperMarker forces a reinstall on a version bump or a different request path.
func helperMarker(request string) string { return helperVersion + "\n" + request }

func installedHelperMarker() string {
	data, err := os.ReadFile(rootVersionFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// EnsureHelper installs/updates the privileged supervisor if needed, prompting for
// a password only when an install/update is actually required.
func EnsureHelper(singboxBin string) error {
	request, err := paths.Sentinel()
	if err != nil {
		return err
	}
	marker := helperMarker(request)
	if HelperInstalled() && rootSingboxOK() && rootExeOK() && installedHelperMarker() == marker {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := paths.Data()
	if err != nil {
		return err
	}

	stagedPlist := filepath.Join(dir, helperLabel+".plist")
	if err := os.WriteFile(stagedPlist, []byte(buildHelperPlist(request)), 0600); err != nil {
		return err
	}

	// Stop the daemon and any core it left behind before replacing the files.
	script := strings.Join([]string{
		"mkdir -p " + shq(rootHelperDir),
		"(launchctl bootout system " + shq(helperPlistPath) + " 2>/dev/null || true)",
		"(pkill -TERM -f " + shq("^"+rootSingboxPath) + " 2>/dev/null || true)",
		"sleep 1",
		"rm -f " + shq(rootSingboxPath) + " " + shq(rootExePath) + " " + shq(rootHelperDir+"/supervisor.sh"),
		"cp " + shq(singboxBin) + " " + shq(rootSingboxPath),
		"cp " + shq(exe) + " " + shq(rootExePath),
		"cp " + shq(stagedPlist) + " " + shq(helperPlistPath),
		"printf %s " + shq(marker) + " > " + shq(rootVersionFile),
		"chown -R root:wheel " + shq(rootHelperDir),
		"chmod 755 " + shq(rootHelperDir) + " " + shq(rootSingboxPath) + " " + shq(rootExePath),
		"chown root:wheel " + shq(helperPlistPath),
		"chmod 644 " + shq(helperPlistPath),
		"launchctl bootstrap system " + shq(helperPlistPath),
	}, " && ")

	return runOsascriptAdmin(script)
}

// sameFile reports whether two paths hold byte-identical contents.
func sameFile(a, b string) bool {
	da, err := fileSum(a)
	if err != nil {
		return false
	}
	db, err := fileSum(b)
	return err == nil && da == db
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = f.Close()
	}()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// UninstallHelper removes the LaunchDaemon and its files (one password prompt).
func UninstallHelper() error {
	script := strings.Join([]string{
		"(launchctl bootout system " + shq(helperPlistPath) + " 2>/dev/null || true)",
		"rm -f " + shq(helperPlistPath),
		"rm -rf " + shq(rootHelperDir),
	}, " && ")
	return runOsascriptAdmin(script)
}

func buildHelperPlist(request string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>%s</string>
		<string>%s</string>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, xmlEsc(helperLabel), xmlEsc(rootExePath), xmlEsc(flagRunService), xmlEsc(flagRequest), xmlEsc(request), xmlEsc(rootSupervisorLg))
}

// runOsascriptAdmin runs a /bin/sh command line as root via one GUI auth prompt.
func runOsascriptAdmin(shell string) error {
	script := "do shell script " + appleScriptQuote(shell) + " with administrator privileges"
	out, err := exec.Command("osascript", "-e", script).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "-128") || strings.Contains(msg, "User canceled") {
			return fmt.Errorf("authorization cancelled")
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("privileged install failed: %s", msg)
	}
	return nil
}

func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appleScriptQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func xmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
