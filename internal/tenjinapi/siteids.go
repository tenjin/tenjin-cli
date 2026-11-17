package tenjinapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

// Site ID Optimization blocks traffic from sites (publisher apps) Tenjin
// should not attribute. It is a Connect RPC service on its own host
// (SiteIDsURL), spoken here in Connect's JSON form, with the API's token.

// SiteIDRule blocks one site ID: everywhere, or for one app, campaign or
// channel. Field names are the service's own.
type SiteIDRule struct {
	SiteID     string `json:"siteId"`
	AppID      string `json:"appId,omitempty"`
	CampaignID string `json:"campaignId,omitempty"`
	// Channel is the marketing channel's ID; nil blocks on every channel.
	Channel    *uint32 `json:"adNetworkId,omitempty"`
	CreateTime string  `json:"createTime,omitempty"`
}

const siteIDService = "/management.v2.SiteIDFilter/"

// CheckSiteIDs refuses what the service would: an empty site ID, one with a
// control character, or the same one twice.
func CheckSiteIDs(ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" {
			return clierr.New(clierr.Usage, "a site ID cannot be empty")
		}
		if strings.IndexFunc(id, unicode.IsControl) >= 0 {
			return clierr.New(clierr.Usage, "the site ID %q has a control character in it", id)
		}
		if seen[id] {
			return clierr.New(clierr.Usage, "the site ID %q is given twice", id)
		}
		seen[id] = true
	}
	return nil
}

// SiteIDRules returns the account's blocking rules, as the service answers:
// {"rules": [...]}, or {} when there are none.
func (c *Client) SiteIDRules(ctx context.Context) (Document, error) {
	return c.siteIDs(ctx, "Get", struct{}{})
}

// BlockSiteIDs adds rules. With replace, they replace every existing rule
// at once.
func (c *Client) BlockSiteIDs(ctx context.Context, rules []SiteIDRule, replace bool) (Document, error) {
	ids := make([]string, 0, len(rules))
	for _, r := range rules {
		ids = append(ids, r.SiteID)
	}
	if err := CheckSiteIDs(ids); err != nil {
		return nil, err
	}
	body := struct {
		Rules []SiteIDRule `json:"rules"`
		Clear bool         `json:"clear,omitempty"`
	}{rules, replace}
	return c.siteIDs(ctx, "Block", body)
}

// ReinstateSiteIDs removes every rule for these site IDs.
func (c *Client) ReinstateSiteIDs(ctx context.Context, siteIDs []string) (Document, error) {
	if err := CheckSiteIDs(siteIDs); err != nil {
		return nil, err
	}
	return c.siteIDs(ctx, "Reinstate", struct {
		SiteIDs []string `json:"siteIds"`
	}{siteIDs})
}

// ClearSiteIDs removes every rule.
func (c *Client) ClearSiteIDs(ctx context.Context) (Document, error) {
	return c.siteIDs(ctx, "Clear", struct{}{})
}

func (c *Client) siteIDs(ctx context.Context, method string, in any) (Document, error) {
	if c.SiteIDsURL == "" {
		return nil, clierr.New(clierr.Internal, "no address for the Site ID Optimization service")
	}
	token, err := c.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, clierr.Wrap(clierr.Internal, err, "could not write the request: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.SiteIDsURL, "/")+siteIDService+method, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentJSON)
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", c.UserAgent)
	httpClient := *c.HTTP
	httpClient.Transport = c.transport()
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "could not reach Tenjin's Site ID Optimization service: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "could not read the Site ID Optimization service's answer: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, connectError(resp, body)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return Document("{}"), nil
	}
	if !json.Valid(body) {
		return nil, clierr.New(clierr.Upstream, "the Site ID Optimization service answered with something that is not JSON (HTTP %d)", resp.StatusCode)
	}
	return Document(body), nil
}

// connectError classifies a Connect error: {"code": "...", "message": "..."}.
func connectError(resp *http.Response, body []byte) error {
	status := resp.StatusCode
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	message := firstNonEmpty(e.Message, e.Code, http.StatusText(status))
	switch e.Code {
	case "unauthenticated":
		return refusedToken(resp)
	case "permission_denied":
		return clierr.New(clierr.Permission, "your Tenjin role does not allow this: %s", message)
	case "not_found":
		return clierr.New(clierr.NotFound, "%s", message)
	case "invalid_argument", "failed_precondition", "out_of_range", "already_exists":
		return clierr.New(clierr.Refused, "Tenjin refused it: %s", message)
	case "resource_exhausted":
		return clierr.New(clierr.RateLimited, "Tenjin is rate limiting this token")
	}
	if status == http.StatusUnauthorized {
		return refusedToken(resp)
	}
	return clierr.New(clierr.Upstream, "the Site ID Optimization service failed (HTTP %d): %s", status, message)
}
