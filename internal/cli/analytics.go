package cli

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
)

// Custom events and the customer user ID are the two features only the
// developer can fill in: which events, and which value identifies a user.
// The app's other analytics SDKs often already say, so setup offers what
// they are given, asks, and passes the answers to the coding agent, which is
// told never to invent either.

// maxOfferedEvents is how many of the event names found are offered, and
// taken when nobody chooses.
const maxOfferedEvents = 20

// analyticsChoice is which custom events to send and which value identifies
// a user, as the developer gave them or as the app's analytics show them.
type analyticsChoice struct {
	// events are the custom events' names; none means the agent proposes
	// some instead of adding any.
	events []string
	// userID is the value that identifies a user, as code or in words; ""
	// means the agent proposes one instead of adding it. userIDSite is
	// where the app already gives that value to another SDK, if it does.
	userID     string
	userIDSite *project.AnalyticsUserID
}

// analyticsFlags are --events and --user-id as given.
type analyticsFlags struct {
	events      []string
	userID      string
	eventsGiven bool
	userIDGiven bool
}

func addAnalyticsFlags(cmd *cobra.Command, into *analyticsFlags) {
	cmd.Flags().StringArrayVar(&into.events, "events", nil, "the custom events to send to Tenjin, by name, comma-separated or repeated (default: the ones the app sends to its other analytics SDKs)")
	cmd.Flags().StringVar(&into.userID, "user-id", "", "the value that identifies a user, as code (user.id) or in words, for the customer user ID (default: the one the app gives its other analytics SDKs)")
}

// read checks the flags once they are parsed.
func (a *analyticsFlags) read(cmd *cobra.Command) error {
	a.eventsGiven = cmd.Flags().Changed("events")
	a.userIDGiven = cmd.Flags().Changed("user-id")
	if a.eventsGiven {
		names, err := eventNames(strings.Join(a.events, ","))
		if err != nil {
			return clierr.New(clierr.Usage, "--events: %s", err)
		}
		if len(names) == 0 {
			return clierr.New(clierr.Usage, "--events: give at least one event name")
		}
		a.events = names
	}
	if a.userIDGiven {
		a.userID = strings.TrimSpace(a.userID)
		switch {
		case a.userID == "":
			return clierr.New(clierr.Usage, "--user-id: give the value that identifies a user")
		case hasControl(a.userID):
			return clierr.New(clierr.Usage, "--user-id: it cannot hold a line break or another control character")
		}
	}
	return nil
}

// conflicts refuses --events or --user-id for a feature that --features
// leaves out.
func (a *analyticsFlags) conflicts(named []string) error {
	if named == nil {
		return nil
	}
	if a.eventsGiven && !containsString(named, project.FeatureCustomEvents) {
		return clierr.New(clierr.Usage, "--events names custom events, which --features leaves out").
			WithHint("Add custom_events to --features.")
	}
	if a.userIDGiven && !containsString(named, project.FeatureUserID) {
		return clierr.New(clierr.Usage, "--user-id names the customer user ID, which --features leaves out").
			WithHint("Add user_id to --features.")
	}
	return nil
}

// implied are the features the flags call for.
func (a *analyticsFlags) implied() []string {
	var out []string
	if a.eventsGiven {
		out = append(out, project.FeatureCustomEvents)
	}
	if a.userIDGiven {
		out = append(out, project.FeatureUserID)
	}
	return out
}

// eventNames reads a comma-separated list of event names: trimmed, without
// empty entries or repeats. Tenjin takes names as given, so nothing else is
// checked.
func eventNames(list string) ([]string, error) {
	var out []string
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" || containsString(out, name) {
			continue
		}
		if hasControl(name) {
			return nil, fmt.Errorf("an event name cannot hold a line break or another control character")
		}
		out = append(out, name)
	}
	return out, nil
}

func hasControl(s string) bool {
	return strings.IndexFunc(s, unicode.IsControl) >= 0
}

// offeredEvents are the event names the app sends to other SDKs that are
// offered, and taken when nobody chooses.
func offeredEvents(p *project.Project) []string {
	names := p.Analytics.Names()
	return names[:min(len(names), maxOfferedEvents)]
}

// defaultAnalytics is the choice without questions: the flags, or what the
// app's analytics show, or nothing for the agent to propose.
func defaultAnalytics(p *project.Project, flags analyticsFlags) analyticsChoice {
	c := analyticsChoice{events: offeredEvents(p)}
	if flags.eventsGiven {
		c.events = flags.events
	}
	if flags.userIDGiven {
		c.userID, c.userIDSite = flags.userID, userIDSite(p, flags.userID)
	} else if ids := p.Analytics.UserIDs; len(ids) > 0 {
		c.userID, c.userIDSite = ids[0].Expression, &ids[0]
	}
	return c
}

// userIDSite is where the app gives an SDK this value as the user's ID.
func userIDSite(p *project.Project, value string) *project.AnalyticsUserID {
	for i, u := range p.Analytics.UserIDs {
		if u.Expression == value {
			return &p.Analytics.UserIDs[i]
		}
	}
	return nil
}

// settleAnalytics settles, right after the features, which custom events to
// send and which value identifies a user, for the features chosen: from the
// flags, at a terminal from the developer, and otherwise from what the app
// already sends. Whichever it is, it is said back.
func (s *setup) settleAnalytics(p *project.Project) error {
	for _, id := range s.analyticsFlags.implied() {
		if containsString(s.features, id) {
			continue
		}
		flag := map[string]string{project.FeatureCustomEvents: "--events", project.FeatureUserID: "--user-id"}[id]
		if s.env.interactive {
			s.say("%s is not used: %s were not chosen.", flag, project.FeatureName(id))
			continue
		}
		s.features = inOrder(p, featureSet(append(s.features, id)))
		s.say("Also integrating %s, for %s.", project.FeatureName(id), flag)
	}
	s.analytics = defaultAnalytics(p, s.analyticsFlags)
	if containsString(s.features, project.FeatureCustomEvents) {
		if err := s.chooseEvents(p); err != nil {
			return err
		}
	}
	if containsString(s.features, project.FeatureUserID) {
		if err := s.chooseUserID(p); err != nil {
			return err
		}
	}
	return nil
}

func featureSet(ids []string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

// chooseEvents settles the custom events.
func (s *setup) chooseEvents(p *project.Project) error {
	width := render.TextWidth(s.out)
	switch {
	case s.analyticsFlags.eventsGiven:
		s.say("\nCustom events: %s (--events).", render.Clean(strings.Join(s.analytics.events, ", ")))
		return nil
	case !s.env.interactive:
		if len(s.analytics.events) > 0 {
			s.say("\n%s", render.Indent(render.Clean("Custom events: "+strings.Join(s.analytics.events, ", ")+", as the app sends them to "+sdkList(p, s.analytics.events)+". To choose others, pass --events."), 0, width))
		} else {
			s.say("\nCustom events: none named, so none are added; a coding agent proposes some in its report. To name them, pass --events.")
		}
		return nil
	}
	offered := offeredEvents(p)
	s.say("")
	s.listEvents(p, offered)
	question := "Which events should go to Tenjin? Names, comma-separated. Enter takes the ones above:"
	if len(offered) == 0 {
		question = "Which events should go to Tenjin? Names, comma-separated. Enter lets your coding agent propose some without adding them:"
	}
	for {
		answer, err := s.env.ask(s.out, question)
		if err != nil {
			return err
		}
		if answer == "" {
			s.analytics.events = offered
			break
		}
		names, err := eventNames(answer)
		if err == nil && len(names) == 0 {
			err = fmt.Errorf("give the names separated by commas, or press Enter")
		}
		if err != nil {
			s.say("  %s", err)
			continue
		}
		s.analytics.events = names
		break
	}
	if len(s.analytics.events) == 0 {
		s.say("Custom events: none named, so none are added; your coding agent proposes some in its report.")
		return nil
	}
	s.say("%s", render.Indent(render.Clean("Custom events: "+strings.Join(s.analytics.events, ", ")+"."), 0, width))
	return nil
}

// listEvents shows what the app already sends to other analytics SDKs, by
// SDK: the names offered, then the names made at runtime and the Adjust
// tokens, which are not offered, a few of each.
func (s *setup) listEvents(p *project.Project, offered []string) {
	width := render.TextWidth(s.out)
	all := p.Analytics.Names()
	if len(all) == 0 && len(p.Analytics.Events) == 0 {
		s.say("Custom events. The app sends no events to an analytics SDK the Tenjin CLI can read.")
		return
	}
	s.say("Custom events. The app already sends these to other analytics SDKs:")
	for _, sdk := range eventSDKs(p) {
		var names []string
		for _, e := range p.Analytics.Events {
			if e.SDK == sdk && e.Name != "" && containsString(offered, e.Name) && !containsString(names, e.Name) {
				names = append(names, e.Name)
			}
		}
		if len(names) > 0 {
			s.say("%s", render.Indent(render.Clean(sdk+": "+strings.Join(names, ", ")), 2, width))
		}
	}
	if more := len(all) - len(offered); more > 0 {
		s.say("  %s", s.style.Dim(fmt.Sprintf("and %d more, which Enter does not take; tenjin project detect --json lists them", more)))
	}
	var runtime, tokens []string
	for _, e := range p.Analytics.Events {
		switch {
		case e.Token != "":
			tokens = append(tokens, fmt.Sprintf("%s (%s:%d)", e.Token, e.File, e.Line))
		case e.Name == "":
			runtime = append(runtime, fmt.Sprintf("%s (%s, %s:%d)", e.Expression, e.SDK, e.File, e.Line))
		}
	}
	if len(runtime) > 0 {
		s.say("%s", render.Indent(render.Clean("Named at runtime, not read: "+firstFew(runtime, 3)), 2, width))
	}
	if len(tokens) > 0 {
		s.say("%s", render.Indent(render.Clean("Adjust event tokens, which are not names: "+firstFew(tokens, 3)), 2, width))
	}
}

// eventSDKs are the SDKs the app sends events to, the busiest first.
func eventSDKs(p *project.Project) []string {
	var out []string
	for _, e := range p.Analytics.Events {
		if !containsString(out, e.SDK) {
			out = append(out, e.SDK)
		}
	}
	return out
}

// sdkList names the SDKs the app sends these events to.
func sdkList(p *project.Project, names []string) string {
	var out []string
	for _, name := range names {
		for _, e := range p.Analytics.EventsNamed(name) {
			if !containsString(out, e.SDK) {
				out = append(out, e.SDK)
			}
		}
	}
	return strings.Join(out, " and ")
}

func firstFew(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(", and %d more", len(items)-n)
}

// chooseUserID settles the customer user ID.
func (s *setup) chooseUserID(p *project.Project) error {
	width := render.TextWidth(s.out)
	switch {
	case s.analyticsFlags.userIDGiven:
		s.say("\nCustomer user ID: %s (--user-id).", render.Clean(s.analytics.userID))
		return nil
	case !s.env.interactive:
		if site := s.analytics.userIDSite; site != nil {
			s.say("\n%s", render.Indent(render.Clean(fmt.Sprintf("Customer user ID: %s, the one the app gives %s (%s:%d). To choose another, pass --user-id.", site.Expression, site.SDK, site.File, site.Line)), 0, width))
		} else {
			s.say("\nCustomer user ID: none found, so none is added; a coding agent proposes one in its report. To name it, pass --user-id.")
		}
		return nil
	}
	ids := p.Analytics.UserIDs
	s.say("")
	var question string
	if len(ids) == 0 {
		s.say("Customer user ID. The app gives no analytics SDK the Tenjin CLI can read a user ID.")
		question = `Which value identifies a user? Describe it, such as "the account ID from the login response". Enter leaves it to your coding agent to propose one, without adding it:`
	} else {
		s.say("Customer user ID. The app already gives other analytics SDKs a user ID:")
		for _, u := range ids[:min(len(ids), 3)] {
			s.say("%s", render.Indent(render.Clean(fmt.Sprintf("%s: %s (%s:%d)", u.SDK, u.Expression, u.File, u.Line)), 2, width))
		}
		question = render.Clean(fmt.Sprintf("Which value identifies a user? Enter takes %s (the one the app gives %s):", ids[0].Expression, ids[0].SDK))
	}
	answer, err := s.env.ask(s.out, question)
	if err != nil {
		return err
	}
	switch {
	case answer == "" && len(ids) == 0:
		s.analytics.userID, s.analytics.userIDSite = "", nil
	case answer == "":
		s.analytics.userID, s.analytics.userIDSite = ids[0].Expression, &ids[0]
	default:
		s.analytics.userID, s.analytics.userIDSite = answer, userIDSite(p, answer)
	}
	s.sayUserID()
	return nil
}

// sayUserID says the customer user ID back.
func (s *setup) sayUserID() {
	width := render.TextWidth(s.out)
	switch site := s.analytics.userIDSite; {
	case s.analytics.userID == "":
		s.say("Customer user ID: none named, so none is added; your coding agent proposes one in its report.")
	case site != nil:
		s.say("%s", render.Indent(render.Clean(fmt.Sprintf("Customer user ID: %s, the one the app gives %s (%s:%d).", site.Expression, site.SDK, site.File, site.Line)), 0, width))
	default:
		s.say("%s", render.Indent(render.Clean("Customer user ID: "+s.analytics.userID+"."), 0, width))
	}
}

// analyticsTask is what the coding agent is told about a feature that
// needs the developer's choice: the exact events or value, with where the
// app already sends them, or, when there is none, to propose and not add.
func analyticsTask(p *project.Project, id string, c analyticsChoice) string {
	switch id {
	case project.FeatureCustomEvents:
		if len(c.events) == 0 {
			text := "none were named. Do not add custom events; in your final report, propose up to five, from what the app does."
			if hints := eventHints(p); hints != "" {
				text += " The app sends these to other analytics SDKs, which may help: " + hints + "."
			}
			return text
		}
		names := make([]string, len(c.events))
		for i, name := range c.events {
			names[i] = name
			if sites := p.Analytics.EventsNamed(name); len(sites) > 0 {
				names[i] += " (next to its " + where(sites[0].SDK, sites[0].Through, sites[0].File, sites[0].Line) + ")"
			}
		}
		return "send exactly these, by these names, each where the app already does the corresponding thing: " + strings.Join(names, ", ") + ". Add no others."
	case project.FeatureUserID:
		if c.userID == "" {
			return "none was named. Do not guess: leave the code unchanged for this feature, and in your final report propose one."
		}
		value := c.userID
		if c.userIDSite == nil {
			value = "the developer describes it as: " + c.userID
		}
		text := value + ". Pass it with setCustomerUserId (or the platform's equivalent from the guide) where the app first knows it"
		if site := c.userIDSite; site != nil {
			text += ", next to its " + where(site.SDK, site.Through, site.File, site.Line)
		}
		return text + "."
	}
	return ""
}

// where says where the app calls an SDK: directly, or through a function of
// its own.
func where(sdk, through, file string, line int) string {
	text := fmt.Sprintf("%s call at %s:%d", sdk, file, line)
	if through != "" {
		text = fmt.Sprintf("%s call at %s:%d, which goes through %s", sdk, file, line, through)
	}
	return text
}

// eventHints are the events the app sends to other SDKs that have no name
// to offer: names made at runtime and Adjust tokens.
func eventHints(p *project.Project) string {
	var hints []string
	for _, e := range p.Analytics.Events {
		switch {
		case e.Token != "":
			hints = append(hints, fmt.Sprintf("Adjust event token %s at %s:%d", e.Token, e.File, e.Line))
		case e.Name == "":
			hints = append(hints, fmt.Sprintf("%s to %s at %s:%d", e.Expression, e.SDK, e.File, e.Line))
		}
	}
	return firstFew(hints, 5)
}

// proposals are the features the agent is told to propose rather than add.
func (c analyticsChoice) proposals(features []string) []string {
	var out []string
	if containsString(features, project.FeatureCustomEvents) && len(c.events) == 0 {
		out = append(out, project.FeatureCustomEvents)
	}
	if containsString(features, project.FeatureUserID) && c.userID == "" {
		out = append(out, project.FeatureUserID)
	}
	return out
}

// analyticsGuide is what a developer integrating by hand is told about the
// events and the user ID chosen.
func (s *setup) analyticsGuide() []string {
	var out []string
	if containsString(s.features, project.FeatureCustomEvents) && len(s.analytics.events) > 0 {
		out = append(out, "Custom events to send: "+strings.Join(s.analytics.events, ", ")+".")
	}
	if containsString(s.features, project.FeatureUserID) && s.analytics.userID != "" {
		text := "Customer user ID: " + s.analytics.userID + "."
		if site := s.analytics.userIDSite; site != nil {
			text = fmt.Sprintf("Customer user ID: %s, which the app gives %s at %s:%d: pass it to Tenjin there too.", site.Expression, site.SDK, site.File, site.Line)
		}
		out = append(out, text)
	}
	return out
}

// analyticsSummary describes, for project detect, what the app sends to
// other analytics SDKs: one line per SDK.
func analyticsSummary(p *project.Project) [][2]string {
	var sdks []string
	add := func(sdk string) {
		if !containsString(sdks, sdk) {
			sdks = append(sdks, sdk)
		}
	}
	for _, e := range p.Analytics.Events {
		add(e.SDK)
	}
	for _, u := range p.Analytics.UserIDs {
		add(u.SDK)
	}
	var lines [][2]string
	for _, sdk := range sdks {
		var names []string
		runtime, tokens := 0, 0
		for _, e := range p.Analytics.Events {
			switch {
			case e.SDK != sdk:
			case e.Token != "":
				tokens++
			case e.Name == "":
				runtime++
			case !containsString(names, e.Name):
				names = append(names, e.Name)
			}
		}
		var parts []string
		if len(names) > 0 {
			parts = append(parts, "events "+firstFew(names, 5))
		}
		if runtime > 0 {
			parts = append(parts, pluralVerb(runtime, "event named at runtime", "events named at runtime"))
		}
		if tokens > 0 {
			parts = append(parts, plural(tokens, "event token"))
		}
		for _, u := range p.Analytics.UserIDs {
			if u.SDK == sdk {
				parts = append(parts, fmt.Sprintf("user ID %s (%s:%d)", u.Expression, u.File, u.Line))
				break
			}
		}
		lines = append(lines, [2]string{sdk, strings.Join(parts, "; ")})
	}
	return lines
}
