package render

import (
	"os"

	"golang.org/x/sys/windows"
)

// utf8 is the console code page for UTF-8.
const utf8 = 65001

// PrepareConsole switches the console to UTF-8 for the run, and returns
// what puts it back. tenjin writes UTF-8 (✓, ✗, …), which a console on a
// legacy code page shows as garbage, and the code page belongs to the console
// window, so it must not be left changed. Without a console there is
// nothing to do.
func PrepareConsole() (restore func()) {
	cp, err := windows.GetConsoleOutputCP()
	if err != nil || cp == utf8 || windows.SetConsoleOutputCP(utf8) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleOutputCP(cp) }
}

// colourSupported turns on the console's handling of ANSI escape codes,
// which Windows consoles leave off unless a program asks, and reports
// whether that worked. Where it does not, as on an old console, output stays
// plain rather than full of escape codes.
func colourSupported(f *os.File) bool {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(handle, &mode) != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
