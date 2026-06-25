//go:build !windows

package proxy

import "context"

// EnsureWintun is a no-op off Windows - the Wintun driver is only needed there.
func EnsureWintun(_ context.Context) error { return nil }
