//go:build !windows

package engine

// MaybeRunService is a no-op on non-Windows platforms: there is no in-process
// service mode to dispatch to. The Windows implementation lives in
// privileged_windows.go.
func MaybeRunService() bool { return false }
