//go:build windows

package gui

import (
	"golang.org/x/sys/windows"
)

// hideConsoleWindow hides the Windows console window when running in GUI mode.
func hideConsoleWindow() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	user32 := windows.NewLazySystemDLL("user32.dll")
	getConsoleWindow := kernel32.NewProc("GetConsoleWindow")
	showWindow := user32.NewProc("ShowWindow")

	hwnd, _, _ := getConsoleWindow.Call()
	if hwnd != 0 {
		// SW_HIDE = 0
		showWindow.Call(hwnd, 0)
	}
}
