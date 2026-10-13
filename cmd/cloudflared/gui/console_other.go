//go:build !windows

package gui

// hideConsoleWindow is a no-op on non-Windows platforms.
func hideConsoleWindow() {}
