// Package config knows where Tenjin's services live for each environment.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Endpoints are the base URLs of one Tenjin environment.
type Endpoints struct {
	Name string
	// Issuer is the dashboard host, which is also the OAuth authorization
	// server.
	Issuer string
	// API is the Campaign Management API.
	API string
	// Track is where the SDK reports: Tenjin's tracking service.
	Track string
	// Resource is the audience tokens are issued for. Tenjin's authorization
	// server accepts one audience today, the MCP server's.
	Resource string
}

// production is where Tenjin's services live.
var production = Endpoints{
	Name:      "production",
	Issuer:    "https://dashboard.tenjin.com",
	API:       "https://api.tenjin.com/v2",
	Track:     "https://track.tenjin.com",
	Resource:  "https://mcp.tenjin.com",
}

// Resolve returns the endpoints the environment variables ask for: Tenjin's
// production services, unless an address is replaced.
//
// TENJIN_DASHBOARD_URL, TENJIN_API_URL, TENJIN_TRACK_URL and
// TENJIN_RESOURCE_URL each replace one address, for running against a local
// or a test copy of that service. With any of them set, the environment is
// called "custom", so the session kept for it is its own: a token issued by
// production is never sent to an address that was substituted.
func Resolve(getenv func(string) string) (Endpoints, error) {
	e := production
	for _, override := range []struct {
		variable string
		address  *string
	}{
		{"TENJIN_DASHBOARD_URL", &e.Issuer},
		{"TENJIN_API_URL", &e.API},
		{"TENJIN_TRACK_URL", &e.Track},
		{"TENJIN_RESOURCE_URL", &e.Resource},
	} {
		value := strings.TrimSuffix(getenv(override.variable), "/")
		if value == "" {
			continue
		}
		if u, err := url.Parse(value); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Endpoints{}, fmt.Errorf("%s must be an http or https address, not %q", override.variable, value)
		}
		*override.address = value
		e.Name = "custom"
	}
	return e, nil
}

// SDKKeysURL is the dashboard page that shows an app's SDK keys, in the
// "SDK Keys" card. The same address serves both layouts of the app page.
func (e Endpoints) SDKKeysURL(appID string) string {
	return e.Issuer + "/dashboard/apps/" + url.PathEscape(appID) + "/sdk"
}

// TestDevicesURL is the dashboard page where test devices are registered.
func (e Endpoints) TestDevicesURL() string {
	return e.Issuer + "/dashboard/debug_app_users"
}

// AccessTokensURL is the dashboard page that lists and revokes connections.
func (e Endpoints) AccessTokensURL() string {
	return e.Issuer + "/dashboard/access_tokens"
}

// LiveEventsURL is the dashboard's Live Event Tool.
func (e Endpoints) LiveEventsURL() string {
	return e.Issuer + "/dashboard/sdk_diagnostics"
}

// SignupURL is where a new user creates an account.
func (e Endpoints) SignupURL() string {
	return e.Issuer + "/signup"
}
