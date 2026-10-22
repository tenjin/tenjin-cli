package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/testproject"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

const gameFile = "app/src/main/java/com/example/sample/Game.kt"

// withAnalytics is the Android sample with Firebase Analytics: two events
// and a user ID.
func withAnalytics() testproject.Files {
	return testproject.AndroidNative().With(testproject.Files{
		gameFile: `package com.example.sample

import com.google.firebase.Firebase
import com.google.firebase.analytics.FirebaseAnalytics
import com.google.firebase.analytics.analytics

class Game {
    private val analytics = Firebase.analytics

    fun finish(user: User) {
        analytics.logEvent("level_complete", null)
        analytics.logEvent(FirebaseAnalytics.Event.LEVEL_UP, null)
        analytics.logEvent("level_complete", null)
        analytics.setUserId(user.id)
    }
}
`,
	})
}

// analyticsSetup is a signed-in harness whose app exists, and the project.
func analyticsSetup(t *testing.T, files testproject.Files) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	h.fake.addApp("Sample", "com.example.sample", "android")
	h.signIn()
	return h, testproject.Write(t, files)
}

func TestProjectDetectShowsTheAppsAnalytics(t *testing.T) {
	dir := testproject.Write(t, withAnalytics())
	h := newHarness(t)
	code, stdout, stderr := h.run("project", "detect", dir)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	lacks(t, stdout, "Analytics in the app", "Firebase Analytics  events level_complete, level_up; user ID user.id ("+gameFile+":14)")

	code, stdout, _ = h.run("project", "detect", dir, "--json")
	var out struct {
		Analytics struct {
			Events []struct {
				Name, SDK, File string
				Line, Count     int
			}
			UserIDs []struct{ Expression string } `json:"user_ids"`
		}
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || code != 0 {
		t.Fatalf("exit = %d, %v", code, err)
	}
	if e := out.Analytics.Events; len(e) != 2 || e[0].Name != "level_complete" || e[0].Count != 2 || e[0].Line != 11 || e[0].File != gameFile || e[1].Name != "level_up" {
		t.Errorf("events = %+v", e)
	}
	if u := out.Analytics.UserIDs; len(u) != 1 || u[0].Expression != "user.id" {
		t.Errorf("user IDs = %+v", u)
	}
}

func TestSetupAsksWhichEventsAndWhichUserID(t *testing.T) {
	h, dir := analyticsSetup(t, withAnalytics())
	args := []string{"setup", dir, "--features", "custom_events,user_id", "--agent", "none"}

	// Enter takes what the app sends already, said back.
	_, stdout, _ := h.typing("", "", "q").run(args...)
	lacks(t, stdout,
		"Custom events. The app already sends these to other analytics SDKs:",
		"  Firebase Analytics: level_complete, level_up",
		"Which events should go to Tenjin? Names, comma-separated. Enter takes the ones above:",
		"Custom events: level_complete, level_up.",
		"Customer user ID. The app already gives other analytics SDKs a user ID:",
		"  Firebase Analytics: user.id ("+gameFile+":14)",
		"Which value identifies a user? Enter takes user.id (the one the app gives Firebase Analytics):",
		"Customer user ID: user.id, the one the app gives Firebase Analytics ("+gameFile+":14).",
		// By hand, the guide says them again.
		"Custom events to send: level_complete, level_up.",
	)

	// Names as given: trimmed, without repeats or empty entries. A value in
	// words is taken as it is.
	_, stdout, _ = h.typing(" boss_defeated , Purchase Made,,boss_defeated", "the account ID from the login response", "q").run(args...)
	lacks(t, stdout,
		"Custom events: boss_defeated, Purchase Made.",
		"Customer user ID: the account ID from the login response.",
	)

	// An answer that names nothing is asked again.
	_, stdout, _ = h.typing(" , ,", "tutorial_done", "", "q").run(args...)
	lacks(t, stdout, "give the names separated by commas, or press Enter", "Custom events: tutorial_done.")
}

func TestSetupWithNoAnalyticsLeavesThemToTheAgent(t *testing.T) {
	h, dir := analyticsSetup(t, testproject.AndroidNative())
	_, stdout, _ := h.typing("", "", "q").run("setup", dir, "--features", "custom_events,user_id", "--agent", "none")
	lacks(t, stdout,
		"Custom events. The app sends no events to an analytics SDK the Tenjin CLI can read.",
		"Enter lets your coding agent propose some without adding them:",
		"Custom events: none named, so none are added; your coding agent proposes some in its report.",
		"Customer user ID. The app gives no analytics SDK the Tenjin CLI can read a user ID.",
		`Describe it, such as "the account ID from the login response". Enter leaves it to your coding agent to propose one, without adding it:`,
		"Customer user ID: none named, so none is added; your coding agent proposes one in its report.",
	)
}

func TestTheEndOfTheInputAtTheAnalyticsQuestions(t *testing.T) {
	for _, tc := range []struct{ typed, prompt string }{
		{"", "Which events should go to Tenjin?"},
		{"\n", "Which value identifies a user?"},
	} {
		h, dir := analyticsSetup(t, withAnalytics())
		h.env.interactive = true
		h.env.stdin = bufio.NewReader(strings.NewReader(tc.typed))
		code, stdout, stderr := h.run("setup", dir, "--features", "custom_events,user_id")
		if code != 2 || !strings.Contains(stdout, tc.prompt) || !strings.Contains(stderr, "the input ended before an answer was given") {
			t.Errorf("exit = %d, stderr = %q\n%s", code, stderr, stdout)
		}
	}
}

func TestSetupWithoutATerminalTakesWhatTheAppSends(t *testing.T) {
	h, dir := analyticsSetup(t, withAnalytics())
	_, stdout, _ := h.run("setup", dir, "--features", "custom_events,user_id", "--agent", "none")
	lacks(t, stdout,
		"Custom events: level_complete, level_up, as the app sends them to Firebase Analytics. To choose others, pass --events.",
		"Customer user ID: user.id, the one the app gives Firebase Analytics ("+gameFile+":14). To choose another, pass --user-id.",
	)
	if strings.Contains(stdout, "Which events") || strings.Contains(stdout, "Which value") {
		t.Errorf("asked without a terminal:\n%s", stdout)
	}

	h, dir = analyticsSetup(t, testproject.AndroidNative())
	_, stdout, _ = h.run("setup", dir, "--features", "custom_events,user_id", "--agent", "none")
	lacks(t, stdout,
		"Custom events: none named, so none are added; a coding agent proposes some in its report. To name them, pass --events.",
		"Customer user ID: none found, so none is added; a coding agent proposes one in its report. To name it, pass --user-id.",
	)
}

func TestEventsAndUserIDFlags(t *testing.T) {
	h, dir := analyticsSetup(t, withAnalytics())

	// The flags answer the questions, at a terminal too.
	_, stdout, _ := h.typing("q").run("setup", dir, "--features", "custom_events,user_id", "--agent", "none",
		"--events", "boss_defeated, tutorial_done", "--events", "boss_defeated", "--user-id", " account.uid ")
	lacks(t, stdout, "Custom events: boss_defeated, tutorial_done (--events).", "Customer user ID: account.uid (--user-id).")
	if strings.Contains(stdout, "Which events") || strings.Contains(stdout, "Which value") {
		t.Errorf("asked although the flags answer:\n%s", stdout)
	}

	// Without a terminal and without --features, they choose their
	// features.
	h.env.interactive = false
	_, stdout, _ = h.run("setup", dir, "--agent", "none", "--events", "boss_defeated")
	lacks(t, stdout, "Also integrating Custom events, for --events.", "Custom events: boss_defeated (--events).")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--features", "purchases", "--events", "x"}, "--events names custom events, which --features leaves out"},
		{[]string{"--features", "none", "--user-id", "user.id"}, "--user-id names the customer user ID, which --features leaves out"},
		{[]string{"--events", " , "}, "--events: give at least one event name"},
		{[]string{"--events", "a\nb"}, "--events: an event name cannot hold a line break or another control character"},
		{[]string{"--user-id", " "}, "--user-id: give the value that identifies a user"},
		{[]string{"--user-id", "user\n.id"}, "--user-id: it cannot hold a line break"},
	} {
		for _, command := range []string{"setup", "doctor"} {
			code, _, stderr := h.run(append([]string{command, dir}, tc.args...)...)
			if code != 2 || !strings.Contains(stderr, tc.want) {
				t.Errorf("%s %q: exit = %d, stderr = %q", command, tc.args, code, stderr)
			}
		}
	}
}

func TestDoctorTakesTheEventsFlagAsTheFeatureChosen(t *testing.T) {
	h, dir := analyticsSetup(t, withAnalytics())
	_, stdout, _ := h.run("doctor", dir, "--offline", "--events", "boss_defeated")
	lacks(t, stdout, "Custom events: chosen for the integration, but not sent to Tenjin")
	if strings.Contains(stdout, "Customer user ID") {
		t.Errorf("the user ID was not chosen:\n%s", stdout)
	}
}

// task builds the agent's task for the sample with analytics, with custom
// events and the customer user ID chosen.
func task(t *testing.T, files testproject.Files, choice analyticsChoice) string {
	t.Helper()
	p, err := project.Detect(testproject.Write(t, files))
	if err != nil {
		t.Fatal(err)
	}
	features := []string{project.FeatureOpens, project.FeatureCustomEvents, project.FeatureUserID}
	report := verify.Run(context.Background(), p, verify.Options{Features: features})
	prompt := integrationPrompt(p, report, map[string]bool{}, features, choice, "tenjin doctor --json")
	if strings.Contains(prompt, testproject.Key) {
		t.Errorf("the task holds the SDK key:\n%s", prompt)
	}
	return prompt
}

func TestTheAgentIsToldTheEventsAndTheUserID(t *testing.T) {
	p, err := project.Detect(testproject.Write(t, withAnalytics()))
	if err != nil {
		t.Fatal(err)
	}
	prompt := task(t, withAnalytics(), defaultAnalytics(p, analyticsFlags{}))
	for _, want := range []string{
		"- Custom events: send exactly these, by these names, each where the app already does the corresponding thing: level_complete (next to its Firebase Analytics call at " + gameFile + ":11), level_up (next to its Firebase Analytics call at " + gameFile + ":12). Add no others.",
		"- Customer user ID: user.id. Pass it with setCustomerUserId (or the platform's equivalent from the guide) where the app first knows it, next to its Firebase Analytics call at " + gameFile + ":14.",
		"[warning] Custom events: chosen for the integration, but not sent to Tenjin",
		`apart from findings whose id starts with "key." and the "sdk.key.unfilled" findings for the placeholders, which are expected.`,
		"Finish with a summary of at most six short lines,",
		"Custom events and the customer user ID are not such choices: use only what the features above name.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s", want, prompt)
		}
	}

	// Names the developer gave, one the app does not send yet, and a user
	// ID in words.
	prompt = task(t, withAnalytics(), analyticsChoice{events: []string{"boss_defeated", "level_complete"}, userID: "the account ID from the login response"})
	for _, want := range []string{
		"each where the app already does the corresponding thing: boss_defeated, level_complete (next to its Firebase Analytics call at " + gameFile + ":11). Add no others.",
		"- Customer user ID: the developer describes it as: the account ID from the login response. Pass it with setCustomerUserId (or the platform's equivalent from the guide) where the app first knows it.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s", want, prompt)
		}
	}
}

func TestTheAgentProposesWhatNobodyNamed(t *testing.T) {
	files := testproject.AndroidNative().With(testproject.Files{
		gameFile: `package com.example.sample

import com.google.firebase.analytics.FirebaseAnalytics

class Game(private val analytics: FirebaseAnalytics) {
    fun finish(level: Int) {
        val name = "level_" + level
        analytics.logEvent(name, null)
    }
}
`,
	})
	prompt := task(t, files, analyticsChoice{})
	for _, want := range []string{
		"- Custom events: none were named. Do not add custom events; in your final report, propose up to five, from what the app does. The app sends these to other analytics SDKs, which may help: name to Firebase Analytics at " + gameFile + ":8.",
		"- Customer user ID: none was named. Do not guess: leave the code unchanged for this feature, and in your final report propose one.",
		`and "feature.custom_events" and "feature.user_id", for what you only propose; all are expected.`,
		"Finish with a summary of at most six short lines, then your proposals,",
		"found nothing else to change.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s", want, prompt)
		}
	}
	// What is to be proposed is not an item to resolve.
	if strings.Contains(prompt, "chosen for the integration, but not sent to Tenjin") {
		t.Errorf("the task asks to resolve a feature it says to propose:\n%s", prompt)
	}
}

// The developer's answers reach the agent's task.
func TestSetupGivesTheAgentTheAnswers(t *testing.T) {
	h, dir := analyticsSetup(t, withAnalytics())
	agent := h.installAgent()
	_, stdout, _ := h.typing("boss_defeated", "", "y", "q").run("setup", dir, "--features", "custom_events,user_id")
	prompt := agent.read("prompt")
	for _, want := range []string{
		"- Custom events: send exactly these, by these names, each where the app already does the corresponding thing: boss_defeated. Add no others.",
		"- Customer user ID: user.id. Pass it with setCustomerUserId",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the task lacks %q:\n%s\n%s", want, prompt, stdout)
		}
	}
}
