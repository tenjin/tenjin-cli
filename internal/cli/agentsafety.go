package cli

import (
	"fmt"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/session"
)

// refuseAgent reports whether the chosen agent must not be started, and
// says why. An agent that can read outside the project could read the
// Tenjin sign-in where it is kept in a file, as on a machine without a
// keychain, and a key or token this run was given: removing them from the
// agent's environment does not remove them from this process's, or from its
// command line, which other programs on the machine can read.
func (s *setup) refuseAgent() bool {
	if !s.helper.ReadsOutsideProject() {
		return false
	}
	var why string
	if file := session.CredentialsFile(s.env.store, s.env.endpoints.Name); file != "" {
		why = fmt.Sprintf("%s was not started: its sandbox can read files outside this project, and this machine keeps your Tenjin sign-in in a file (%s), not in a system keychain.", s.helper.Name, file)
	} else if s.given.All.Value != "" || len(s.given.ByBuild) > 0 || s.env.getenv("TENJIN_API_TOKEN") != "" {
		why = fmt.Sprintf("%s was not started: its sandbox can read outside this project, including what this run of tenjin was given (an SDK key from --sdk-key or TENJIN_SDK_KEY*, or TENJIN_API_TOKEN). Run it again without them to let %s work, and give the keys at the prompt or afterwards with 'tenjin project set-keys'.", s.helper.Name, s.helper.Name)
	} else {
		return false
	}
	next := " Make the changes yourself; 'tenjin sdk guide' prints the integration guide."
	for _, a := range agent.Find(s.env.lookPath) {
		if !a.ReadsOutsideProject() {
			next = fmt.Sprintf(" Use %s instead, with '%s --agent %s', or make the changes yourself.", a.Name, s.cmd.CommandPath(), a.ID)
			break
		}
	}
	s.say("%s %s", s.style.Yellow("!"), why+next)
	return true
}
