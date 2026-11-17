package cli

import (
	"fmt"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/session"
)

// refusal says why agent a must not be started in this run, and what would
// let it start; why is "" when nothing stops it. An agent that can read
// outside the project could read the Tenjin sign-in where it is kept in a
// file, as on a machine without a keychain, and a key or token this run was
// given: removing them from the agent's environment does not remove them
// from this process's, or from its command line, which other programs on
// the machine can read.
func (s *setup) refusal(a *agent.Agent) (why, remedy string) {
	if !a.ReadsOutsideProject() {
		return "", ""
	}
	if file := session.CredentialsFile(s.env.store, s.env.endpoints.Name); file != "" {
		return fmt.Sprintf("its sandbox can read files outside this project, and this machine keeps your Tenjin sign-in in a file (%s), not in a system keychain", file), ""
	}
	if s.given.All.Value != "" || len(s.given.ByBuild) > 0 || s.env.getenv("TENJIN_API_TOKEN") != "" {
		return "its sandbox can read outside this project, including what this run of tenjin was given (an SDK key from --sdk-key or TENJIN_SDK_KEY*, or TENJIN_API_TOKEN)",
			fmt.Sprintf(" Run it again without them to let %s work%s.", a.Name, s.agentJob().keysLater)
	}
	return "", ""
}

// refuseAgent reports whether agent a must not be started, and says why and
// what to do instead.
func (s *setup) refuseAgent(a *agent.Agent) bool {
	why, remedy := s.refusal(a)
	if why == "" {
		return false
	}
	next := " " + s.agentJob().byHand
	for _, other := range agent.Find(s.env.lookPath) {
		if !other.ReadsOutsideProject() {
			next = fmt.Sprintf(" Use %s instead, with '%s --agent %s', or make the changes yourself.", other.Name, s.cmd.CommandPath(), other.ID)
			break
		}
	}
	s.say("%s %s was not started: %s.%s%s", s.style.Yellow("!"), a.Name, why, remedy, next)
	s.markRefused(a)
	return true
}

// offerable leaves out of installed the agents this run must not start,
// saying once of each why it is not offered.
func (s *setup) offerable(installed []agent.Agent) []agent.Agent {
	var ok []agent.Agent
	for i := range installed {
		a := &installed[i]
		why, remedy := s.refusal(a)
		if why == "" {
			ok = append(ok, *a)
			continue
		}
		if !s.refused[a.ID] {
			s.say("\n%s %s is installed, but is not offered: %s.%s", s.style.Yellow("!"), a.Name, why, remedy)
			s.markRefused(a)
		}
	}
	return ok
}

func (s *setup) markRefused(a *agent.Agent) {
	if s.refused == nil {
		s.refused = map[string]bool{}
	}
	s.refused[a.ID] = true
}
