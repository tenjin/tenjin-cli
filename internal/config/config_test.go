package config

import (
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func TestResolve(t *testing.T) {
	e, err := Resolve(env(nil))
	if err != nil || e.Name != "production" || e.API != "https://api.tenjin.com/v2" || e.Collector != "https://track.tenjin.com" {
		t.Errorf("default: %+v, %v", e, err)
	}
	if _, err := Resolve(env(map[string]string{"TENJIN_API_URL": "ftp://example.com"})); err == nil {
		t.Error("an address that is not http or https was accepted")
	}
}

// A substituted address must not be handed the session of the environment
// it replaces. The session store is keyed by the environment's name.
func TestAnOverriddenAddressGetsItsOwnSession(t *testing.T) {
	e, err := Resolve(env(map[string]string{"TENJIN_COLLECTOR_URL": "http://127.0.0.1:9999/"}))
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "custom" || e.Collector != "http://127.0.0.1:9999" {
		t.Errorf("endpoints = %+v", e)
	}
	// The addresses that were not overridden stay as they were.
	if e.API != "https://api.tenjin.com/v2" || e.Issuer != "https://dashboard.tenjin.com" {
		t.Errorf("endpoints = %+v", e)
	}
	if got := e.SDKKeysURL("abc"); got != "https://dashboard.tenjin.com/dashboard/apps/abc/sdk" {
		t.Errorf("SDK keys page = %q", got)
	}

	for _, bad := range []string{"track.example.com", "ftp://example.com", "http://"} {
		if _, err := Resolve(env(map[string]string{"TENJIN_API_URL": bad})); err == nil || !strings.Contains(err.Error(), "TENJIN_API_URL") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}
