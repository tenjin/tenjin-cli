package cli

import (
	"strings"
	"testing"

	"github.com/tenjin/tenjin-cli/internal/testproject"
)

// The sign-in says who, and names the environment only when it is not
// Tenjin's own; the short-lived token under it is not the person's concern.
func TestTheSignInSaysWhoAndNoMore(t *testing.T) {
	if got := environmentNote("production"); got != "" {
		t.Errorf("production: %q", got)
	}
	if got := environmentNote("test"); got != " (test)" {
		t.Errorf("test: %q", got)
	}

	h := newHarness(t)
	h.signIn()
	_, stdout, _ := h.run("auth", "status")
	if stdout != "Signed in to Acme Games (test).\nThe session renews itself as you use the Tenjin CLI, until 'tenjin logout'.\n" {
		t.Errorf("auth status:\n%s", stdout)
	}
	_, stdout, _ = h.typing("", "q").run("setup", testproject.Write(t, testproject.AndroidNative()))
	signIn := stdout[strings.Index(stdout, "Step 2 of 7"):]
	signIn = signIn[:strings.Index(signIn, "Step 3 of 7")]
	if !strings.Contains(signIn, "Signed in to Acme Games (test).\n") || strings.Contains(signIn, "token") || strings.Contains(signIn, "renews") {
		t.Errorf("setup talks about the token:\n%s", stdout)
	}
}

// A new Tenjin app is named after the project, not the folder it is in.
func TestANewAppIsNamedAfterTheProject(t *testing.T) {
	h := newHarness(t)
	h.signIn()
	dir := testproject.Write(t, testproject.AndroidNative().With(testproject.Files{"settings.gradle.kts": "rootProject.name = \"Timer\"\ninclude(\":app\")\n"}))
	_, stdout, _ := h.typing("", "n").run("setup", dir)
	if !strings.Contains(stdout, `Create "Timer (Android)"?`) {
		t.Errorf("stdout:\n%s", stdout)
	}
}
