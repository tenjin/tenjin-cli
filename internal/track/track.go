// Package track talks to Tenjin's tracking service, where the SDK itself
// reports. The CLI uses it only to ask questions an SDK key is allowed to
// ask; it never sends events.
package track

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tenjin/tenjin-cli/internal/transport"
)

// ProductionURL is where the released SDKs report.
const ProductionURL = "https://track.tenjin.com"

// KeyStatus is what Tenjin says about an SDK key for one app.
type KeyStatus string

const (
	// KeyValid: the key is accepted for this bundle ID and platform.
	KeyValid KeyStatus = "valid"
	// KeyRejected: the key is unknown, or it belongs to a different app.
	KeyRejected KeyStatus = "rejected"
	// KeyNoApp: the key is known, but its organization has no app with this
	// bundle ID on this platform.
	KeyNoApp KeyStatus = "no_app"
	// KeyDisabled: the key exists and has been disabled.
	KeyDisabled KeyStatus = "disabled"
	// KeyUnclear: Tenjin answered in a way this client does not know.
	KeyUnclear KeyStatus = "unclear"
)

// KeyCheck is the outcome of CheckKey.
type KeyCheck struct {
	Status KeyStatus
	// Detail is Tenjin's own status and message, for diagnostics.
	Detail string
}

// Client calls Tenjin's tracking service.
type Client struct {
	BaseURL   string
	UserAgent string
	HTTP      *http.Client
}

// New returns a client for baseURL with a short timeout: an answer comes in
// well under a second or not at all.
func New(baseURL, userAgent string) *Client {
	return &Client{
		BaseURL:   strings.TrimSuffix(baseURL, "/"),
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: 15 * time.Second},
	}
}

// CheckKey asks whether an SDK key is valid for an app, without sending an
// event: the request carries no device identifier, and Tenjin's answer says
// whether it accepts the key for the app.
func (c *Client) CheckKey(ctx context.Context, key, bundleID, platform string) (KeyCheck, error) {
	form := url.Values{"bundle_id": {bundleID}, "platform": {platform}}
	// It carries no device, so sending it again is harmless.
	req, err := http.NewRequestWithContext(transport.Retryable(ctx), http.MethodPost, c.BaseURL+"/v0/event", strings.NewReader(form.Encode()))
	if err != nil {
		return KeyCheck{}, err
	}
	req.SetBasicAuth(key, "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return KeyCheck{}, fmt.Errorf("could not reach Tenjin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	var body struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &body)
	detail := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if body.Message != "" {
		detail += " " + body.Message
	}

	status := KeyUnclear
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		status = KeyRejected
	case resp.StatusCode == http.StatusNotFound:
		status = KeyNoApp
	case resp.StatusCode == http.StatusAccepted && body.Message == "invalid device identifier":
		status = KeyValid
	case resp.StatusCode == http.StatusAccepted && body.Message == "not logged":
		status = KeyDisabled
	}
	return KeyCheck{Status: status, Detail: detail}, nil
}

// Attribution is what Tenjin has recorded about a device's install.
type Attribution struct {
	// Known is false when Tenjin has not processed an install for the device.
	Known        bool
	AdNetwork    string
	CampaignName string
}

// LookupDevice asks whether Tenjin has processed an install for a device,
// identified by the installation ID the SDK generates. It uses the same
// endpoint the SDK calls to read its own attribution.
func (c *Client) LookupDevice(ctx context.Context, key, bundleID, platform, installationID string) (Attribution, error) {
	query := url.Values{"bundle_id": {bundleID}, "platform": {platform}, "analytics_installation_id": {installationID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v0/user?"+query.Encode(), nil)
	if err != nil {
		return Attribution{}, err
	}
	req.SetBasicAuth(key, "")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Attribution{}, fmt.Errorf("could not reach Tenjin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	switch resp.StatusCode {
	case http.StatusOK:
		var body struct {
			AdNetwork    string `json:"ad_network"`
			CampaignName string `json:"campaign_name"`
		}
		// Anything but Tenjin's JSON, such as a captive portal's page,
		// says nothing about the install.
		if err := json.Unmarshal(raw, &body); err != nil {
			return Attribution{}, fmt.Errorf("Tenjin's answer could not be read: %s", truncate(string(raw), 80))
		}
		return Attribution{Known: true, AdNetwork: body.AdNetwork, CampaignName: body.CampaignName}, nil
	case http.StatusNotFound:
		// A 404 with an empty object is a device not processed yet; any
		// other 404 is a failed lookup.
		if strings.TrimSpace(string(raw)) == "{}" {
			return Attribution{}, nil
		}
	}
	var answer struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &answer) == nil && answer.Message != "" {
		return Attribution{}, fmt.Errorf("Tenjin answered HTTP %d %q", resp.StatusCode, answer.Message)
	}
	return Attribution{}, fmt.Errorf("Tenjin answered HTTP %d", resp.StatusCode)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
