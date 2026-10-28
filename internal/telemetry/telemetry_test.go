package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type received struct {
	APIKey string           `json:"api_key"`
	Events []map[string]any `json:"events"`
}

func server(t *testing.T, status int) (*httptest.Server, *[]received) {
	t.Helper()
	var got []received
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body received
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body: %v", err)
		}
		got = append(got, body)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestAnEventIsTheCommandsShape(t *testing.T) {
	srv, got := server(t, http.StatusOK)
	c := &Client{Key: "k", Endpoint: srv.URL, State: filepath.Join(t.TempDir(), "telemetry.json")}
	c.Send(context.Background(), Event{
		Command: "tenjin doctor", Flags: []string{"offline"}, ExitCode: 9, Error: "checks_failed",
		Duration: 1500 * time.Millisecond, Version: "0.1.0", OS: "darwin", Arch: "arm64",
		Facts: map[string]any{"framework": "native"},
	})
	if len(*got) != 1 || (*got)[0].APIKey != "k" || len((*got)[0].Events) != 1 {
		t.Fatalf("received %+v", *got)
	}
	ev := (*got)[0].Events[0]
	props := ev["event_properties"].(map[string]any)
	if ev["event_type"] != "cli_command" || ev["app_version"] != "0.1.0" || ev["os_name"] != "darwin" || len(ev["device_id"].(string)) != 32 {
		t.Errorf("event = %v", ev)
	}
	if props["command"] != "tenjin doctor" || props["status"] != "error" || props["error_class"] != "checks_failed" ||
		props["exit_code"] != float64(9) || props["latency_ms"] != float64(1500) || props["framework"] != "native" {
		t.Errorf("properties = %v", props)
	}
}

// The install keeps its ID from one command to the next.
func TestTheInstallKeepsItsID(t *testing.T) {
	srv, got := server(t, http.StatusOK)
	c := &Client{Key: "k", Endpoint: srv.URL, State: filepath.Join(t.TempDir(), "telemetry.json")}
	c.Send(context.Background(), Event{Command: "tenjin"})
	c.Send(context.Background(), Event{Command: "tenjin version"})
	if len(*got) != 2 || (*got)[0].Events[0]["device_id"] != (*got)[1].Events[0]["device_id"] {
		t.Errorf("received %+v", *got)
	}
	if raw, err := os.ReadFile(c.State); err != nil || len(raw) == 0 {
		t.Errorf("state: %q, %v", raw, err)
	}
}

// A failed send keeps the next ones from trying for an hour, so being
// offline does not slow every command.
func TestAFailedSendWaitsAnHour(t *testing.T) {
	srv, got := server(t, http.StatusServiceUnavailable)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	c := &Client{Key: "k", Endpoint: srv.URL, State: filepath.Join(t.TempDir(), "telemetry.json"), Now: func() time.Time { return now }}
	c.Send(context.Background(), Event{})
	now = now.Add(59 * time.Minute)
	c.Send(context.Background(), Event{})
	if len(*got) != 1 {
		t.Errorf("sent %d times within the hour", len(*got))
	}
	now = now.Add(time.Minute)
	c.Send(context.Background(), Event{})
	if len(*got) != 2 {
		t.Errorf("sent %d times after the hour", len(*got))
	}
}

func TestOff(t *testing.T) {
	for vars, want := range map[string]bool{
		"":                      false,
		"DO_NOT_TRACK=1":        true,
		"TENJIN_NO_TELEMETRY=1": true,
		"CI=true":               true,
		"DO_NOT_TRACK=0":        false,
		"CI=false":              false,
	} {
		env := map[string]string{}
		if vars != "" {
			name, value, _ := strings.Cut(vars, "=")
			env[name] = value
		}
		if got := Off(func(n string) string { return env[n] }); got != want {
			t.Errorf("%q: Off = %v", vars, got)
		}
	}
}
