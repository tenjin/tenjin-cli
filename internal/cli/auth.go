package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/clierr"
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
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Signed in to %s (%s). The session is saved in %s.\n",
				status.OrganizationName, status.Environment, location)
			return err
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "print the sign-in URL instead of opening a browser")
	return cmd
}

// login runs the browser sign-in, narrating on stderr so stdout stays clean.
func login(cmd *cobra.Command, env *environment, noBrowser bool) (session.Status, string, error) {
	stderr := cmd.ErrOrStderr()
	manager := env.session()
	var pasted func() (string, error)
	if noBrowser && env.interactive {
		pasted = func() (string, error) { return env.stdin.ReadString('\n') }
	}
	open := func(url string) error {
		if noBrowser {
			_, _ = fmt.Fprintf(stderr, "Open this URL in a browser, on this machine or any other, to sign in:\n\n  %s\n\n", url)
			if pasted != nil {
				_, _ = fmt.Fprintf(stderr, "On another machine, as over SSH, the browser ends on an address starting http://127.0.0.1 that does not load. Copy that address from the address bar and paste it here.\n")
			}
			return nil
		}
		_, _ = fmt.Fprintf(stderr, "Opening the browser to sign in to Tenjin. If it does not open, visit:\n\n  %s\n\n", url)
		// A browser that fails to open is not fatal: the URL is on screen.
		_ = env.openBrowser(url)
		return nil
	}
	_, location, err := manager.Login(cmd.Context(), open, pasted)
	if err != nil {
		return session.Status{}, "", err
	}
	status, err := manager.Status()
	return status, location, err
}

func newLogoutCmd(opts *options, env *environment) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out on this machine",
		Long: `Forget the saved session and revoke its refresh token.

Tenjin's API token for the session is short-lived and cannot be cancelled from
here: it stops working within two hours. To cut access immediately, remove the
connection on the dashboard's access tokens page.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			wasSignedIn, err := env.session().Logout(cmd.Context())
			if err != nil {
				return err
			}
			if format == render.JSON {
				return render.WriteJSON(cmd.OutOrStdout(), map[string]bool{"signed_out": wasSignedIn})
			}
			if !wasSignedIn {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "Not signed in.")
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(),
				"Signed out on this machine.\nThe session's API token expires within two hours. To end it now, remove the connection at %s\n",
				env.endpoints.AccessTokensURL())
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
				err = writeStatus(cmd.OutOrStdout(), status)
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

func writeStatus(w io.Writer, s session.Status) error {
	if !s.SignedIn {
		_, err := fmt.Fprintf(w, "Not signed in (%s).\n", s.Environment)
		return err
	}
	if s.Source == session.SourceAPIToken {
		_, err := fmt.Fprintf(w, "Using the access token in TENJIN_API_TOKEN (%s).\n", s.Environment)
		return err
	}
	_, err := fmt.Fprintf(w, "Signed in to %s (%s).\n", s.OrganizationName, s.Environment)
	if err == nil && s.TokenExpiry != nil {
		_, err = fmt.Fprintf(w, "API token renews automatically; the current one expires %s.\n", s.TokenExpiry.Local().Format(time.RFC1123))
	}
	return err
}
