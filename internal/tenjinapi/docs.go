package tenjinapi

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

// DocResult is one page of Tenjin's documentation.
type DocResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Excerpt string `json:"excerpt,omitempty"`
}

// docTypes are the kinds of page that are documentation; the search also
// knows others.
var docTypes = map[string]bool{"docs": true, "betterdocs_faq": true}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// SearchDocs searches Tenjin's public documentation at base, ranked by the
// site. limit caps the documentation pages and the FAQs, each. No token is
// sent: the site is public.
func SearchDocs(ctx context.Context, httpClient *http.Client, base, userAgent, query string, limit int) ([]DocResult, error) {
	params := url.Values{"s": {query}, "per_page": {strconv.Itoa(limit)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(base, "/")+"/wp-json/betterdocs/v1/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", contentJSON)
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "could not reach Tenjin's documentation: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "could not read Tenjin's documentation search: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, clierr.New(clierr.Upstream, "Tenjin's documentation search failed (HTTP %d)", resp.StatusCode)
	}
	var raw []struct {
		Title     string `json:"title"`
		Permalink string `json:"permalink"`
		PostType  string `json:"post_type"`
		Content   string `json:"content"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "Tenjin's documentation search answered with something unexpected")
	}
	results := []DocResult{}
	for _, r := range raw {
		if !docTypes[r.PostType] {
			continue
		}
		text := html.UnescapeString(htmlTag.ReplaceAllString(r.Content, " "))
		results = append(results, DocResult{Title: html.UnescapeString(r.Title), URL: r.Permalink, Excerpt: strings.Join(strings.Fields(text), " ")})
	}
	return results, nil
}
