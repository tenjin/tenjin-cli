package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/buildinfo"
	"github.com/tenjin/tenjin-cli/internal/config"
	"github.com/tenjin/tenjin-cli/internal/proc"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/telemetry"
	"github.com/tenjin/tenjin-cli/internal/transport"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// telemetryWait bounds sending a command's event when the command waits for
// it: with --verbose, which shows the request, or where no copy of the CLI
// could be started to send it.
const telemetryWait = time.Second

// telemetryEventVar hands an event to a copy of the CLI started only to send
// it, so the command ends without waiting for the network.
const telemetryEventVar = "TENJIN_TELEMETRY_EVENT"

// detachedWait bounds the send of a copy that nobody waits for.
const detachedWait = 10 * time.Second

// sendDetached starts a copy of the CLI that sends ev on its own.
func sendDetached(ev telemetry.Event) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return proc.Detach(exe, nil, append(os.Environ(), telemetryEventVar+"="+string(raw)))
}

// sendHandedEvent is the whole run of a copy started by sendDetached.
func sendHandedEvent(raw string) {
	endpoints, err := config.Resolve(os.Getenv)
	if err != nil {
		return
	}
	c := telemetryClient(os.Getenv, endpoints)
	var ev telemetry.Event
	if c == nil || json.Unmarshal([]byte(raw), &ev) != nil {
		return
	}
	c.Transport = transport.New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), detachedWait)
	defer cancel()
	c.Send(ctx, ev)
}

// telemetryClient returns the client that sends each command's event, or
// nil: in a build without a key, when it was turned off, and against any
// Tenjin but production.
func telemetryClient(getenv func(string) string, endpoints config.Endpoints) *telemetry.Client {
	if telemetry.Key() == "" || telemetry.Off(getenv) || endpoints.Name != config.ProductionName {
		return nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	return &telemetry.Client{Key: telemetry.Key(), Endpoint: telemetry.Endpoint, State: filepath.Join(dir, "tenjin", "telemetry.json")}
}

// note adds a fact to the command's event. Facts are the CLI's own names
// and counts, never text from the project or the user.
func (e *environment) note(key string, value any) {
	if e.facts == nil {
		e.facts = map[string]any{}
	}
	e.facts[key] = value
}

// noteProject notes what kind of project the command ran on.
func (e *environment) noteProject(p *project.Project) {
	e.note("framework", string(p.Framework))
	var platforms []string
	for _, t := range p.Targets {
		if !slices.Contains(platforms, string(t.Platform)) {
			platforms = append(platforms, string(t.Platform))
		}
	}
	slices.Sort(platforms)
	e.note("platforms", platforms)
	e.note("has_tenjin_sdk", len(p.Tenjin) > 0)
	adSDKs := []string{}
	for _, a := range p.AdSDKs {
		if !slices.Contains(adSDKs, a.Name) {
			adSDKs = append(adSDKs, a.Name)
		}
	}
	slices.Sort(adSDKs)
	e.note("ad_sdks", adSDKs)
}

// noteReport notes which checks failed and which warned.
func (e *environment) noteReport(r *verify.Report) {
	failed, warned := []string{}, []string{}
	for _, f := range r.Findings {
		switch f.Severity {
		case verify.Error:
			if !slices.Contains(failed, f.ID) {
				failed = append(failed, f.ID)
			}
		case verify.Warning:
			if !slices.Contains(warned, f.ID) {
				warned = append(warned, f.ID)
			}
		}
	}
	slices.Sort(failed)
	slices.Sort(warned)
	e.note("failed_checks", failed)
	e.note("warned_checks", warned)
}

// sendTelemetry starts sending the event of the command that ran, and
// returns what waits for it to be sent.
func (e *environment) sendTelemetry(ran *cobra.Command, code int, kind string, took time.Duration) (wait func()) {
	if e.telemetry == nil || ran == nil {
		return func() {}
	}
	// Shell completion runs inside the shell, as the person types.
	for c := ran; c != nil; c = c.Parent() {
		switch c.Name() {
		case "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return func() {}
		}
	}
	var flags []string
	ran.Flags().Visit(func(f *pflag.Flag) { flags = append(flags, f.Name) })
	info := buildinfo.Get()
	ev := telemetry.Event{
		Command:     strings.TrimSpace(ran.CommandPath()),
		Flags:       flags,
		ExitCode:    code,
		Error:       kind,
		Duration:    took,
		Version:     info.Version,
		OS:          info.OS,
		Arch:        info.Arch,
		Agent:       agent.Inside(e.getenv),
		Interactive: e.interactive,
		Facts:       e.facts,
	}
	if e.verbose == nil && e.detach != nil && e.detach(ev) == nil {
		return func() {}
	}
	e.telemetry.Transport = e.transport()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), telemetryWait)
		defer cancel()
		e.telemetry.Send(ctx, ev)
	}()
	return func() { <-done }
}
