package catalog

import "testing"

func TestTheCatalogsLoad(t *testing.T) {
	if n := len(Metrics()); n < 100 {
		t.Errorf("%d metrics", n)
	}
	if n := len(Macros()); n < 100 {
		t.Errorf("%d macros", n)
	}
}

func TestSearchMetrics(t *testing.T) {
	// A concrete N-day name finds its family first.
	if got := SearchMetrics("roi_7d", "", ""); len(got) == 0 || got[0].ID != "roi_Nd" {
		t.Errorf("roi_7d: %v", ids(got))
	}
	if got := SearchMetrics("spend", "", UserAcquisition); len(got) == 0 || got[0].ID != "spend" {
		t.Errorf("spend: %v", ids(got))
	}
	for _, m := range SearchMetrics("", "", SKAN) {
		if m.Report != SKAN {
			t.Errorf("%s is in %s", m.ID, m.Report)
		}
	}
	if got := SearchMetrics("zzqxj", "", ""); len(got) != 0 {
		t.Errorf("nonsense matched %v", ids(got))
	}
}

func TestSearchMacros(t *testing.T) {
	if got := SearchMacros("country", "", ""); len(got) == 0 || got[0].Macro != "country" {
		t.Errorf("country: %v", got)
	}
	for _, m := range SearchMacros("", "", "AppLovin") {
		if !containsFold(m.Providers, "applovin") {
			t.Errorf("%s does not list AppLovin", m.Macro)
		}
	}
}

func ids(ms []Metric) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}
