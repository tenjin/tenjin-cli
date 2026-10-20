package tenjinapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi/cma"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi/rma"
)

// Document is one of the API's answers as it sent it: a JSON:API document.
// The data commands print it as it is, so the API's documentation describes
// their JSON.
type Document = json.RawMessage

// Paging picks the pages of a list. Zero values leave the API's defaults
// (page 1, 25 a page); All reads every page and merges their data.
type Paging struct {
	Page    int
	PerPage int
	All     bool
}

// fetch makes one call and returns the document it answered with.
func (c *Client) fetch(ctx context.Context, call func(clients) (*http.Response, error)) (Document, error) {
	cl, err := c.clients(ctx)
	if err != nil {
		return nil, err
	}
	var doc json.RawMessage
	if err := receive(call(cl))(&doc); err != nil { //nolint:bodyclose // receive closes it
		return nil, err
	}
	return doc, nil
}

// maxPages caps how many pages one list reads: a server that kept offering
// another page would otherwise be read for ever.
const maxPages = 1000

// list fetches the pages p asks for. With All, the result is the first page's
// document with every page's data in it, and without its links, which named
// pages.
func (c *Client) list(ctx context.Context, p Paging, call func(cl clients, page, perPage *int) (*http.Response, error)) (Document, error) {
	if !p.All {
		return c.fetch(ctx, func(cl clients) (*http.Response, error) {
			return call(cl, positive(p.Page), positive(p.PerPage))
		})
	}
	var first map[string]json.RawMessage
	data := []json.RawMessage{}
	for page := 1; page <= maxPages; page++ {
		doc, err := c.fetch(ctx, func(cl clients) (*http.Response, error) {
			return call(cl, &page, new(maxPageSize))
		})
		if err != nil {
			return nil, err
		}
		var body struct {
			Data  []json.RawMessage `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
			Meta struct {
				Count *int `json:"count"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(doc, &body); err != nil {
			return nil, clierr.Wrap(clierr.Upstream, err, "the Tenjin API returned a list that is not one")
		}
		if first == nil {
			if err := json.Unmarshal(doc, &first); err != nil {
				return nil, clierr.Wrap(clierr.Upstream, err, "the Tenjin API returned a list that is not one")
			}
		}
		data = append(data, body.Data...)
		// A list says it has more with a next link. Reports may not: a full
		// page short of meta.count, or with no count, is followed too.
		full := len(body.Data) >= maxPageSize && (body.Meta.Count == nil || len(data) < *body.Meta.Count)
		more := len(body.Data) > 0 && (body.Links.Next != "" || full)
		if !more {
			return merged(first, data)
		}
	}
	return nil, clierr.New(clierr.Upstream, "the Tenjin API offered more than %d pages of %d; nothing is shown rather than part of the list", maxPages, maxPageSize).
		WithHint("Narrow it with the command's flags, or read one page at a time with --page.")
}

// merged is a list's first page with every page's data in it. Its links
// named single pages, so they are left out.
func merged(first map[string]json.RawMessage, data []json.RawMessage) (Document, error) {
	out := make(map[string]json.RawMessage, len(first))
	for k, v := range first {
		if k != "links" {
			out[k] = v
		}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "the Tenjin API returned a list that is not one")
	}
	out["data"] = raw
	return json.Marshal(out)
}

func positive(n int) *int {
	if n <= 0 {
		return nil
	}
	return &n
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ParseID reads a resource ID: a UUID. what names it in the error.
func ParseID(s, what string) (openapi_types.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return id, clierr.New(clierr.Usage, "%s %q is not an ID: Tenjin IDs look like 3f2a9c1e-5b7d-4e8a-9c21-7d4e5f6a8b90", what, s)
	}
	return id, nil
}

// CampaignQuery narrows a list of campaigns. Zero values do not narrow it.
type CampaignQuery struct {
	App      string
	Channel  int
	Platform string
	Query    string
}

// Campaigns lists campaigns.
func (c *Client) Campaigns(ctx context.Context, q CampaignQuery, p Paging) (Document, error) {
	params := cma.GetCampaignsParams{ChannelId: positive(q.Channel), Query: optional(q.Query)}
	if q.App != "" {
		app, err := ParseID(q.App, "app")
		if err != nil {
			return nil, err
		}
		params.AppId = &app
	}
	if q.Platform != "" {
		platform := cma.GetCampaignsParamsPlatform(q.Platform)
		params.Platform = &platform
	}
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		params.Page, params.PerPage = page, perPage
		return cl.cma.GetCampaigns(ctx, &params)
	})
}

// Campaign gets one campaign.
func (c *Client) Campaign(ctx context.Context, id string) (Document, error) {
	campaign, err := ParseID(id, "campaign")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.cma.GetCampaignsId(ctx, campaign) })
}

// Channels lists marketing channels. query matches their names.
func (c *Client) Channels(ctx context.Context, query string, p Paging) (Document, error) {
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		return cl.cma.GetChannels(ctx, &cma.GetChannelsParams{Page: page, PerPage: perPage, Query: optional(query)})
	})
}

// Channel gets one marketing channel.
func (c *Client) Channel(ctx context.Context, id int) (Document, error) {
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.cma.GetChannelsId(ctx, id) })
}

// CallbackGroupTemplates lists a marketing channel's callback group templates.
func (c *Client) CallbackGroupTemplates(ctx context.Context, channel int) (Document, error) {
	return c.fetch(ctx, func(cl clients) (*http.Response, error) {
		return cl.cma.GetIntegrationsIntegrationIdCallbackGroupTemplates(ctx, channel)
	})
}

// AdAccounts lists ad accounts, of one channel when channel is not 0.
func (c *Client) AdAccounts(ctx context.Context, channel int, query string, p Paging) (Document, error) {
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		return cl.cma.GetAdAccounts(ctx, &cma.GetAdAccountsParams{Page: page, PerPage: perPage, ChannelId: positive(channel), Query: optional(query)})
	})
}

// AdAccount gets one ad account.
func (c *Client) AdAccount(ctx context.Context, id string) (Document, error) {
	account, err := ParseID(id, "ad account")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.cma.GetAdAccountsId(ctx, account) })
}

// SKAdNetworks lists the SKAdNetwork IDs Tenjin knows.
func (c *Client) SKAdNetworks(ctx context.Context) (Document, error) {
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.cma.GetSkAdNetworks(ctx) })
}

// SKAdNetwork gets one SKAdNetwork ID, such as 4fzdc2evr5.skadnetwork.
func (c *Client) SKAdNetwork(ctx context.Context, id string) (Document, error) {
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.cma.GetSkAdNetworksId(ctx, id) })
}

// AppEvents lists the events an app has sent.
func (c *Client) AppEvents(ctx context.Context, app string, p Paging) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		return cl.cma.GetAppsAppIdEvents(ctx, id, &cma.GetAppsAppIdEventsParams{Page: page, PerPage: perPage})
	})
}

// AppCallbacks lists an app's callbacks, of one channel when channel is not 0.
func (c *Client) AppCallbacks(ctx context.Context, app string, channel int, p Paging) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		return cl.cma.GetAppsAppIdCallbacks(ctx, id, &cma.GetAppsAppIdCallbacksParams{Page: page, PerPage: perPage, ChannelId: positive(channel)})
	})
}

// AppCallback gets one of an app's callbacks.
func (c *Client) AppCallback(ctx context.Context, app, callback string) (Document, error) {
	appID, id, err := twoIDs(app, "app", callback, "callback")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.cma.GetAppsAppIdCallbacksId(ctx, appID, id) })
}

// AppS2SCallbacks lists an app's server-to-server callbacks.
func (c *Client) AppS2SCallbacks(ctx context.Context, app string, p Paging) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		return cl.cma.GetAppsAppIdS2sCallbacks(ctx, id, &cma.GetAppsAppIdS2sCallbacksParams{Page: page, PerPage: perPage})
	})
}

// AppS2SCallback gets one of an app's server-to-server callbacks.
func (c *Client) AppS2SCallback(ctx context.Context, app, callback string) (Document, error) {
	appID, id, err := twoIDs(app, "app", callback, "callback")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.cma.GetAppsAppIdS2sCallbacksId(ctx, appID, id) })
}

// AppCallbackTemplates lists the callback templates an app can use, of one
// channel when channel is not 0.
func (c *Client) AppCallbackTemplates(ctx context.Context, app string, channel int, p Paging) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		return cl.cma.GetAppsAppIdCallbackTemplates(ctx, id, &cma.GetAppsAppIdCallbackTemplatesParams{Page: page, PerPage: perPage, ChannelId: positive(channel)})
	})
}

// AppCallbackTemplate gets one callback template.
func (c *Client) AppCallbackTemplate(ctx context.Context, app, template string) (Document, error) {
	appID, id, err := twoIDs(app, "app", template, "callback template")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) {
		return cl.cma.GetAppsAppIdCallbackTemplatesId(ctx, appID, id)
	})
}

// AppCallbackGroups lists an app's callback groups for one channel.
func (c *Client) AppCallbackGroups(ctx context.Context, app string, channel int) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) {
		return cl.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackGroups(ctx, id, channel)
	})
}

// AppCallbackGroup gets one of an app's callback groups.
func (c *Client) AppCallbackGroup(ctx context.Context, app string, channel int, group string) (Document, error) {
	appID, id, err := twoIDs(app, "app", group, "callback group")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) {
		return cl.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackGroupsId(ctx, appID, channel, id)
	})
}

// AppCallbackSettings gets an app's callback settings for one channel.
func (c *Client) AppCallbackSettings(ctx context.Context, app string, channel int) (Document, error) {
	id, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) {
		return cl.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackSettings(ctx, id, channel)
	})
}

func twoIDs(a, aWhat, b, bWhat string) (openapi_types.UUID, openapi_types.UUID, error) {
	first, err := ParseID(a, aWhat)
	if err != nil {
		return first, first, err
	}
	second, err := ParseID(b, bWhat)
	return first, second, err
}

// Report is one of the reporting API's reports.
type Report string

const (
	ReportSpend       Report = "spend"
	ReportAdRevenue   Report = "ad-revenue"
	ReportSKAdNetwork Report = "skadnetwork"
	ReportSKAN        Report = "skan"
)

// ReportQuery is what a report covers. From and To are dates, 2006-01-02;
// the rest are the API's own values, passed as they are, empty for its
// defaults. Apps and Channels are comma-separated IDs; Metrics are
// comma-separated names.
type ReportQuery struct {
	From, To    string
	Granularity string
	GroupBy     string
	Apps        string
	Channels    string
	Metrics     string
}

// Report pulls one report.
func (c *Client) Report(ctx context.Context, report Report, q ReportQuery, p Paging) (Document, error) {
	from, err := parseDate(q.From, "--from")
	if err != nil {
		return nil, err
	}
	to, err := parseDate(q.To, "--to")
	if err != nil {
		return nil, err
	}
	apps, channels, metrics := optional(q.Apps), optional(q.Channels), optional(q.Metrics)
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		switch report {
		case ReportSpend:
			params := rma.GetReportsSpendParams{Page: page, PerPage: perPage, StartDate: from, EndDate: to, AppIds: apps, ChannelIds: channels, SpendMetricsParam: metrics}
			if q.Granularity != "" {
				params.Granularity = new(rma.GetReportsSpendParamsGranularity(q.Granularity))
			}
			if q.GroupBy != "" {
				params.GroupBy = new(rma.GetReportsSpendParamsGroupBy(q.GroupBy))
			}
			return cl.rma.GetReportsSpend(ctx, &params)
		case ReportAdRevenue:
			params := rma.GetReportsAdRevenueParams{Page: page, PerPage: perPage, StartDate: from, EndDate: to, AppIds: apps, ChannelIds: channels, AdRevenueMetricsParam: metrics}
			if q.Granularity != "" {
				params.Granularity = new(rma.GetReportsAdRevenueParamsGranularity(q.Granularity))
			}
			if q.GroupBy != "" {
				params.GroupBy = new(rma.GetReportsAdRevenueParamsGroupBy(q.GroupBy))
			}
			return cl.rma.GetReportsAdRevenue(ctx, &params)
		case ReportSKAdNetwork:
			params := rma.GetReportsSkAdNetworkParams{Page: page, PerPage: perPage, StartDate: from, EndDate: to, AppIds: apps, ChannelIds: channels, Metrics: metrics}
			if q.Granularity != "" {
				params.GranularityDailyOnly = new(rma.GetReportsSkAdNetworkParamsGranularity(q.Granularity))
			}
			if q.GroupBy != "" {
				params.GroupBy = new(rma.GetReportsSkAdNetworkParamsGroupBy(q.GroupBy))
			}
			return cl.rma.GetReportsSkAdNetwork(ctx, &params)
		default:
			params := rma.GetReportsSkanAppleParams{Page: page, PerPage: perPage, StartDate: from, EndDate: to, AppIds: apps, ChannelIds: channels, Metrics: metrics}
			if q.Granularity != "" {
				params.GranularityDailyOnly = new(rma.GetReportsSkanAppleParamsGranularity(q.Granularity))
			}
			if q.GroupBy != "" {
				params.GroupBy = new(rma.GetReportsSkanAppleParamsGroupBy(q.GroupBy))
			}
			return cl.rma.GetReportsSkanApple(ctx, &params)
		}
	})
}

func parseDate(s, flag string) (openapi_types.Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return openapi_types.Date{}, clierr.New(clierr.Usage, "%s needs a date such as 2026-10-01, not %q", flag, s)
	}
	return openapi_types.Date{Time: t}, nil
}

// SavedReports lists saved reports, of one type (user_acquisition or
// ad_revenue) when reportType is not empty.
func (c *Client) SavedReports(ctx context.Context, reportType string, p Paging) (Document, error) {
	return c.list(ctx, p, func(cl clients, page, perPage *int) (*http.Response, error) {
		params := rma.GetSavedReportsParams{Page: page, PerPage: perPage}
		if reportType != "" {
			params.ReportType = new(rma.GetSavedReportsParamsReportType(reportType))
		}
		return cl.rma.GetSavedReports(ctx, &params)
	})
}

// SavedReport gets one saved report: its settings, not its data.
func (c *Client) SavedReport(ctx context.Context, id string) (Document, error) {
	report, err := ParseID(id, "saved report")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.rma.GetSavedReportsId(ctx, report) })
}

// RunSavedReport pulls the data of a saved report.
func (c *Client) RunSavedReport(ctx context.Context, id string) (Document, error) {
	report, err := ParseID(id, "saved report")
	if err != nil {
		return nil, err
	}
	return c.fetch(ctx, func(cl clients) (*http.Response, error) { return cl.rma.GetReportsId(ctx, report) })
}

// ChannelID reads a marketing channel's ID: a number.
func ChannelID(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, clierr.New(clierr.Usage, "channel %q is not an ID: channel IDs are numbers, from 'tenjin channels list'", s)
	}
	return n, nil
}
