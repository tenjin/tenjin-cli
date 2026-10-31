package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/config"
	"github.com/tenjin/tenjin-cli/internal/oauth"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/session"
)

func newAuthCmd(opts *options, env *environment) *cobra.Command {
	return group("auth", "Sign in, sign out, and see who is signed in",
		newLoginCmd(opts, env),
		newLogoutCmd(opts, env),
		newAuthStatusCmd(opts, env),
	)
}

func newLoginCmd(opts *options, env *environment) *cobra.Command {
	var noBrowser bool
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to Tenjin through the browser",
		Long: `Sign in to Tenjin through the browser and save the session in the system
keychain. The session is for the organization currently selected in the
Tenjin dashboard.

The browser ends on an address on this machine (http://127.0.0.1). A browser
on another machine, as over SSH or in a container, cannot reach it: paste
that address at the terminal instead. The CLI asks for it with --no-browser,
over SSH, and when no browser opens.

In CI, skip this command and set TENJIN_API_TOKEN to an access token created
in the dashboard.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			status, location, err := login(cmd, env, noBrowser)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), status)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Signed in to %s%s. The session is saved in %s.\n",
				oneLine(status.OrganizationName), environmentNote(status.Environment), location)
			if env.session().APIToken != "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "TENJIN_API_TOKEN is set, and other commands use it rather than this session until it is unset.")
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, noBrowserHelp)
	return cmd
}

const noBrowserHelp = "print the sign-in URL instead of opening a browser, and at a terminal take the address the browser ends on, pasted, for a browser on another machine"

// login runs the browser sign-in, narrating on stderr so stdout stays clean.
//
// The browser's redirect comes back to this machine's loopback address,
// which a browser on another machine cannot reach. At a terminal, the
// address it ends on can then be pasted instead: with --no-browser, over
// SSH, and when no browser could be opened.
func login(cmd *cobra.Command, env *environment, noBrowser bool) (session.Status, string, error) {
	stderr := cmd.ErrOrStderr()
	manager := env.session()
	// A paste still being read when the sign-in ends is left to the next
	// question (readLineUntil).
	ctx, stop := context.WithCancel(cmd.Context())
	defer stop()
	paste := false // decided by open, before the first read
	pasted := func() (string, error) {
		if !paste {
			return "", errNoPaste
		}
		return env.readLineUntil(ctx)
	}
	open := func(url string) error {
		// Without a paste, only a browser on this machine can finish.
		where := " on this machine"
		if noBrowser {
			if paste = env.interactive; paste {
				where = " on this machine or any other"
			}
			_, _ = fmt.Fprintf(stderr, "Open this URL in a browser,%s, to sign in:\n\n  %s\n\n", where, url)
		} else if err := env.openBrowser(url); err != nil {
			if paste = env.interactive; paste {
				where = ""
			}
			_, _ = fmt.Fprintf(stderr, "Could not open a browser (%v). Open this URL in a browser%s to sign in:\n\n  %s\n\n", err, where, url)
		} else {
			// The browser may have opened on the far machine's screen, or
			// on none.
			paste = env.interactive && overSSH(env.getenv)
			_, _ = fmt.Fprintf(stderr, "Opening the browser to sign in to Tenjin. If it does not open, visit:\n\n  %s\n\n", url)
		}
		if paste {
			_, _ = fmt.Fprintln(stderr, pasteHelp)
		}
		return nil
	}
	c, location, err := manager.Login(ctx, open, pasted)
	if errors.Is(err, oauth.ErrTimedOut) {
		return session.Status{}, "", clierr.From(err).WithHint("%s", timeoutHint(cmd.CommandPath(), env.interactive, paste))
	}
	if err != nil {
		return session.Status{}, "", err
	}
	return manager.StatusOf(c), location, nil
}

// pasteHelp says how to finish a sign-in whose browser cannot reach this
// machine.
const pasteHelp = "On another machine, as over SSH, the browser ends on an address starting http://127.0.0.1 that does not load. Copy that address from the address bar and paste it here."

// errNoPaste ends the reading of a paste where none was offered.
var errNoPaste = errors.New("no paste offered")

// overSSH reports whether the CLI runs in an SSH session, where a browser
// it opens may be on another machine than the one signing in, or on none.
func overSSH(getenv func(string) string) bool {
	return getenv("SSH_CONNECTION") != "" || getenv("SSH_TTY") != ""
}

// timeoutHint is what to do when nobody finished the sign-in in time.
// command is the command that signs in, for it to be run again.
func timeoutHint(command string, interactive, pasteOffered bool) string {
	const token = "set TENJIN_API_TOKEN to an access token from the Tenjin dashboard"
	switch {
	case pasteOffered:
		return "Where the browser is on another machine, paste the address it ends on, starting http://127.0.0.1, before the wait ends; or " + token + "."
	case interactive:
		return fmt.Sprintf("Where the browser cannot reach this machine, as over SSH or in a container, run '%s --no-browser' and paste the address the browser ends on; or %s.", command, token)
	}
	return "Without a terminal, only a browser on this machine can finish the sign-in. Elsewhere, as over SSH or in CI, " + token + "."
}

func newLogoutCmd(opts *options, env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out, and end the API access of your Tenjin CLI sessions",
		Long: `Forget the saved session and revoke it, which ends its API access at once.
Every Tenjin CLI session of yours in this organization shares that access, so
signing out here signs them all out, on every machine.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			manager := env.session()
			out, err := manager.Logout(cmd.Context())
			if err != nil {
				if out.WasSignedIn {
					// Forgotten here, but the session may still be open at Tenjin.
					return clierr.Wrap(clierr.Upstream, err, "signed out on this machine, but Tenjin did not confirm it, so the session's API access may still be open (%v)", err).
						WithHint("End it on the dashboard: remove the connection at %s", env.endpoints.AccessTokensURL())
				}
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), map[string]bool{
					"signed_out": out.WasSignedIn, "access_ended": out.AccessEnded,
					// Commands keep using it, signed out or not.
					"api_token_set": manager.APIToken != "",
				})
			}
			switch {
			case !out.WasSignedIn && manager.APIToken != "":
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No saved session to sign out of.")
			case !out.WasSignedIn:
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Not signed in.")
			case out.AccessEnded:
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Signed out. The session's API access has ended.")
			default:
				_, err = fmt.Fprintf(cmd.OutOrStdout(),
					"Signed out on this machine.\nThe session's API token expires within two hours. To end it now, remove the connection at %s\n",
					env.endpoints.AccessTokensURL())
			}
			if err == nil && manager.APIToken != "" {
				_, err = fmt.Fprintln(cmd.ErrOrStderr(), "TENJIN_API_TOKEN is still set, and commands keep using it.")
			}
			return err
		},
	}
}

func newAuthStatusCmd(opts *options, env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show who is signed in",
		Long: `Show whether a session exists, for which organization, and where the
credential comes from. Exits 3 when not signed in.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			status, err := env.session().Status()
			if err != nil {
				return err
			}
			if format == render.JSON {
				err = render.WriteJSON(cmd.OutOrStdout(), status)
			} else {
				err = writeStatus(cmd.OutOrStdout(), status, true)
			}
			if err != nil {
				return err
			}
			if !status.SignedIn {
				return clierr.New(clierr.NotSignedIn, "not signed in").
					WithHint("Run 'tenjin login', or set TENJIN_API_TOKEN to a token from the Tenjin dashboard.")
			}
			return nil
		},
	}
}

// writeStatus says who is signed in. With more, it also says how long the
// sign-in lasts: the short-lived token under it is the CLI's business, and
// --json shows its expiry.
func writeStatus(w io.Writer, s session.Status, more bool) error {
	where := environmentNote(s.Environment)
	if !s.SignedIn {
		_, err := fmt.Fprintf(w, "Not signed in%s.\n", where)
		return err
	}
	if s.Source == session.SourceAPIToken {
		_, err := fmt.Fprintf(w, "Using the access token in TENJIN_API_TOKEN%s.\n", where)
		return err
	}
	_, err := fmt.Fprintf(w, "Signed in to %s%s.\n", oneLine(s.OrganizationName), where)
	if err == nil && more {
		_, err = fmt.Fprintln(w, "The session renews itself as you use the Tenjin CLI, until 'tenjin logout'.")
	}
	return err
}

// environmentNote names an environment other than Tenjin's own services, as
// " (name)", for a sign-in to a test copy; production goes unsaid.
func environmentNote(environment string) string {
	if environment == "" || environment == config.ProductionName {
		return ""
	}
	return " (" + environment + ")"
}
