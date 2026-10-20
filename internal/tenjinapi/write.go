package tenjinapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

// Fields is a request body, by the API's own field names. Only the fields
// given are sent, so an update changes those and leaves the rest.
type Fields map[string]any

// Request is a write as it would be sent.
type Request struct {
	Method  string
	URL     string
	HasBody bool
}

var errPlanned = errors.New("planned, not sent")

type planner func(*http.Request) (*http.Response, error)

func (p planner) RoundTrip(r *http.Request) (*http.Response, error) { return p(r) }

type noToken struct{}

func (noToken) Token(context.Context) (string, error) { return "", nil }

// Plan runs write against a copy of c that sends nothing, and returns the
// first request it would have sent. Everything write checks before sending,
// such as its IDs, is checked; no sign-in is needed.
func (c *Client) Plan(write func(*Client) error) (*Request, error) {
	var planned *Request
	dry := *c
	dry.Tokens = noToken{}
	dry.HTTP = &http.Client{Transport: planner(func(r *http.Request) (*http.Response, error) {
		if planned == nil {
			planned = &Request{Method: r.Method, URL: r.URL.String(), HasBody: r.Body != nil && r.Body != http.NoBody}
		}
		return nil, errPlanned
	})}
	err := write(&dry)
	if planned != nil {
		return planned, nil
	}
	if err == nil {
		err = clierr.New(clierr.Internal, "the change would send nothing")
	}
	return nil, err
}

const contentJSON = "application/json"

// write sends a body and returns the API's answer. An empty answer, as a
// delete may give, is the empty document {}.
func (c *Client) write(ctx context.Context, f Fields, call func(cl clients, body io.Reader) (*http.Response, error)) (Document, error) {
	if f == nil {
		f = Fields{}
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return nil, clierr.Wrap(clierr.Internal, err, "could not write the request: %v", err)
	}
	return c.send(ctx, func(cl clients) (*http.Response, error) { return call(cl, bytes.NewReader(raw)) })
}

func (c *Client) send(ctx context.Context, call func(cl clients) (*http.Response, error)) (Document, error) {
	cl, err := c.clients(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := call(cl)
	if err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "could not reach the Tenjin API: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "could not read the Tenjin API's response: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, apiError(resp, raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return Document("{}"), nil
	}
	if !json.Valid(raw) {
		return nil, clierr.New(clierr.Upstream, "the Tenjin API returned something that is not JSON (HTTP %d)", resp.StatusCode)
	}
	return Document(raw), nil
}

// UpdateApp changes an app's fields and returns the app as the CLI shows it:
// the API's answer also holds the app's shared secrets.
func (c *Client) UpdateApp(ctx context.Context, id string, f Fields) (*App, error) {
	appID, err := ParseID(id, "app")
	if err != nil {
		return nil, err
	}
	doc, err := c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PatchAppsIdWithBody(ctx, appID, contentJSON, body)
	})
	if err != nil {
		return nil, err
	}
	var body struct {
		Data resource `json:"data"`
	}
	if err := json.Unmarshal(doc, &body); err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "the Tenjin API answered the change with something that is not an app")
	}
	app := body.Data.app()
	if app.ID == "" {
		app.ID = appID.String()
	}
	return &app, nil
}

// DeleteApp soft-deletes an app. Its answer is not kept: nothing of an app
// is shown but what App holds.
func (c *Client) DeleteApp(ctx context.Context, id string) error {
	app, err := ParseID(id, "app")
	if err != nil {
		return err
	}
	_, err = c.send(ctx, func(cl clients) (*http.Response, error) { return cl.cma.DeleteAppsId(ctx, app) })
	return err
}

// CreateCampaign creates a campaign: name, app_id, and channel_id or
// ad_account_id.
func (c *Client) CreateCampaign(ctx context.Context, f Fields) (Document, error) {
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PostCampaignsWithBody(ctx, contentJSON, body)
	})
}

// UpdateCampaign changes a campaign. Its attribution window is the only field
// the API lets change.
func (c *Client) UpdateCampaign(ctx context.Context, id string, f Fields) (Document, error) {
	campaign, err := ParseID(id, "campaign")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PatchCampaignsIdWithBody(ctx, campaign, contentJSON, body)
	})
}

// DeleteCampaign soft-deletes a campaign. The API refuses one that has
// started tracking installs.
func (c *Client) DeleteCampaign(ctx context.Context, id string) (Document, error) {
	campaign, err := ParseID(id, "campaign")
	if err != nil {
		return nil, err
	}
	return c.send(ctx, func(cl clients) (*http.Response, error) { return cl.cma.DeleteCampaignsId(ctx, campaign) })
}

// CreateAppCallback creates a callback on an app from a callback template.
func (c *Client) CreateAppCallback(ctx context.Context, app string, f Fields) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PostAppsAppIdCallbacksWithBody(ctx, id, contentJSON, body)
	})
}

// UpdateAppCallback changes one of an app's callbacks.
func (c *Client) UpdateAppCallback(ctx context.Context, app, callback string, f Fields) (Document, error) {
	appID, id, err := twoIDs(app, "app", callback, "callback")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PatchAppsAppIdCallbacksIdWithBody(ctx, appID, id, contentJSON, body)
	})
}

// CreateAppS2SCallback creates a server-to-server callback on an app.
func (c *Client) CreateAppS2SCallback(ctx context.Context, app string, f Fields) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PostAppsAppIdS2sCallbacksWithBody(ctx, id, contentJSON, body)
	})
}

// UpdateAppS2SCallback changes one of an app's server-to-server callbacks.
func (c *Client) UpdateAppS2SCallback(ctx context.Context, app, callback string, f Fields) (Document, error) {
	appID, id, err := twoIDs(app, "app", callback, "callback")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PatchAppsAppIdS2sCallbacksIdWithBody(ctx, appID, id, contentJSON, body)
	})
}

// CreateAppCallbackGroup creates a callback group on an app for one channel.
func (c *Client) CreateAppCallbackGroup(ctx context.Context, app string, channel int, f Fields) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PostAppsAppIdIntegrationsIntegrationIdCallbackGroupsWithBody(ctx, id, channel, contentJSON, body)
	})
}

// UpdateAppCallbackGroup changes one of an app's callback groups.
func (c *Client) UpdateAppCallbackGroup(ctx context.Context, app string, channel int, group string, f Fields) (Document, error) {
	appID, id, err := twoIDs(app, "app", group, "callback group")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PatchAppsAppIdIntegrationsIntegrationIdCallbackGroupsIdWithBody(ctx, appID, channel, id, contentJSON, body)
	})
}

// UpdateAppCallbackSettings changes an app's callback settings for one
// channel.
func (c *Client) UpdateAppCallbackSettings(ctx context.Context, app string, channel int, f Fields) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.cma.PatchAppsAppIdIntegrationsIntegrationIdCallbackSettingsWithBody(ctx, id, channel, contentJSON, body)
	})
}

// CreateSavedReport saves a report.
func (c *Client) CreateSavedReport(ctx context.Context, f Fields) (Document, error) {
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.rma.PostSavedReportsWithBody(ctx, contentJSON, body)
	})
}

// UpdateSavedReport changes a saved report.
func (c *Client) UpdateSavedReport(ctx context.Context, id string, f Fields) (Document, error) {
	report, err := ParseID(id, "saved report")
	if err != nil {
		return nil, err
	}
	return c.write(ctx, f, func(cl clients, body io.Reader) (*http.Response, error) {
		return cl.rma.PatchSavedReportsIdWithBody(ctx, report, contentJSON, body)
	})
}

// DeleteSavedReport deletes a saved report.
func (c *Client) DeleteSavedReport(ctx context.Context, id string) (Document, error) {
	report, err := ParseID(id, "saved report")
	if err != nil {
		return nil, err
	}
	return c.send(ctx, func(cl clients) (*http.Response, error) { return cl.rma.DeleteSavedReportsId(ctx, report) })
}
