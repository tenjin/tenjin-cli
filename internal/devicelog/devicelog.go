// Package devicelog reads what the Tenjin SDK writes to a device's log: the
// requests it sent and what Tenjin answered. That is the only place
// the reason for a refused request is visible to a developer.
package devicelog

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

// The Android SDK logs each request as three lines once the response is in:
//
//	D/HttpConnection: Tenjin request URL: https://track.tenjin.com/v0/event
//	D/HttpConnection: Tenjin::connect params: {key=value, ...}
//	D/HttpConnection: Tenjin::connect response: {"code":200,"success":true}
//
// The label says "connect" for every kind of request, so the URL is what
// tells them apart. Lines are matched on these messages, not on the log tag,
// because "HttpConnection" is a name other libraries use too.
const (
	markURL      = "Tenjin request URL: "
	markParams   = "Tenjin::connect params: "
	markResponse = "Tenjin::connect response: "
	// markDeduped is logged when a connect is skipped because one was sent
	// less than 30 seconds earlier, a gap that persists across launches.
	markDeduped = "Connect deduped by persisted timestamp"
)

// The iOS SDK logs a request in four lines, the second and fourth only with
// TenjinSDK.debugLogs() on:
//
//	[Tenjin] - LOG: Sending request - https://track.tenjin.com/v0/event
//	[Tenjin] - DEBUG: request body key=value&key=value
//	[Tenjin] - LOG: Got http response 200
//	[Tenjin] - DEBUG: Response body {"code":200,"success":true}
//
// Every line carries the "[Tenjin]" tag, wherever the log comes from.
const (
	iosTag          = "[Tenjin]"
	iosMarkURL      = "Sending request - "
	iosMarkParams   = "request body "
	iosMarkStatus   = "Got http response "
	iosMarkResponse = "Response body "
	// iosMarkDeduped is the iOS form of the 30-second rule, in SDK
	// versions that have it.
	iosMarkDeduped = "ignoring duplicate ping"
)

// sdkTags are the log tags only the Tenjin SDK uses.
var sdkTags = []string{"TenjinSDK", "TenjinStartup", "TenjinReflection"}

// Request is one call the SDK made to Tenjin.
type Request struct {
	URL string
	// Endpoint is the last part of the URL path: "event", "purchase", ...
	Endpoint string
	Params   map[string]string
	// Code and Message come from the response body. Code is 0 when the body
	// could not be read as Tenjin's JSON. On iOS, where the body is
	// logged only with debug logs on, Code is the HTTP status otherwise.
	Code     int
	Message  string
	Response string
	// Line is where the request starts in the log, counting from 1.
	Line int
	// Time is when the device logged it, as the log writes it, or "".
	Time string
	// Unread is true for an iOS request whose body was not logged, as
	// without debug logs: what it was cannot be told.
	Unread bool
}

// IsConnect reports whether this is the launch call that registers an
// install or session, as opposed to a custom event sent to the same URL.
func (r Request) IsConnect() bool {
	_, named := r.Params["event"]
	return r.Endpoint == "event" && !named && !r.Unread
}

// Accepted reports whether Tenjin took the request.
func (r Request) Accepted() bool { return r.Code == 200 }

// Log is what a device log says about the SDK.
type Log struct {
	// Platform is the SDK that wrote the log: "android" or "ios", or ""
	// when the log has nothing from the SDK.
	Platform string
	// SawSDK is true when the SDK wrote anything at all.
	SawSDK   bool
	Requests []Request
	// Deduped counts connects the SDK skipped under its 30-second rule, and
	// DedupedLine is where the last of them is in the log.
	Deduped     int
	DedupedLine int
}

// logTime matches the time at the start of a log line: logcat's "-v time"
// (10-02 15:07:34.121) and the unified log's (2026-10-06 14:22:01.123).
var logTime = regexp.MustCompile(`^\s*((?:\d{4}-)?\d{2}-\d{2}\s+\d{2}:\d{2}:\d{2}(?:\.\d+)?)`)

func lineTime(line string) string {
	if m := logTime.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// After returns the part of a log that comes after its last line equal to
// mark: what a device logged since mark was its newest line. When mark is
// empty or no longer in the log, the whole log is new.
func After(raw []byte, mark string) []byte {
	if mark == "" {
		return raw
	}
	lines := strings.Split(string(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimRight(lines[i], "\r") == mark {
			return []byte(strings.Join(lines[i+1:], "\n"))
		}
	}
	return raw
}

// LastLine is the newest non-empty line of a log, to pass to After later.
func LastLine(raw []byte) string {
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimRight(lines[i], "\r"); strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// LastConnect returns the most recent connect request, or nil.
func (l *Log) LastConnect() *Request {
	for i := len(l.Requests) - 1; i >= 0; i-- {
		if l.Requests[i].IsConnect() {
			return &l.Requests[i]
		}
	}
	return nil
}

// Parse reads a device log in any logcat format.
func Parse(r io.Reader) (*Log, error) {
	log := &Log{}
	var pending *Request
	flush := func() {
		if pending != nil {
			log.Requests = append(log.Requests, *pending)
			pending = nil
		}
	}

	lines, err := readLines(r)
	if err != nil {
		return nil, err
	}
	for i, line := range lines {
		n := i + 1
		if strings.Contains(line, iosTag) {
			log.SawSDK, log.Platform = true, "ios"
			pending = parseIOSLine(log, pending, line, n)
			continue
		}
		switch {
		case strings.Contains(line, markURL):
			flush()
			url := strings.TrimSpace(after(line, markURL))
			pending = &Request{URL: url, Endpoint: endpoint(url), Params: map[string]string{}, Line: n, Time: lineTime(line)}
			log.SawSDK = true
		case strings.Contains(line, markParams):
			if pending == nil {
				pending = &Request{Params: map[string]string{}, Line: n}
			}
			pending.Params = parseParams(after(line, markParams))
			log.SawSDK = true
		case strings.Contains(line, markResponse):
			if pending == nil {
				pending = &Request{Params: map[string]string{}, Line: n}
			}
			pending.Response = strings.TrimSpace(after(line, markResponse))
			pending.Code, pending.Message = parseResponse(pending.Response)
			log.SawSDK = true
			flush()
		case strings.Contains(line, markDeduped):
			log.Deduped++
			log.DedupedLine = n
			log.SawSDK = true
		default:
			for _, tag := range sdkTags {
				if strings.Contains(line, tag) {
					log.SawSDK = true
					break
				}
			}
		}
	}
	if pending != nil && log.Platform == "ios" {
		pending.Unread = len(pending.Params) == 0
	}
	flush()
	if log.SawSDK && log.Platform == "" {
		log.Platform = "android"
	}
	return log, nil
}

// maxLine is the longest line kept whole; the SDK's never come near it.
const maxLine = 1 << 20

// readLines splits a log into lines. A log saved by Windows PowerShell is
// UTF-16 with a byte-order mark, and is read as such; a line longer than
// maxLine is cut there rather than failing the whole read.
func readLines(r io.Reader) ([]string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	text := decodeText(raw)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if len(line) > maxLine {
			line = line[:maxLine]
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// decodeText returns a log as UTF-8 text, from UTF-8 or from UTF-16 with a
// byte-order mark.
func decodeText(raw []byte) string {
	switch {
	case bytes.HasPrefix(raw, []byte{0xFF, 0xFE}), bytes.HasPrefix(raw, []byte{0xFE, 0xFF}):
		little := raw[0] == 0xFF
		raw = raw[2:]
		units := make([]uint16, len(raw)/2)
		for i := range units {
			if little {
				units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
			} else {
				units[i] = uint16(raw[2*i])<<8 | uint16(raw[2*i+1])
			}
		}
		return string(utf16.Decode(units))
	case bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}):
		return string(raw[3:])
	}
	return string(raw)
}

// parseIOSLine reads one line of the iOS SDK's output into the request it
// belongs to, and returns the request still open.
func parseIOSLine(log *Log, pending *Request, line string, n int) *Request {
	flush := func() {
		if pending != nil {
			pending.Unread = len(pending.Params) == 0
			log.Requests = append(log.Requests, *pending)
			pending = nil
		}
	}
	switch {
	case strings.Contains(line, iosMarkURL):
		flush()
		u := strings.TrimSpace(after(line, iosMarkURL))
		return &Request{URL: u, Endpoint: endpoint(u), Params: map[string]string{}, Line: n, Time: lineTime(line)}
	case strings.Contains(line, iosMarkDeduped):
		log.Deduped++
		log.DedupedLine = n
		return pending
	case pending == nil:
		return nil
	case strings.Contains(line, iosMarkParams):
		if values, err := url.ParseQuery(strings.TrimSpace(after(line, iosMarkParams))); err == nil {
			for k, v := range values {
				pending.Params[k] = v[0]
			}
		}
	case strings.Contains(line, iosMarkStatus):
		if status, err := strconv.Atoi(strings.TrimSpace(after(line, iosMarkStatus))); err == nil {
			pending.Code = status
		}
	case strings.Contains(line, iosMarkResponse):
		pending.Response = strings.TrimSpace(after(line, iosMarkResponse))
		if code, message := parseResponse(pending.Response); code != 0 {
			pending.Code, pending.Message = code, message
		}
		flush()
	}
	return pending
}

func after(line, marker string) string {
	return line[strings.Index(line, marker)+len(marker):]
}

// endpoint returns the path after the API version: ".../v0/ad_impressions/max"
// gives "ad_impressions/max".
func endpoint(url string) string {
	for _, version := range []string{"/v0/", "/v1/"} {
		if i := strings.Index(url, version); i >= 0 {
			return strings.TrimSuffix(url[i+len(version):], "/")
		}
	}
	return ""
}

var paramKey = regexp.MustCompile(`(?:^|, )([a-z_0-9]+)=`)

// parseParams reads the SDK's parameter dump, a Java map printed as
// {key=value, key=value}. Values may contain commas, so the text is split
// where a new "key=" begins and not at every comma.
func parseParams(s string) map[string]string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "{"), "}")
	params := map[string]string{}
	keys := paramKey.FindAllStringSubmatchIndex(s, -1)
	for i, k := range keys {
		end := len(s)
		if i+1 < len(keys) {
			end = keys[i+1][0]
		}
		params[s[k[2]:k[3]]] = s[k[1]:end]
	}
	return params
}

func parseResponse(body string) (int, string) {
	var r struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(body), &r) != nil {
		return 0, ""
	}
	return r.Code, r.Message
}
