//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Security -framework Foundation

#include <stdlib.h>
#include <Security/Security.h>
#include <stdio.h>
#include <unistd.h>

// AuthorizationExecuteWithPrivileges is deprecated but still functional and is the
// standard way to run a helper as root from a GUI app. A single AuthorizationRef
// is created once and reused, so the user is prompted for their password only
// once per app session (this fixes "prompt on every Start").
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
static AuthorizationRef g_authRef = NULL;

static int runWithPrivileges(const char *path, char **args, pid_t *outScriptPid, pid_t *outSingboxPid) {
	*outScriptPid = 0;
	*outSingboxPid = 0;
	if (g_authRef == NULL) {
		OSStatus status = AuthorizationCreate(NULL, kAuthorizationEmptyEnvironment,
			kAuthorizationFlagInteractionAllowed | kAuthorizationFlagExtendRights, &g_authRef);
		if (status != errAuthorizationSuccess) {
			return (int)status;
		}
	}
	FILE *pipe = NULL;
	OSStatus status = AuthorizationExecuteWithPrivileges(g_authRef, path,
		kAuthorizationFlagDefaults, args, &pipe);
	if (status != errAuthorizationSuccess) {
		return (int)status;
	}
	if (pipe) {
		char buf[32];
		if (fgets(buf, (int)sizeof(buf), pipe)) {
			long p = strtol(buf, NULL, 10);
			if (p > 0) *outScriptPid = (pid_t)p;
		}
		if (fgets(buf, (int)sizeof(buf), pipe)) {
			long p = strtol(buf, NULL, 10);
			if (p > 0) *outSingboxPid = (pid_t)p;
		}
		fclose(pipe);
	}
	return 0;
}

void freeAuth(void) {
	if (g_authRef != NULL) {
		AuthorizationFree(g_authRef, kAuthorizationFlagDestroyRights);
		g_authRef = NULL;
	}
}
#pragma clang diagnostic pop
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"unsafe"
)

// runPrivileged runs toolPath with elevated privileges, returning the first two
// decimal PIDs the child prints on stdout (script PID, then sing-box PID).
func runPrivileged(toolPath string, args []string) (scriptPID, singboxPID int, err error) {
	cPath := C.CString(toolPath)
	defer C.free(unsafe.Pointer(cPath))

	cArgs := make([]*C.char, 0, len(args)+1)
	for _, a := range args {
		cArgs = append(cArgs, C.CString(a))
	}
	defer func() {
		for _, p := range cArgs {
			C.free(unsafe.Pointer(p))
		}
	}()
	cArgs = append(cArgs, nil)

	var cScript, cCore C.pid_t
	code := C.runWithPrivileges(cPath, &cArgs[0], &cScript, &cCore)
	if code != 0 {
		// -60006 (errAuthorizationCanceled) and friends mean the user dismissed
		// the prompt.
		return 0, 0, fmt.Errorf("authorization failed or was cancelled (status %d)", int(code))
	}
	return int(cScript), int(cCore), nil
}

// startTunnelPrivileged launches sing-box as root via a small shell script. The
// script truncates the log, backgrounds the core, reports both PIDs, then babysits
// the core: it keeps running only while the sentinel file exists AND the launcher
// (appPID) is alive. Removing the sentinel or quitting the app makes the core
// self-terminate — so neither Stop nor app-exit needs another privilege prompt.
func startTunnelPrivileged(binPath, cfgPath, logPath, sentinel string, appPID int) (scriptPID, singboxPID int, err error) {
	dir, err := appDataDir()
	if err != nil {
		return 0, 0, err
	}
	scriptPath := filepath.Join(dir, "start-singbox.sh")

	script := fmt.Sprintf(`#!/bin/sh
echo $$
: > %s
%s run -c %s >> %s 2>&1 &
CORE=$!
echo $CORE
exec 1>>%s 2>&1
while [ -e %s ] && kill -0 %d 2>/dev/null; do
  sleep 1
done
kill -TERM $CORE 2>/dev/null
wait $CORE
`, strconv.Quote(logPath), strconv.Quote(binPath), strconv.Quote(cfgPath), strconv.Quote(logPath),
		strconv.Quote(logPath), strconv.Quote(sentinel), appPID)

	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		return 0, 0, err
	}

	scriptPID, singboxPID, err = runPrivileged("/bin/sh", []string{scriptPath})
	if err != nil {
		return 0, 0, err
	}
	if singboxPID == 0 {
		return 0, 0, fmt.Errorf("tunnel script did not report a sing-box PID")
	}
	return scriptPID, singboxPID, nil
}

// processAlive reports whether a (root-owned) process is still running.
// kill(pid, 0) returns EPERM for a live process we don't own — still "alive".
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// FreePrivilegedAuthorization releases the cached authorization (call on shutdown).
func FreePrivilegedAuthorization() {
	C.freeAuth()
}
