//go:build windows

package engine

import (
	"fmt"
	"os"
	"os/exec"
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
// The same freesurf.exe binary is reused as the service: the SCM launches it with
// the flagRunService argument and MaybeRunService() routes it into svc.Run before
// the Wails GUI ever starts. Because the service runs as LocalSystem (which can't
// resolve the launching user's %AppData%), the absolute paths it needs are baked
// into the service's command line at install time.
const (
	serviceName        = "FreeSurfTunnel"
	serviceDisplayName = "FreeSurf Tunnel Helper"
	serviceDesc        = "Runs the FreeSurf VPN tunnel core (sing-box) with the privileges required for the TUN device."

	// Bump when the service definition or supervisor behaviour changes to force a
	// one-time reinstall.
	helperVersion = "1"

	// Internal flags handled by MaybeRunService before the GUI starts.
	flagRunService       = "--freesurf-tun-service"
	flagInstallService   = "--freesurf-install-service"
	flagUninstallService = "--freesurf-uninstall-service"
)

// serviceArgs are the absolute paths baked into the service command line so the
// LocalSystem supervisor can find everything without resolving the user profile.
type serviceArgs struct {
	singbox  string
	config   string
	log      string
	sentinel string
}

func parseServiceArgs(args []string) serviceArgs {
	var o serviceArgs
	for i := 0; i+1 < len(args); i += 2 {
		switch args[i] {
		case "--singbox":
			o.singbox = args[i+1]
		case "--config":
			o.config = args[i+1]
		case "--log":
			o.log = args[i+1]
		case "--sentinel":
			o.sentinel = args[i+1]
		}
	}
	return o
}

func (o serviceArgs) flags() []string {
	return []string{
		"--singbox", o.singbox,
		"--config", o.config,
		"--log", o.log,
		"--sentinel", o.sentinel,
	}
}

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

// EnsureHelper installs/updates the service if needed, prompting for elevation (a
// single UAC dialog) only when an install/update is actually required.
func EnsureHelper(singboxBin string) error {
	want, err := currentMarker()
	if err != nil {
		return err
	}
	if HelperInstalled() && installedMarker() == want {
		return nil
	}

	cfg, err := paths.Config()
	if err != nil {
		return err
	}
	logPath, err := paths.CoreLog()
	if err != nil {
		return err
	}
	sentinel, err := paths.Sentinel()
	if err != nil {
		return err
	}

	opts := serviceArgs{singbox: singboxBin, config: cfg, log: logPath, sentinel: sentinel}
	return runElevated(append([]string{flagInstallService}, opts.flags()...))
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
		opts := parseServiceArgs(os.Args[2:])
		_ = svc.Run(serviceName, &tunnelService{opts: opts})
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
	opts := parseServiceArgs(args)
	exe, err := os.Executable()
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

	s, err := m.CreateService(serviceName, exe, mgr.Config{
		DisplayName: serviceDisplayName,
		Description: serviceDesc,
		StartType:   mgr.StartAutomatic,
	}, append([]string{flagRunService}, opts.flags()...)...)
	if err != nil {
		return err
	}
	defer s.Close()

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
	opts serviceArgs
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

// coreProc tracks a running sing-box child so the supervisor can detect crashes
// without racing on exec.Cmd.ProcessState.
type coreProc struct {
	cmd  *exec.Cmd
	done chan struct{}
}

// supervise keeps sing-box running while the sentinel exists and stops it when the
// sentinel is removed - the Go port of the macOS supervisor.sh loop.
func (t *tunnelService) supervise(stop <-chan struct{}) {
	lg := newServiceLogger(t.opts.config)
	defer lg.Close()
	lg.Printf("supervisor started (singbox=%s)", t.opts.singbox)

	var cp *coreProc
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	stopCore := func() {
		if cp == nil {
			return
		}
		if cp.cmd.Process != nil {
			_ = cp.cmd.Process.Kill()
		}
		<-cp.done
		cp = nil
	}
	defer stopCore()

	for {
		// Reap a crashed core so it gets restarted below.
		if cp != nil {
			select {
			case <-cp.done:
				lg.Printf("sing-box exited")
				cp = nil
			default:
			}
		}

		select {
		case <-stop:
			lg.Printf("supervisor stopping")
			return
		case <-ticker.C:
		}

		switch {
		case fileExists(t.opts.sentinel) && cp == nil:
			c, err := t.startCore()
			if err != nil {
				lg.Printf("failed to start sing-box: %v", err)
			} else {
				cp = c
				lg.Printf("sing-box started")
			}
		case !fileExists(t.opts.sentinel) && cp != nil:
			lg.Printf("sentinel removed, stopping sing-box")
			stopCore()
		}
	}
}

func (t *tunnelService) startCore() (*coreProc, error) {
	logFile, err := os.OpenFile(t.opts.log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(t.opts.singbox, "run", "-c", t.opts.config)
	cmd.Dir = filepath.Dir(t.opts.singbox) // so a sibling wintun.dll is found
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}

	cp := &coreProc{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		logFile.Close()
		close(cp.done)
	}()
	return cp, nil
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

// currentMarker combines the helper version with the current exe path, so the
// service is reinstalled both on a version bump and when the app moves (its baked
// command-line path would otherwise be stale).
func currentMarker() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return helperVersion + "\n" + exe, nil
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

// ---- small helpers ----------------------------------------------------------

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

type serviceLogger struct{ f *os.File }

// newServiceLogger writes supervisor diagnostics next to the core log so service
// problems are visible (the service has no console).
func newServiceLogger(configPath string) *serviceLogger {
	p := filepath.Join(filepath.Dir(configPath), "tun-service.log")
	f, _ := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	return &serviceLogger{f: f}
}

func (l *serviceLogger) Printf(format string, a ...any) {
	if l.f == nil {
		return
	}
	fmt.Fprintf(l.f, "%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func (l *serviceLogger) Close() {
	if l.f != nil {
		l.f.Close()
	}
}
