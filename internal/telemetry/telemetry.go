// Package telemetry sends one anonymous event per command to Amplitude: which
// command ran, the names of the flags given, how it ended and how long it
// took, and a few facts about the project it ran on, such as its framework.
// Never a flag's value, a path, a bundle ID, a key or anything the project
// contains. A random ID, kept in a small state file, tells installs apart.
//
// It is off in builds without an Amplitude key (any build but a release), in
// CI, and with TENJIN_NO_TELEMETRY or DO_NOT_TRACK set.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// apiKey is Amplitude's key for the CLI's project, stamped by the linker
// from the release's secrets. Empty in every other build, which then sends
// nothing.
var apiKey string

// Endpoint is Amplitude's HTTP API.
const Endpoint = "https://api2.amplitude.com/2/httpapi"

// retryAfter is how long a failed send keeps the next ones from trying, so
// being offline costs one short wait an hour, not one per command.
const retryAfter = time.Hour

// Off reports whether the person or the machine asked for no telemetry.
func Off(getenv func(string) string) bool {
	for _, name := range []string{"TENJIN_NO_TELEMETRY", "DO_NOT_TRACK", "CI"} {
		if v := getenv(name); v != "" && v != "0" && v != "false" {
			return true
		}
	}
	return false
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
	// Command is the command's path without "tenjin", such as "apps ensure".
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

// Send sends ev. It never fails: telemetry must not get in the way of the
// command.
func (c *Client) Send(ctx context.Context, ev Event) {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	s := c.load()
	if s.InstallID == "" {
		s.InstallID = randomID()
		c.save(s)
	}
	if since := now().Sub(s.FailedAt); since >= 0 && since < retryAfter {
		return
	}
	if err := c.post(ctx, s.InstallID, ev, now()); err != nil {
		s.FailedAt = now()
		c.save(s)
	}
}

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
		return errors.New(resp.Status)
	}
	return nil
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *Client) load() state {
	var s state
	if raw, err := os.ReadFile(c.State); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// save writes the state through a temporary file, so two commands run at
// once never leave it half written.
func (c *Client) save(s state) {
	raw, err := json.Marshal(s)
	if err != nil || c.State == "" {
		return
	}
	dir := filepath.Dir(c.State)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".telemetry-*")
	if err != nil {
		return
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), c.State)
}
