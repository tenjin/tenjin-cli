package devicelog

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// iosLogWindow is how far back a simulator's log is read. It covers a launch
// made a few minutes before the check.
const iosLogWindow = "10m"

// Simulator is a booted iOS simulator.
type Simulator struct {
	UDID string
	Name string
}

// ReadIOSSimulator returns the SDK's lines from a booted iOS simulator.
// udid picks one when several are booted. A physical iPhone is read with
// ReadIPhone instead. The error is an *Unavailable when there is nothing to
// read from.
func ReadIOSSimulator(ctx context.Context, run Runner, xcrun, udid string) ([]byte, error) {
	return ReadIOSSimulatorSince(ctx, run, xcrun, udid, time.Time{})
}

// ReadIOSSimulatorSince is ReadIOSSimulator for what was logged since a
// moment, or in the last ten minutes when since is zero. A simulator logs
// to this Mac's own log, on its clock.
func ReadIOSSimulatorSince(ctx context.Context, run Runner, xcrun, udid string, since time.Time) ([]byte, error) {
	out, err := run(ctx, xcrun, "simctl", "list", "devices", "booted", "-j")
	if err != nil {
		return nil, &Unavailable{
			Reason: fmt.Sprintf("Xcode's simctl could not list simulators: %v", err),
			Fix:    "Check that Xcode is installed and selected: run 'xcrun simctl list devices'.",
		}
	}
	booted, err := bootedSimulators(out)
	if err != nil {
		return nil, &Unavailable{Reason: fmt.Sprintf("Xcode's simctl gave a list of simulators this CLI cannot read: %v", err)}
	}
	names := func() string {
		var all []string
		for _, s := range booted {
			all = append(all, s.Name+" ("+s.UDID+")")
		}
		return strings.Join(all, ", ")
	}
	switch {
	case len(booted) == 0:
		return nil, &Unavailable{
			Reason: "No iOS simulator is running",
			Fix:    "Run the app's debug build on a simulator from Xcode, or connect an iPhone by cable with the app installed, then run this again.",
		}
	case udid == "" && len(booted) > 1:
		return nil, &Unavailable{
			Reason: "More than one iOS simulator is running: " + names(),
			Fix:    "Pass --serial with the UDID of the one to read.",
		}
	case udid == "":
		udid = booted[0].UDID
	case !hasSimulator(booted, udid):
		return nil, &Unavailable{
			Reason: "No running iOS simulator has the UDID " + udid,
			Fix:    "Running: " + names() + ".",
		}
	}

	window := []string{"--last", iosLogWindow}
	if !since.IsZero() {
		window = []string{"--start", since.Format("2006-01-02 15:04:05")}
	}
	args := append([]string{"simctl", "spawn", udid, "log", "show"}, window...)
	args = append(args, "--style", "compact", "--predicate", `eventMessage CONTAINS "`+iosTag+`"`)
	log, err := run(ctx, xcrun, args...)
	if err != nil {
		return nil, &Unavailable{Reason: fmt.Sprintf("Could not read the log of the simulator %s: %v", udid, err)}
	}
	return log, nil
}

// bootedSimulators reads the output of `simctl list devices booted -j`.
func bootedSimulators(out []byte) ([]Simulator, error) {
	var list struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, err
	}
	var booted []Simulator
	for _, devices := range list.Devices {
		for _, d := range devices {
			if d.State == "Booted" {
				booted = append(booted, Simulator{UDID: d.UDID, Name: d.Name})
			}
		}
	}
	// The JSON is keyed by runtime; sort so the order does not depend on it.
	sort.Slice(booted, func(i, j int) bool { return booted[i].Name < booted[j].Name })
	return booted, nil
}

func hasSimulator(booted []Simulator, udid string) bool {
	for _, s := range booted {
		if s.UDID == udid {
			return true
		}
	}
	return false
}
