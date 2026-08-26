//go:build windows

package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"freesurf/internal/paths"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Windows runs the TUN core (which needs privileges for the TUN/wintun device) via
// a native Go Windows service installed once. The service is a small supervisor
// loop running as LocalSystem: while the sentinel file exists it keeps sing-box
// running (restarting it on crash); remove the sentinel and the supervisor kills
// sing-box, restoring routing. So after the one-time install (a single UAC prompt)
// the app starts/stops the tunnel by creating/removing a file - no further prompts,
// even across app restarts and reboots.
//
// The same freesurf.exe binary is reused as the service, but a root-owned copy of
// it: the elevated installer copies this exe and the core into %ProgramData%\FreeSurf
// and locks that directory down to SYSTEM and Administrators, so LocalSystem never
// executes anything an unprivileged process could rewrite. The SCM launches the copy
// with flagRunService and MaybeRunService() routes it into svc.Run before the Wails
// GUI ever starts.
//
// Everything else the service needs lives in the same directory, so only the user's
// request file - which the supervisor validates - is passed on the command line.
const (
	serviceName        = "FreeSurfTunnel"
	serviceDisplayName = "FreeSurf Tunnel Helper"
	serviceDesc        = "Runs the FreeSurf VPN tunnel core (sing-box) with the privileges required for the TUN device."

	// Bump when the service definition or supervisor behaviour changes to force a
	// one-time reinstall.
	helperVersion = "3"

	// Internal flags handled by MaybeRunService before the GUI starts.
	flagInstallService   = "--freesurf-install-service"
	flagUninstallService = "--freesurf-uninstall-service"

	// flagSingboxSource names the core the elevated installer copies into the
	// root-owned directory; it is only ever read by that installer.
	flagSingboxSource = "--singbox-source"
)

func windowsRootFiles() rootFiles {
	dir := programDataDir()
	return rootFiles{
		exe:     filepath.Join(dir, "freesurf.exe"),
		singbox: filepath.Join(dir, "sing-box.exe"),
		config:  filepath.Join(dir, "config.json"),
		log:     filepath.Join(dir, "sing-box.log"),
		status:  filepath.Join(dir, "status.json"),
	}
}

// coreLogPath and statusPath are what the app reads: the log to display, the status
// to learn whether the tunnel came up.
func coreLogPath() (string, error) { return windowsRootFiles().log, nil }
func statusPath() string           { return windowsRootFiles().status }

// ---- public API (mirrors privileged_darwin.go) -----------------------------

// HelperInstalled reports whether the tunnel service is registered. It opens the
// SCM read-only so it works without elevation.
func HelperInstalled() bool {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(scm)

	namePtr, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return false
	}
	h, err := windows.OpenService(scm, namePtr, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	windows.CloseServiceHandle(h)
	return true
}

// serviceRunning reports whether the tunnel service is currently in the running
// state. Opens read-only, so it works without elevation. A stopped service can't
// supervise the tunnel, so EnsureHelper treats this as "needs (re)install".
func serviceRunning() bool {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(scm)

	namePtr, err := windows.UTF16PtrFromString(serviceName)
	if err != nil {
		return false
	}
	h, err := windows.OpenService(scm, namePtr, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(h)

	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &st); err != nil {
		return false
	}
	return st.CurrentState == windows.SERVICE_RUNNING
}

// EnsureHelper installs/updates the service if needed, prompting for elevation (a
// single UAC dialog) only when an install/update is actually required.
func EnsureHelper(singboxBin string) error {
	want, err := currentMarker()
	if err != nil {
		return err
	}
	if HelperInstalled() && installedMarker() == want && serviceRunning() {
		return nil
	}

	request, err := paths.Sentinel()
	if err != nil {
		return err
	}
	return runElevated([]string{flagInstallService, flagSingboxSource, singboxBin, flagRequest, request})
}

// UninstallHelper removes the service (one UAC prompt).
func UninstallHelper() error {
	if !HelperInstalled() {
		return nil
	}
	return runElevated([]string{flagUninstallService})
}

// ---- service-mode entry point ----------------------------------------------

// MaybeRunService handles the internal service-mode invocations: running as the
// Windows service, or the elevated install/uninstall worker. It returns true if
// the process was started in one of those modes and the caller (main) should exit
// instead of launching the GUI.
func MaybeRunService() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case flagRunService:
		_ = svc.Run(serviceName, &tunnelService{request: parseRequestPath(os.Args[2:])})
		return true
	case flagInstallService:
		if err := installServiceWorker(os.Args[2:]); err != nil {
			os.Exit(1) // non-zero so the parent EnsureHelper sees the failure
		}
		return true
	case flagUninstallService:
		if err := uninstallServiceWorker(); err != nil {
			os.Exit(1)
		}
		return true
	}
	return false
}

// ---- elevated install / uninstall workers ----------------------------------

func installServiceWorker(args []string) error {
	request := parseRequestPath(args)
	if request == "" {
		return fmt.Errorf("no request path given")
	}
	source := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flagSingboxSource {
			source = args[i+1]
		}
	}

	files, err := installRootFiles(source)
	if err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// Idempotent reinstall: drop any existing instance first.
	if s, err := m.OpenService(serviceName); err == nil {
		_ = stopAndDelete(s)
		s.Close()
	}

	s, err := m.CreateService(serviceName, files.exe, mgr.Config{
		DisplayName: serviceDisplayName,
		Description: serviceDesc,
		StartType:   mgr.StartAutomatic,
	}, flagRunService, flagRequest, request)
	if err != nil {
		return err
	}
	defer s.Close()

	// Auto-restart if the supervisor process ever dies, so the tunnel survives a
	// crash without another elevation prompt (the Windows analogue of launchd
	// KeepAlive). Reset the failure counter after an hour of healthy running.
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}, 3600)

	if err := writeMarker(); err != nil {
		return err
	}
	if err := s.Start(); err != nil {
		return fmt.Errorf("service created but failed to start: %w", err)
	}
	return nil
}

func uninstallServiceWorker() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return nil // already gone
	}
	defer s.Close()

	if err := stopAndDelete(s); err != nil {
		return err
	}
	_ = os.Remove(markerPath())
	return nil
}

func stopAndDelete(s *mgr.Service) error {
	_, _ = s.Control(svc.Stop)
	for i := 0; i < 25; i++ {
		st, err := s.Query()
		if err != nil || st.State == svc.Stopped {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	return s.Delete()
}

// ---- the service itself -----------------------------------------------------

type tunnelService struct {
	request string
}

func (t *tunnelService) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.StartPending}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		t.supervise(stop)
		close(done)
	}()

	status <- svc.Status{State: svc.Running, Accepts: accepted}

	for c := range r {
		switch c.Cmd {
		case svc.Interrogate:
			status <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			close(stop)
			<-done
			status <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
	return false, 0
}

// supervise runs the shared supervisor loop: keep sing-box running while the
// request exists, stop it when it goes away. The loop, the config generation and
// the status reporting are the same code macOS runs under launchd.
func (t *tunnelService) supervise(stop <-chan struct{}) {
	files := windowsRootFiles()
	lg, closeLog := supervisorLogger(filepath.Join(programDataDir(), "tun-service.log"))
	defer closeLog()
	superviseTunnel(files, t.request, stop, lg)
}

// installRootFiles copies this exe and the core into the root-owned directory and
// locks it down, so LocalSystem only ever executes files an unprivileged process
// cannot replace. Runs elevated.
func installRootFiles(singboxSource string) (rootFiles, error) {
	files := windowsRootFiles()
	dir := programDataDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return files, err
	}
	if err := restrictToAdmins(dir); err != nil {
		return files, fmt.Errorf("failed to secure %s: %w", dir, err)
	}

	exe, err := os.Executable()
	if err != nil {
		return files, err
	}
	if err := copyFile(exe, files.exe); err != nil {
		return files, fmt.Errorf("failed to copy the app binary: %w", err)
	}
	if singboxSource == "" {
		return files, fmt.Errorf("no sing-box source given")
	}
	if err := copyFile(singboxSource, files.singbox); err != nil {
		return files, fmt.Errorf("failed to copy the core: %w", err)
	}
	return files, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// restrictToAdmins replaces the directory's inherited ACL with one granting full
// access to SYSTEM and Administrators and read/execute to everyone else. Without
// this, %ProgramData% subdirectories let ordinary users create files, which is the
// whole hole we are closing.
func restrictToAdmins(dir string) error {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return err
	}

	entries := []windows.EXPLICIT_ACCESS{
		explicitAccess(system, windows.GENERIC_ALL, windows.GRANT_ACCESS),
		explicitAccess(admins, windows.GENERIC_ALL, windows.GRANT_ACCESS),
		explicitAccess(users, windows.GENERIC_READ|windows.GENERIC_EXECUTE, windows.GRANT_ACCESS),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	// PROTECTED_DACL_SECURITY_INFORMATION drops the inherited entries that would
	// otherwise let Users write here.
	return windows.SetNamedSecurityInfo(
		dir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

func explicitAccess(sid *windows.SID, mask windows.ACCESS_MASK, mode windows.ACCESS_MODE) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: mask,
		AccessMode:        mode,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

// ---- elevation (re-launch self as admin and wait) ---------------------------

var (
	shell32            = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteEx = shell32.NewProc("ShellExecuteExW")
)

// shellExecuteInfo mirrors the Win32 SHELLEXECUTEINFOW layout.
type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.HWND
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       windows.Handle
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

// runElevated re-launches this executable with the given args via the "runas"
// verb (one UAC prompt), waits for it, and maps its exit code to an error.
func runElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(quoteArgs(args))

	const (
		seeMaskNoCloseProcess = 0x00000040
		swHide                = 0
		errorCancelled        = 1223 // ERROR_CANCELLED: user declined the UAC prompt
	)
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		nShow:        swHide,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	ret, _, callErr := procShellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == errorCancelled {
			return fmt.Errorf("authorization cancelled")
		}
		return fmt.Errorf("failed to request elevation: %v", callErr)
	}
	if info.hProcess == 0 {
		return fmt.Errorf("elevation did not start a process")
	}
	defer windows.CloseHandle(info.hProcess)

	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("privileged install failed (exit code %d)", code)
	}
	return nil
}

func quoteArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = syscall.EscapeArg(a)
	}
	return strings.Join(parts, " ")
}

// ---- version marker (machine-wide, written by the elevated worker) ----------

func programDataDir() string {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Join(base, "FreeSurf")
}

func markerPath() string { return filepath.Join(programDataDir(), "helper.version") }

// currentMarker combines the helper version with the exe path and the request file
// baked into the service command line, so the service is reinstalled on a version
// bump, when the app moves, and for a different user whose request file the
// installed service would otherwise never watch.
func currentMarker() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	request, err := paths.Sentinel()
	if err != nil {
		return "", err
	}
	return helperVersion + "\n" + exe + "\n" + request, nil
}

func installedMarker() string {
	data, err := os.ReadFile(markerPath())
	if err != nil {
		return ""
	}
	return string(data)
}

func writeMarker() error {
	if err := os.MkdirAll(programDataDir(), 0755); err != nil {
		return err
	}
	want, err := currentMarker()
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(), []byte(want), 0644)
}
