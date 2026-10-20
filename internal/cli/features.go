package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/project"
)

// addFeaturesFlag adds --features; verb says what is done with them, such as
// "integrate" or "check".
func addFeaturesFlag(cmd *cobra.Command, into *[]string, verb string) {
	cmd.Flags().StringSliceVar(into, "features", nil, "the SDK features to "+verb+", besides app opens: "+strings.Join(project.FeatureIDs()[1:], ", ")+"; none for app opens only (default: the ones the project suggests)")
}

// chosenFeatures checks the features named with --features against the
// project's, and returns them with app opens, which are always included.
// nil means none were named.
func chosenFeatures(p *project.Project, named []string) ([]string, error) {
	if named == nil {
		return nil, nil
	}
	chosen := []string{project.FeatureOpens}
	for _, id := range named {
		id = strings.ToLower(strings.TrimSpace(id))
		switch {
		case id == "none" || id == "" || id == project.FeatureOpens:
			continue
		case p.Feature(id) == nil:
			var all []string
			for _, f := range p.Features {
				all = append(all, f.ID)
			}
			return nil, clierr.New(clierr.Usage, "--features %s: not a feature of this project", id).
				WithHint("Its features: %s.", strings.Join(all, ", "))
		}
		if !containsString(chosen, id) {
			chosen = append(chosen, id)
		}
	}
	return chosen, nil
}

// suggestedFeatures are the features the project suggests, app opens first.
func suggestedFeatures(p *project.Project) []string {
	var out []string
	for _, f := range p.Features {
		if f.Suggested || f.Required {
			out = append(out, f.ID)
		}
	}
	return out
}

func containsString(values []string, v string) bool {
	for _, candidate := range values {
		if candidate == v {
			return true
		}
	}
	return false
}

// chooseFeatures settles which features setup integrates: the ones named
// with --features; at a terminal, the suggested ones changed one by one until
// the developer presses Enter; without a terminal, the suggested ones.
// Whichever it is, the choice is said back.
func (s *setup) chooseFeatures(p *project.Project) error {
	if s.features != nil {
		s.say("\nIntegrating: %s (--features).", featureList(s.features))
		return nil
	}
	chosen := map[string]bool{}
	for _, id := range suggestedFeatures(p) {
		chosen[id] = true
	}
	s.say("\nTenjin features for this integration (%s = included):", s.style.Green("✓"))
	s.listFeatures(p, chosen)
	if !s.env.interactive {
		s.features = inOrder(p, chosen)
		s.say("Integrating: %s. To choose, pass --features, such as --features %s.", featureList(s.features), featureExample(p))
		return nil
	}
	for {
		answer, err := s.env.ask(s.out, "\nType numbers to add or remove (such as 3, or 3,4), all or none. Press Enter when the "+s.style.Green("✓")+" list is right:")
		if err != nil {
			return err
		}
		answer = strings.ToLower(answer)
		if answer == "" {
			s.features = inOrder(p, chosen)
			s.say("Integrating: %s.", featureList(s.features))
			return nil
		}
		if message := toggleFeatures(p, chosen, answer); message != "" {
			s.say("  %s", message)
		} else {
			s.listFeatures(p, chosen)
		}
	}
}

// listFeatures prints the project's features, the chosen ones ticked.
func (s *setup) listFeatures(p *project.Project, chosen map[string]bool) {
	for i, f := range p.Features {
		mark := " "
		if chosen[f.ID] || f.Required {
			mark = s.style.Green("✓")
		}
		line := fmt.Sprintf("  %s %d) %s", mark, i+1, f.Name)
		switch {
		case f.Required:
			line += s.style.Dim("  always included")
		case len(f.Evidence) > 0:
			var why []string
			for _, e := range f.Evidence {
				why = append(why, e.What+" in "+e.File)
			}
			line += s.style.Dim("  (" + strings.Join(why, "; ") + ")")
		}
		s.say("%s", line)
	}
}

// toggleFeatures applies an answer to the choice: numbers add or remove,
// all and none set it. It returns what to tell the developer when the
// answer cannot be applied, or "" when it was.
func toggleFeatures(p *project.Project, chosen map[string]bool, answer string) string {
	switch answer {
	case "all":
		for _, f := range p.Features {
			chosen[f.ID] = true
		}
		return ""
	case "none", "0":
		for id := range chosen {
			delete(chosen, id)
		}
		return ""
	}
	var numbers []int
	for _, part := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		n := 0
		if _, err := fmt.Sscan(part, &n); err != nil || n < 1 || n > len(p.Features) || fmt.Sprint(n) != part {
			return fmt.Sprintf("%q is not in the list. Type numbers from 1 to %d, all or none, or press Enter.", part, len(p.Features))
		}
		numbers = append(numbers, n)
	}
	for _, n := range numbers {
		f := p.Features[n-1]
		if f.Required {
			if len(numbers) == 1 {
				return f.Name + " are always included."
			}
			continue
		}
		chosen[f.ID] = !chosen[f.ID]
	}
	return ""
}

// inOrder is the chosen features in the project's order, required ones
// included.
func inOrder(p *project.Project, chosen map[string]bool) []string {
	var out []string
	for _, f := range p.Features {
		if chosen[f.ID] || f.Required {
			out = append(out, f.ID)
		}
	}
	return out
}

func featureExample(p *project.Project) string {
	for _, f := range p.Features {
		if !f.Required {
			return f.ID
		}
	}
	return "none"
}

// featureList names the chosen features for people.
func featureList(ids []string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = project.FeatureName(id)
	}
	return strings.Join(names, ", ")
}
