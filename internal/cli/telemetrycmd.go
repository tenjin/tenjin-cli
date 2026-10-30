package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/telemetry"
)

// privacyURL is the README's list of what the usage statistics hold.
const privacyURL = "https://github.com/tenjin/tenjin-cli#privacy-and-security"

// telemetryStatus is whether this CLI sends usage statistics, and why not.
type telemetryStatus struct {
	Enabled bool `json:"enabled"`
	// Setting is what tenjin telemetry on or off left on this machine: "on"
	// or "off". Telemetry can be off with the setting on, as in CI.
	Setting string `json:"setting"`
	// Reason says why nothing is sent, when nothing is.
	Reason string `json:"reason,omitempty"`
}

func newTelemetryCmd(opts *options, env *environment) *cobra.Command {
	cmd := group("telemetry", "Turn the anonymous usage statistics on or off",
		newTelemetrySetCmd(opts, env, "off"),
		newTelemetrySetCmd(opts, env, "on"),
		newTelemetryStatusCmd(opts, env),
	)
	cmd.Long = `A release of the CLI sends Tenjin one anonymous event per command: the
command, the names of the flags given (never their values), how it ended,
and the shape of the project, such as its framework. Never a path, a bundle
ID, a key or anything from your code. The README lists every field:
` + privacyURL + `

'tenjin telemetry off' turns them off on this machine and deletes the random
ID that tells installs apart. TENJIN_NO_TELEMETRY=1 or DO_NOT_TRACK=1 turns
them off too, and nothing is sent in CI.`
	return cmd
}

func newTelemetrySetCmd(opts *options, env *environment, setting string) *cobra.Command {
	short := "Stop sending usage statistics from this machine"
	if setting == "on" {
		short = "Send usage statistics again"
	}
	return &cobra.Command{
		Use:   setting,
		Short: short,
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if env.telemetryState == "" {
				return clierr.New(clierr.Internal, "there is no configuration folder to keep the choice in").
					WithHint("Set TENJIN_NO_TELEMETRY=1 in your shell's environment instead.")
			}
			change := telemetry.TurnOn
			if setting == "off" {
				change = telemetry.TurnOff
				// Not even this command's own event.
				env.telemetry = nil
			}
			if err := change(env.telemetryState); err != nil {
				return clierr.Wrap(clierr.Internal, err, "could not turn usage statistics %s: %v", setting, err)
			}
			status := readTelemetryStatus(env)
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), status)
			}
			w := cmd.OutOrStdout()
			switch {
			case setting == "off":
				_, err = fmt.Fprintln(w, "Usage statistics are off. Nothing more is sent from this machine, and its random ID was deleted.")
			case status.Enabled:
				_, err = fmt.Fprintf(w, "Usage statistics are on. What they hold: %s\n", privacyURL)
			default:
				_, err = fmt.Fprintf(w, "Usage statistics are on, but none are sent: %s.\n", status.Reason)
			}
			return err
		},
	}
}

func newTelemetryStatusCmd(opts *options, env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Say whether usage statistics are sent, and if not, why",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			status := readTelemetryStatus(env)
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), status)
			}
			if status.Enabled {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Usage statistics are on. What they hold: %s\n'tenjin telemetry off' turns them off.\n", privacyURL)
			} else {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Usage statistics are off: %s.\n", status.Reason)
			}
			return err
		},
	}
}

// readTelemetryStatus reads the setting as it is now, which a command run
// before may have changed.
func readTelemetryStatus(env *environment) telemetryStatus {
	s := telemetryStatus{Setting: "on"}
	if env.telemetryState != "" && telemetry.TurnedOff(env.telemetryState) {
		s.Setting = "off"
	}
	s.Reason = telemetryOff(env.getenv, env.telemetryState, env.telemetryUnsent)
	s.Enabled = s.Reason == ""
	return s
}
