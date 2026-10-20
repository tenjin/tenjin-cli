package cli

import (
	"fmt"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/session"
)

// refuseAgent reports whether the chosen agent must not be started, and
// says why. An agent that can read outside the project could read the
// Tenjin sign-in where it is kept in a file, as on a machine without a
// keychain.
func (s *setup) refuseAgent() bool {
	if !s.helper.ReadsOutsideProject() {
		return false
	}
	file := session.CredentialsFile(s.env.store, s.env.endpoints.Name)
	if file == "" {
		return false
	}
	why := fmt.Sprintf("%s was not started: its sandbox can read files outside this project, and this machine keeps your Tenjin sign-in in a file (%s), not in a system keychain.", s.helper.Name, file)
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
