// Package buildinfo reports which build of the CLI is running.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Stamped by the linker (see the Makefile). A plain `go build` or `go install`
// leaves them unset, and Get falls back to what the Go toolchain recorded.
var (
	version string
	commit  string
	date    string
)

// Info describes one build.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Get returns the build's identity.
func Get() Info {
	info := Info{
		Version: version,
		Commit:  commit,
		Date:    date,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && info.Commit == "":
				info.Commit = s.Value
			case s.Key == "vcs.time" && info.Date == "":
				info.Date = s.Value
			}
		}
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.Date == "" {
		info.Date = "unknown"
	}
	return info
}

func (i Info) String() string {
	return fmt.Sprintf("Tenjin CLI %s (commit %s, built %s, %s %s/%s)", i.Version, i.Commit, i.Date, i.Go, i.OS, i.Arch)
}
