// Package catalog holds what Tenjin documents but its API does not serve:
// the reporting metrics and the callback macros. They are the same catalogs
// the Tenjin MCP server searches, kept here as JSON.
package catalog

import (
	"embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

//go:embed *.json
var files embed.FS

// Reports a metric can belong to, as the reports commands name them.
const (
	UserAcquisition = "user_acquisition"
	AdMonetization  = "ad_monetization"
	SKAN            = "skan"
)

var reportFiles = []struct{ report, file string }{
	{UserAcquisition, "user_acquisition_metrics.json"},
	{AdMonetization, "ad_monetization_metrics.json"},
	{SKAN, "skan_metrics.json"},
}

// Metric is one reporting metric. An ID ending in _Nd stands for a family:
// roi_Nd is roi_1d, roi_7d, roi_30d and so on.
type Metric struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
	// Report is the report that serves it: user_acquisition (tenjin reports
	// spend), ad_monetization (ad-revenue) or skan (skadnetwork, skan).
	Report string `json:"report"`
}

// Macro is one callback macro. In a callback URL it is written in double
// braces: country is {{country}}.
type Macro struct {
	Macro       string   `json:"macro"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Providers   []string `json:"providers,omitempty"`
}

// Metrics returns every metric, in the catalogs' order.
func Metrics() []Metric {
	var out []Metric
	for _, f := range reportFiles {
		raw, err := files.ReadFile(f.file)
		if err != nil {
			panic(err)
		}
		// The catalogs are objects keyed by metric ID; their order is kept.
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		if _, err := dec.Token(); err != nil {
			panic(err)
		}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				panic(err)
			}
			var m Metric
			if err := dec.Decode(&m); err != nil {
				panic(err)
			}
			m.ID, m.Report = key.(string), f.report
			out = append(out, m)
		}
	}
	return out
}

// Macros returns every callback macro, in the catalog's order.
func Macros() []Macro {
	raw, err := files.ReadFile("callback_macros.json")
	if err != nil {
		panic(err)
	}
	var out []Macro
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	return out
}

// SearchMetrics returns the metrics in category and report (empty for any),
// matching query, best match first. With no query they come in the catalogs'
// order.
func SearchMetrics(query, category, report string) []Metric {
	var keep []Metric
	var texts [][3]string
	for _, m := range Metrics() {
		if (report == "" || strings.EqualFold(m.Report, report)) && (category == "" || containsFold(m.Categories, category)) {
			keep = append(keep, m)
			texts = append(texts, [3]string{m.ID, m.Name, m.Description})
		}
	}
	order := rank(query, texts)
	out := make([]Metric, 0, len(order))
	for _, i := range order {
		out = append(out, keep[i])
	}
	return out
}

// SearchMacros returns the macros in category with provider (empty for any),
// matching query, best match first.
func SearchMacros(query, category, provider string) []Macro {
	var keep []Macro
	var texts [][3]string
	for _, m := range Macros() {
		if (category == "" || strings.EqualFold(m.Category, category)) && (provider == "" || containsFold(m.Providers, provider)) {
			keep = append(keep, m)
			texts = append(texts, [3]string{m.Macro, "", m.Description})
		}
	}
	order := rank(query, texts)
	out := make([]Macro, 0, len(order))
	for _, i := range order {
		out = append(out, keep[i])
	}
	return out
}

var nDay = regexp.MustCompile(`^(.+_)\d+d$`)

// rank orders entries by how well each matches the query's words: in its ID
// (an exact ID first; roi_7d finds roi_Nd), its name, then its description.
// Entries matching no word are left out. Ties keep the catalog's order.
func rank(query string, texts [][3]string) []int {
	words := strings.Fields(strings.ToLower(query))
	type scored struct{ index, score int }
	var hits []scored
	for i, t := range texts {
		id, name, description := strings.ToLower(t[0]), strings.ToLower(t[1]), strings.ToLower(t[2])
		score := 0
		for _, w := range words {
			family := nDay.ReplaceAllString(w, "${1}nd")
			switch {
			case id == w || id == family:
				score += 100
			case strings.HasPrefix(id, w):
				score += 40
			case strings.Contains(id, w):
				score += 25
			case strings.Contains(name, w):
				score += 15
			case strings.Contains(description, w):
				score += 5
			default:
				continue
			}
		}
		if len(words) == 0 || score > 0 {
			hits = append(hits, scored{i, score})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]int, len(hits))
	for i, h := range hits {
		out[i] = h.index
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
