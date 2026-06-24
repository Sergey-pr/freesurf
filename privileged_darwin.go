//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// macOS runs the TUN core (which needs root) via a launchd LaunchDaemon installed
// once. The daemon is a small root *supervisor* shell loop that starts/stops
// sing-box based on the sentinel file: while the sentinel exists it keeps sing-box
// running (restarting it if it crashes); remove the sentinel and the supervisor
// kills sing-box within ~1s — restoring routing. So after the one-time install (a
// single password prompt) the app starts/stops the tunnel by creating/removing a
// file: no CGO, no further prompts, even across app restarts and reboots.
//
// (launchd's own KeepAlive/PathState only governs *restart*, not stopping a
// running job, which is why we supervise sing-box ourselves.)
const (
	helperLabel      = "com.freesurf.helper"
	helperPlistPath  = "/Library/LaunchDaemons/com.freesurf.helper.plist"
	rootHelperDir    = "/Library/Application Support/FreeSurf"
	rootSingboxPath  = rootHelperDir + "/sing-box"
	rootSupervisor   = rootHelperDir + "/supervisor.sh"
	rootVersionFile  = rootHelperDir + "/helper.version"
	rootSupervisorLg = rootHelperDir + "/supervisor.log"

	// Bump when the plist/supervisor format changes to force a one-time reinstall.
	helperVersion = "2"
)

func helperInstalled() bool {
	_, err := os.Stat(helperPlistPath)
	return err == nil
}

func rootSingboxVersionOK() bool {
	out, err := exec.Command(rootSingboxPath, "version").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), requiredCoreVersion)
}

func installedHelperVersion() string {
	data, err := os.ReadFile(rootVersionFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ensureHelper installs/updates the privileged supervisor if needed. It only
// prompts for a password when an install/update is actually required.
func ensureHelper(singboxBin string) error {
	if helperInstalled() && rootSingboxVersionOK() && installedHelperVersion() == helperVersion {
		return nil
	}

	cfgPath, err := configPath()
	if err != nil {
		return err
	}
	logPath, err := coreLogPath()
	if err != nil {
		return err
	}
	sentinel, err := sentinelPath()
	if err != nil {
		return err
	}
	dir, err := appDataDir()
	if err != nil {
		return err
	}

	stagedPlist := filepath.Join(dir, helperLabel+".plist")
	stagedSupervisor := filepath.Join(dir, "supervisor.sh")
	if err := os.WriteFile(stagedPlist, []byte(buildHelperPlist()), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(stagedSupervisor, []byte(buildSupervisor(rootSingboxPath, cfgPath, logPath, sentinel)), 0644); err != nil {
		return err
	}

	script := strings.Join([]string{
		"mkdir -p " + shq(rootHelperDir),
		"cp " + shq(singboxBin) + " " + shq(rootSingboxPath),
		"cp " + shq(stagedSupervisor) + " " + shq(rootSupervisor),
		"cp " + shq(stagedPlist) + " " + shq(helperPlistPath),
		"printf %s " + shq(helperVersion) + " > " + shq(rootVersionFile),
		"chown -R root:wheel " + shq(rootHelperDir),
		"chmod 755 " + shq(rootSingboxPath) + " " + shq(rootSupervisor),
		"chown root:wheel " + shq(helperPlistPath),
		"chmod 644 " + shq(helperPlistPath),
		"(launchctl bootout system " + shq(helperPlistPath) + " 2>/dev/null || true)",
		"launchctl bootstrap system " + shq(helperPlistPath),
	}, " && ")

	return runOsascriptAdmin(script)
}

// uninstallHelper removes the LaunchDaemon and its files (one password prompt).
func uninstallHelper() error {
	script := strings.Join([]string{
		"(launchctl bootout system " + shq(helperPlistPath) + " 2>/dev/null || true)",
		"rm -f " + shq(helperPlistPath),
		"rm -rf " + shq(rootHelperDir),
	}, " && ")
	return runOsascriptAdmin(script)
}

// buildSupervisor returns the root supervisor shell loop: it keeps sing-box
// running while the sentinel exists and kills it when the sentinel is removed.
func buildSupervisor(singbox, cfgPath, logPath, sentinel string) string {
	return fmt.Sprintf(`#!/bin/sh
CORE=""
while :; do
  if [ -e %s ]; then
    if [ -z "$CORE" ] || ! kill -0 "$CORE" 2>/dev/null; then
      %s run -c %s >> %s 2>&1 &
      CORE=$!
    fi
  elif [ -n "$CORE" ]; then
    kill -TERM "$CORE" 2>/dev/null
    CORE=""
  fi
  sleep 1
done
`, shq(sentinel), shq(singbox), shq(cfgPath), shq(logPath))
}

func buildHelperPlist() string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>/bin/sh</string>
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
`, xmlEsc(helperLabel), xmlEsc(rootSupervisor), xmlEsc(rootSupervisorLg))
}

// runOsascriptAdmin runs a /bin/sh command line as root via one GUI auth prompt.
func runOsascriptAdmin(shell string) error {
	script := fmt.Sprintf("do shell script %s with administrator privileges", appleScriptQuote(shell))
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
