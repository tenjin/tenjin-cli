package track

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckKeyMapsTenjinsAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   KeyStatus
	}{
		{"good pairing", http.StatusAccepted, `{"code":202,"message":"invalid device identifier"}`, KeyValid},
		{"unknown key", http.StatusUnauthorized, `{"code":401,"message":"unauthorized"}`, KeyRejected},
		{"no such app", http.StatusNotFound, `{"code":404,"message":"no such app"}`, KeyNoApp},
		{"disabled key", http.StatusAccepted, `{"code":202,"message":"not logged"}`, KeyDisabled},
		{"filtered by version", http.StatusAccepted, `{"code":202,"message":"ignored"}`, KeyUnclear},
		{"server error", http.StatusBadGateway, `upstream`, KeyUnclear},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, err := New(srv.URL, "test").CheckKey(context.Background(), "KEY", "com.example.app", "android")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.want {
				t.Errorf("status = %q (%s), want %q", got.Status, got.Detail, tc.want)
			}
		})
	}
}

// The check carries the key, the app and no device identifier, so it sends
// no event.
func TestCheckKeySendsTheKeyAndNoDeviceIdentifier(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	if _, err := New(srv.URL+"/", "tenjin-cli/test").CheckKey(context.Background(), "MYKEY", "com.example.app", "ios"); err != nil {
		t.Fatal(err)
	}
	if got.Method != http.MethodPost || got.URL.Path != "/v0/event" {
		t.Errorf("request = %s %s", got.Method, got.URL.Path)
	}
	if user, _, ok := got.BasicAuth(); !ok || user != "MYKEY" {
		t.Errorf("basic auth user = %q", user)
	}
	if got.PostForm.Get("bundle_id") != "com.example.app" || got.PostForm.Get("platform") != "ios" {
		t.Errorf("form = %v", got.PostForm)
	}
	for _, field := range []string{"advertising_id", "developer_device_id", "analytics_installation_id"} {
		if got.PostForm.Has(field) {
			t.Errorf("request carries %s; it must not identify a device", field)
		}
	}
	if got.Header.Get("User-Agent") != "tenjin-cli/test" {
		t.Errorf("user agent = %q", got.Header.Get("User-Agent"))
	}
}

func TestCheckKeyReportsUnreachableTracking(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if _, err := New(srv.URL, "test").CheckKey(context.Background(), "KEY", "com.example.app", "android"); err == nil {
		t.Error("expected an error")
	}
}

func TestLookupDevice(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		known   bool
		wantErr bool
	}{
		{"processed", http.StatusOK, `{"advertising_id":"","analytics_installation_id":"install1","developer_device_id":"","ad_network":"organic","campaign_name":"Organic"}`, true, false},
		// JSON that is not an install's attribution says nothing about one.
		{"a 200 without an install", http.StatusOK, `{"code":200,"success":true}`, false, true},
		{"an empty 200", http.StatusOK, `{}`, false, true},
		{"not processed yet", http.StatusNotFound, `{}`, false, false},
		// The same status with a different body means the app is unknown,
		// which is a failed lookup and not an answer about the device.
		{"unknown app", http.StatusNotFound, `{"code":404,"message":"no such app"}`, false, true},
		{"bad key", http.StatusUnauthorized, `{"code":401,"message":"unauthorized"}`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got *http.Request
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			attribution, err := New(srv.URL, "test").LookupDevice(context.Background(), "MYKEY", "com.example.app", "android", "install-1")
			if (err != nil) != tc.wantErr || attribution.Known != tc.known {
				t.Fatalf("attribution = %+v, err = %v", attribution, err)
			}
			if tc.known && attribution.CampaignName != "Organic" {
				t.Errorf("attribution = %+v", attribution)
			}
			q := got.URL.Query()
			if user, _, _ := got.BasicAuth(); user != "MYKEY" || got.URL.Path != "/v0/user" ||
				q.Get("analytics_installation_id") != "install-1" || q.Get("bundle_id") != "com.example.app" || q.Get("platform") != "android" {
				t.Errorf("request = %s %s (user %q)", got.Method, got.URL, user)
			}
		})
	}
}

// A 200 that is not Tenjin's JSON, such as a captive portal's page,
// is not taken for a processed install.
func TestAnUnreadableAnswerIsNotAnInstall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>Sign in to the Wi-Fi</html>"))
	}))
	defer srv.Close()
	got, err := New(srv.URL, "test").LookupDevice(context.Background(), "KEY", "com.example", "android", "install-1")
	if err == nil || got.Known {
		t.Errorf("got %+v, %v", got, err)
	}
}
