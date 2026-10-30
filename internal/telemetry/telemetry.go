// Package telemetry sends one anonymous event per command to Amplitude: which
// command ran, the names of the flags given, how it ended and how long it
// took, and a few facts about the project it ran on, such as its framework.
// Never a flag's value, a path, a bundle ID, a key or anything the project
// contains. A random ID, kept in a small state file, tells installs apart.
//
// It is off in builds without an Amplitude key (any build but a release), in
// CI, with TENJIN_NO_TELEMETRY or DO_NOT_TRACK set, and once turned off with
// tenjin telemetry off. The key is readable
// in a released binary, as any Amplitude ingestion key is: it only sends.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// apiKey is Amplitude's key for the CLI's project, stamped by the linker at
// release. Empty in every other build, which then sends nothing.
var apiKey string

// Endpoint is Amplitude's HTTP API.
const Endpoint = "https://api2.amplitude.com/2/httpapi"

// retryAfter is how long a send that could not reach Amplitude keeps the
// next ones from trying, so being offline costs one short wait an hour, not
// one per command.
const retryAfter = time.Hour

// OffVar turns telemetry off.
const OffVar = "TENJIN_NO_TELEMETRY"

// offVars turn telemetry off: the person's choice, and CI services, most of
// which set CI and some of which only their own variable.
var offVars = []string{OffVar, "DO_NOT_TRACK", "CI", "JENKINS_URL", "TF_BUILD", "TEAMCITY_VERSION", "CODEBUILD_BUILD_ID"}

// OffBy returns the variable that turns telemetry off, the person's or a CI
// service's, or "".
func OffBy(getenv func(string) string) string {
	for _, name := range offVars {
		if v := getenv(name); v != "" && v != "0" && v != "false" {
			return name
		}
	}
	return ""
}

// offFile records tenjin telemetry off, beside the state file. It is a file
// of its own because a send under way rewrites the state, and must not undo
// the choice.
const offFile = "telemetry-off"

func offPath(state string) string { return filepath.Join(filepath.Dir(state), offFile) }

// TurnedOff reports whether telemetry was turned off on this machine, for
// the state file state.
func TurnedOff(state string) bool {
	_, err := os.Lstat(offPath(state))
	return err == nil
}

// TurnOff records that nothing is to be sent from this machine, and forgets
// the install's ID, so that turning it on again counts a new install that
// nothing ties to this one.
func TurnOff(state string) error {
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(offPath(state), []byte("Usage statistics are off: tenjin telemetry on turns them on.\n"), 0o600); err != nil {
		return err
	}
	if err := os.Remove(state); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// TurnOn undoes TurnOff.
func TurnOn(state string) error {
	if err := os.Remove(offPath(state)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Key returns the key the build was stamped with, or "".
func Key() string { return apiKey }

// Client sends events.
type Client struct {
	Key      string
	Endpoint string
	// State is the file the install's ID is kept in.
	State string
	// Transport sends the request; nil is http.DefaultTransport.
	Transport http.RoundTripper
	Now       func() time.Time
}

// Event is one run of a command.
type Event struct {
	// Command is the command's path, such as "tenjin apps ensure".
	Command string
	// Flags are the names of the flags given, never their values.
	Flags    []string
	ExitCode int
	// Error is the error's kind, such as not_signed_in; "" when it ran.
	Error    string
	Duration time.Duration
	Version  string
	OS, Arch string
	// Agent is the coding agent the CLI was started by, if any.
	Agent       string
	Interactive bool
	// Facts are what the command adds, such as the project's framework.
	Facts map[string]any
}

type state struct {
	InstallID string    `json:"install_id"`
	FailedAt  time.Time `json:"failed_at,omitzero"`
}

// Send sends ev, unless telemetry was turned off since the command started.
// It never fails: telemetry must not get in the way of the command.
func (c *Client) Send(ctx context.Context, ev Event) {
	if TurnedOff(c.State) {
		return
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	s := c.load()
	if s.InstallID == "" {
		// An ID that cannot be kept would count each command as an install.
		s.InstallID = randomID()
		if c.save(s) != nil {
			return
		}
	}
	if since := now().Sub(s.FailedAt); since >= 0 && since < retryAfter {
		return
	}
	// Only a failure to reach Amplitude, or its asking to wait, pauses the
	// next sends. An event it refused is dropped alone, and a send cut short
	// by the wait was only slow.
	err := c.post(ctx, s.InstallID, ev, now())
	var refused refusal
	if err != nil && ctx.Err() == nil && (!errors.As(err, &refused) || refused.retry()) {
		s.FailedAt = now()
		_ = c.save(s)
	}
}

// refusal is Amplitude's answer when it was not 200.
type refusal int

func (r refusal) Error() string { return fmt.Sprintf("amplitude answered %d", int(r)) }

func (r refusal) retry() bool { return r == http.StatusTooManyRequests || r >= 500 }

func (c *Client) post(ctx context.Context, installID string, ev Event, at time.Time) error {
	props := map[string]any{
		"command":     ev.Command,
		"set_flags":   ev.Flags,
		"flag_count":  len(ev.Flags),
		"exit_code":   ev.ExitCode,
		"status":      "ok",
		"latency_ms":  ev.Duration.Milliseconds(),
		"arch":        ev.Arch,
		"agent":       ev.Agent,
		"interactive": ev.Interactive,
	}
	if ev.Flags == nil {
		props["set_flags"] = []string{}
	}
	if ev.Error != "" {
		props["status"] = "error"
		props["error_class"] = ev.Error
	}
	for k, v := range ev.Facts {
		props[k] = v
	}
	body, err := json.Marshal(map[string]any{
		"api_key": c.Key,
		"events": []map[string]any{{
			"device_id":        installID,
			"event_type":       "cli_command",
			"time":             at.UnixMilli(),
			"insert_id":        randomID(),
			"app_version":      ev.Version,
			"platform":         "CLI",
			"os_name":          ev.OS,
			"event_properties": props,
		}},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: c.Transport}).Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return refusal(resp.StatusCode)
	}
	return nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var errTurnedOff = errors.New("telemetry was turned off")

func (c *Client) load() state {
	var s state
	if raw, err := os.ReadFile(c.State); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// save writes the state through a temporary file, so two commands run at
// once never leave it half written.
func (c *Client) save(s state) error {
	if c.State == "" {
		return errors.New("no state file")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	dir := filepath.Dir(c.State)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".telemetry-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Turned off while this send was on its way: the state, with the
	// install's ID, was deleted, and is not brought back.
	if TurnedOff(c.State) {
		return errTurnedOff
	}
	return os.Rename(tmp.Name(), c.State)
}
