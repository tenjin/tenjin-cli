//go:build !windows

package render

import "os"

// colourSupported reports whether a terminal shows ANSI colour, which every
// terminal outside Windows does.
func colourSupported(*os.File) bool { return true }

// PrepareConsole has nothing to do outside Windows.
func PrepareConsole() (restore func()) { return func() {} }
