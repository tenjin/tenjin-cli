package tenjinapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/clierr"
)

// Integration is everything about one app's callbacks to one channel, in one
// place: the channel's callback group templates, the app's groups (each with
// whether it can be activated and the settings it still lacks), the
// callback settings the groups share, and the app's events. A part that
// could not be read is empty, with a warning.
type Integration struct {
	Templates []map[string]any `json:"available_templates"`
	Groups    []map[string]any `json:"configured_groups"`
	Settings  map[string]any   `json:"callback_settings"`
	Events    []map[string]any `json:"events"`
	Warnings  []string         `json:"warnings,omitempty"`
}

// InspectIntegration reads an app's integration with one channel.
func (c *Client) InspectIntegration(ctx context.Context, app string, channel int) (*Integration, error) {
	appID, err := ParseID(app, "app")
	if err != nil {
		return nil, err
	}
	out := &Integration{}
	every := Paging{All: true}
	// The first failure of each read of the app's own data: when all of
	// them fail, nothing is known about the app, which must not read as
	// "nothing configured".
	var appFailures []error
	appReads := map[string]bool{"configured_groups": true, "callback_settings": true, "events": true}
	read := func(name string, get func() (Document, error)) any {
		doc, err := get()
		if err != nil {
			e := clierr.From(err)
			if e.Kind == clierr.NotSignedIn {
				panic(signInError{err})
			}
			if appReads[name] {
				appFailures = append(appFailures, err)
			}
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %s", name, e.Message))
			return nil
		}
		dec := json.NewDecoder(bytes.NewReader(doc))
		dec.UseNumber()
		var body struct {
			Data any `json:"data"`
		}
		if err := dec.Decode(&body); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: not a JSON:API document", name))
			return nil
		}
		return flatten(body.Data)
	}
	err = catchSignIn(func() {
		out.Templates = list(read("available_templates", func() (Document, error) {
			return c.CallbackGroupTemplates(ctx, channel, every)
		}))
		out.Groups = list(read("configured_groups", func() (Document, error) {
			return c.AppCallbackGroups(ctx, app, channel, every)
		}))
		settingsKnown := false
		if settings, ok := read("callback_settings", func() (Document, error) {
			return c.fetch(ctx, func(cl clients) (*http.Response, error) {
				return cl.cma.GetAppsAppIdIntegrationsIntegrationIdCallbackSettings(ctx, appID, channel)
			})
		}).(map[string]any); ok {
			out.Settings, settingsKnown = settings, true
		}
		out.Events = list(read("events", func() (Document, error) {
			return c.AppEvents(ctx, app, every)
		}))
		annotate(out, settingsKnown)
	})
	if err != nil {
		return nil, err
	}
	if len(appFailures) == len(appReads) {
		return nil, appFailures[0]
	}
	return out, nil
}

// CallbackGroup is one of an app's callback groups, as a question about it
// names it.
type CallbackGroup struct {
	ID, Name string
	Active   bool
}

// GroupCreateWouldChange is the app's callback group on a channel that
// creating another from template would change, or nil when it would make a
// new one. A template that does not allow more than one group per app gets
// the app's existing group changed, with what the create sends, rather than
// a second one.
func (c *Client) GroupCreateWouldChange(ctx context.Context, app string, channel int, template string) (*CallbackGroup, error) {
	every := Paging{All: true}
	groups, err := flatList(c.AppCallbackGroups(ctx, app, channel, every))
	if err != nil {
		return nil, err
	}
	var found *CallbackGroup
	for _, g := range groups {
		if t, _ := g["callback_group_template_id"].(string); strings.EqualFold(t, template) {
			id, _ := g["id"].(string)
			name, _ := g["name"].(string)
			active, _ := g["active"].(bool)
			found = &CallbackGroup{ID: id, Name: name, Active: active}
			break
		}
	}
	if found == nil {
		return nil, nil
	}
	templates, err := flatList(c.CallbackGroupTemplates(ctx, channel, every))
	if err != nil {
		return nil, err
	}
	for _, t := range templates {
		if id, _ := t["id"].(string); strings.EqualFold(id, template) {
			if multiple, _ := t["allow_multiple"].(bool); multiple {
				return nil, nil
			}
			if found.Name == "" {
				found.Name, _ = t["name"].(string)
			}
		}
	}
	return found, nil
}

// flatList flattens the resources of a list read.
func flatList(doc Document, err error) ([]map[string]any, error) {
	if err != nil {
		return nil, err
	}
	var body struct {
		Data any `json:"data"`
	}
	if err := json.Unmarshal(doc, &body); err != nil {
		return nil, clierr.Wrap(clierr.Upstream, err, "Tenjin's answer is not a JSON:API document")
	}
	return list(flatten(body.Data)), nil
}

type signInError struct{ err error }

// catchSignIn turns a lost sign-in in any part into the error itself: the
// rest would fail the same way.
func catchSignIn(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			s, ok := r.(signInError)
			if !ok {
				panic(r)
			}
			err = s.err
		}
	}()
	f()
	return nil
}

// annotate marks each group with the settings its template requires that
// neither it nor the shared settings give, and whether it can be activated.
func annotate(in *Integration, settingsKnown bool) {
	required := map[string][]string{}
	for _, t := range in.Templates {
		id, _ := t["id"].(string)
		for _, s := range asList(t["required_settings"]) {
			if name, ok := s.(string); ok {
				required[id] = append(required[id], name)
			}
		}
	}
	shared, _ := in.Settings["callback_settings"].(map[string]any)
	overstated := false
	for _, g := range in.Groups {
		template, _ := g["callback_group_template_id"].(string)
		own, _ := g["callback_settings"].(map[string]any)
		missing := []string{}
		for _, name := range required[template] {
			if !given(own[name]) && !given(shared[name]) {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		active, _ := g["active"].(bool)
		g["missing_settings"] = missing
		// Inactive with nothing missing means a condition Tenjin does not
		// say, such as no ad account: it cannot be promised.
		g["activatable"] = len(missing) == 0 && active
		if !settingsKnown && len(missing) > 0 {
			overstated = true
		}
	}
	if overstated {
		in.Warnings = append(in.Warnings, "missing_settings may be overstated: the shared callback settings could not be read")
	}
}

func given(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	}
	return true
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func list(v any) []map[string]any {
	out := []map[string]any{}
	for _, item := range asList(v) {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// flatten turns JSON:API resources into flat objects: their attributes and
// their id.
func flatten(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = flatten(e)
		}
		return out
	case map[string]any:
		attrs, ok := t["attributes"].(map[string]any)
		if !ok {
			return t
		}
		flat := map[string]any{}
		for k, val := range attrs {
			flat[k] = val
		}
		if id, ok := t["id"]; ok {
			flat["id"] = id
		}
		return flat
	}
	return v
}
