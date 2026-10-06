package cli

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
)

// readLine reads one line of input, without its line ending.
func (e *environment) readLine() (string, error) {
	line, err := e.stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// confirm asks a yes/no question. An empty answer takes the default.
func (e *environment) confirm(w io.Writer, question string, def bool) (bool, error) {
	suffix := " [y/N] "
	if def {
		suffix = " [Y/n] "
	}
	_, _ = fmt.Fprint(w, question+suffix)
	answer, err := e.readLine()
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// ask prompts for a line of text.
func (e *environment) ask(w io.Writer, question string) (string, error) {
	_, _ = fmt.Fprint(w, question+" ")
	answer, err := e.readLine()
	return strings.TrimSpace(answer), err
}

// openBrowser opens a URL in the user's default browser.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
