package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/project"
)

func addFeaturesFlag(cmd *cobra.Command, into *[]string) {
	cmd.Flags().StringSliceVar(into, "features", nil, "the SDK features to integrate, besides app opens: "+strings.Join(project.FeatureIDs()[1:], ", ")+"; none for app opens only (default: the ones the project suggests)")
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
// with --features, or at a terminal the ones the developer picks from a list
// with the suggested ones marked, or without a terminal the suggested ones.
func (s *setup) chooseFeatures(p *project.Project) error {
	if s.features != nil {
		return nil
	}
	suggested := suggestedFeatures(p)
	s.say("\nTenjin features this integration can include:")
	for i, f := range p.Features {
		mark := " "
		if containsString(suggested, f.ID) {
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
	if !s.env.interactive {
		s.features = suggested
		example := project.FeaturePurchases
		if len(suggested) > 1 {
			example = strings.Join(suggested[1:], ",")
		}
		s.say("The ones marked are included. To choose, pass --features, such as --features %s.", example)
		return nil
	}
	for {
		answer, err := s.env.ask(s.out, "\nPress Enter for the ones marked, or give the numbers you want, such as 2,4 (0 for app opens only):")
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer == "" || answer == "y" || answer == "yes" || errors.Is(err, io.EOF) && answer == "" {
			s.features = suggested
			return nil
		}
		if picked, ok := pickFeatures(p, answer); ok {
			s.features = picked
			return nil
		}
		if errors.Is(err, io.EOF) {
			s.features = suggested
			return nil
		}
		s.say("  Give numbers from the list, separated by commas, or press Enter for the ones marked.")
	}
}

// pickFeatures reads the numbers given for features, with app opens always
// included.
func pickFeatures(p *project.Project, answer string) ([]string, bool) {
	picked := []string{project.FeatureOpens}
	for _, part := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		n := -1
		if _, err := fmt.Sscan(part, &n); err != nil || n < 0 || n > len(p.Features) {
			return nil, false
		}
		if n > 0 && !containsString(picked, p.Features[n-1].ID) {
			picked = append(picked, p.Features[n-1].ID)
		}
	}
	return picked, true
}

// featureList names the chosen features for people.
func featureList(ids []string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = project.FeatureName(id)
	}
	return strings.Join(names, ", ")
}
