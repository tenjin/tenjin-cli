// Package cli builds the tenjin command tree.
//
// Commands stay thin: parse flags, call one operation, render the result.
// Nothing in this package talks HTTP.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/tenjin/tenjin-cli/internal/buildinfo"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/collector"
	"github.com/tenjin/tenjin-cli/internal/config"
	"github.com/tenjin/tenjin-cli/internal/devicelog"
	"github.com/tenjin/tenjin-cli/internal/oauth"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/session"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
	"github.com/tenjin/tenjin-cli/internal/transport"
)

// options holds the flags every command shares.
type options struct {
	output  string
	json    bool
	verbose bool
}

// format resolves --output and its --json shorthand.
func (o *options) format(cmd *cobra.Command) (render.Format, error) {
	if o.json {
		if cmd.Flags().Changed("output") && o.output != string(render.JSON) {
			return "", clierr.New(clierr.Usage, "--json conflicts with --output %s", o.output)
		}
		return render.JSON, nil
	}
	f, err := render.ParseFormat(o.output)
	if err != nil {
		return "", clierr.Wrap(clierr.Usage, err, "%v", err)
	}
	return f, nil
}

// environment is everything a command reads from outside its arguments.
// Tests replace it to point the CLI at fakes.
type environment struct {
	getenv    func(string) string
	endpoints config.Endpoints
	store     session.Store
	stdin     *bufio.Reader
	// interactive is true when a person is at the terminal. Commands never
	// prompt otherwise.
	interactive bool
	openBrowser func(url string) error
	// loginTimeout bounds the wait for the browser; zero means the default.
	loginTimeout time.Duration
	// lookPath finds an installed program, and executable returns the path
	// of this one. Both are used to hand work to a coding agent.
	lookPath   func(string) (string, error)
	executable func() (string, error)
	// run executes a program and returns its output. It is how the device
	// log is read through adb.
	run devicelog.Runner
	// stream runs a program until a line of its output says to stop: how an
	// app launched on an iPhone is read.
	stream devicelog.Streamer
	// wait pauses; tests make it return at once.
	wait func(time.Duration)
	// verbose receives a line for each request sent and each program run,
	// when --verbose is given. Nil otherwise.
	verbose io.Writer
}

// transport is what every HTTP client uses: retries where they are safe, and
// the --verbose log.
func (e *environment) transport() *transport.Transport {
	return transport.New(e.verbose)
}

// collector returns a client for Tenjin's collector.
func (e *environment) collector() *collector.Client {
	c := collector.New(e.endpoints.Collector, userAgent())
	c.HTTP.Transport = e.transport()
	return c
}

// runner is env.run, logged with --verbose.
func (e *environment) runner() devicelog.Runner {
	if e.verbose == nil {
		return e.run
	}
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		_, _ = fmt.Fprintf(e.verbose, "tenjin: run %s %s\n", name, strings.Join(args, " "))
		return e.run(ctx, name, args...)
	}
}

func defaultEnvironment() (*environment, error) {
	endpoints, err := config.Resolve(os.Getenv)
	if err != nil {
		return nil, clierr.Wrap(clierr.Usage, err, "%v", err)
	}
	return &environment{
		getenv:      os.Getenv,
		endpoints:   endpoints,
		store:       session.NewStore(),
		stdin:       bufio.NewReader(os.Stdin),
		interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		openBrowser: openBrowser,
		lookPath:    exec.LookPath,
		wait:        time.Sleep,
		executable:  os.Executable,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		stream: streamProgram,
	}, nil
}

// streamProgram runs a program, gathers its output line by line, and kills
// it once stop reports true for a line, or when ctx ends. Killing it is
// SIGKILL, which devicectl cannot pass on to the app it launched, so the
// app keeps running.
func streamProgram(ctx context.Context, name string, args []string, stop func(string) bool) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	stopped := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		out.Write(scanner.Bytes())
		out.WriteByte('\n')
		if stop(scanner.Text()) {
			stopped = true
			cancel()
			break
		}
	}
	err = cmd.Wait()
	if stopped || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out.Bytes(), nil
	}
	return out.Bytes(), err
}

// streamer is stream, logged with --verbose.
func (e *environment) streamer() devicelog.Streamer {
	if e.verbose == nil {
		return e.stream
	}
	return func(ctx context.Context, name string, args []string, stop func(string) bool) ([]byte, error) {
		_, _ = fmt.Fprintf(e.verbose, "tenjin: run %s %s\n", name, strings.Join(args, " "))
		return e.stream(ctx, name, args, stop)
	}
}

func userAgent() string {
	return "tenjin-cli/" + buildinfo.Get().Version
}

// session returns the manager for the signed-in state.
func (e *environment) session() *session.Manager {
	authority := oauth.New(e.endpoints.Issuer, e.endpoints.Resource)
	authority.HTTP.Transport = e.transport()
	return &session.Manager{
		Environment: e.endpoints.Name,
		Store:       e.store,
		Authority:   loginAuthority{Client: authority, timeout: e.loginTimeout},
		APIToken:    e.getenv("TENJIN_API_TOKEN"),
	}
}

// loginAuthority applies the environment's login timeout.
type loginAuthority struct {
	*oauth.Client
	timeout time.Duration
}

func (a loginAuthority) Login(ctx context.Context, opts oauth.LoginOptions) (oauth.Tokens, error) {
	if opts.Timeout == 0 {
		opts.Timeout = a.timeout
	}
	return a.Client.Login(ctx, opts)
}

// api returns a client for the Campaign Management API.
func (e *environment) api() *tenjinapi.Client {
	api := tenjinapi.New(e.endpoints.API, userAgent(), e.session())
	api.HTTP.Transport = e.transport()
	return api
}

// Execute runs the CLI and returns the process exit code. The result goes to
// stdout; everything else, including errors, goes to stderr.
func Execute(args []string, stdout, stderr io.Writer) int {
	defer render.PrepareConsole()()
	env, err := defaultEnvironment()
	if err != nil {
		e := clierr.From(err)
		writeError(stderr, e, wantsJSON(args))
		return e.Kind.ExitCode()
	}
	return execute(args, stdout, stderr, env)
}

func execute(args []string, stdout, stderr io.Writer, env *environment) int {
	// Ctrl-C cancels the command's context, which stops anything it started,
	// such as a coding agent.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := newRootCmd(env)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	e := clierr.From(err)
	writeError(stderr, e, wantsJSON(args))
	return e.Kind.ExitCode()
}

// writeError reports a failure on stderr: prose for people, an object with a
// stable code for scripts and agents.
func writeError(w io.Writer, e *clierr.Error, asJSON bool) {
	hint := e.Hint
	if hint == "" && e.Kind == clierr.Usage {
		hint = "Run 'tenjin --help' for usage."
	}
	// A failed write to stderr has nowhere to be reported; the exit code still tells the caller.
	if asJSON {
		body := map[string]string{"code": string(e.Kind), "message": e.Message}
		if hint != "" {
			body["hint"] = hint
		}
		_ = render.WriteJSON(w, map[string]any{"error": body})
		return
	}
	style := render.NewStyler(w)
	_, _ = fmt.Fprintf(w, "%s %s\n", style.Red("tenjin:"), e.Message)
	if hint != "" {
		_, _ = fmt.Fprintln(w, style.Dim(hint))
	}
}

// wantsJSON reports whether the arguments ask for JSON output. It reads the
// raw arguments because an error can occur before flags are parsed.
func wantsJSON(args []string) bool {
	for i, arg := range args {
		switch {
		case arg == "--":
			return false
		case arg == "--json", arg == "--json=true", arg == "-ojson", arg == "-o=json", arg == "--output=json":
			return true
		case (arg == "-o" || arg == "--output") && i+1 < len(args) && args[i+1] == "json":
			return true
		}
	}
	return false
}

func newRootCmd(env *environment) *cobra.Command {
	opts := &options{}
	root := &cobra.Command{
		Use:   "tenjin",
		Short: "Developer tools for integrating and verifying the Tenjin SDK",
		Long: `tenjin covers the parts of Tenjin SDK onboarding that happen on your machine:
working out what the project is, finding its Tenjin app, checking the
integration, and explaining why it sends nothing.

Start with 'tenjin setup'. The commands under it also work on their own: each
accepts --json and never prompts when stdin is not a terminal, so scripts and
coding agents can drive them.`,
		Version:       buildinfo.Get().Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// cobra only defaults this on its own unknown-command path, which the
		// RunE below replaces.
		SuggestionsMinimumDistance: 2,
		// Accept any args so an unknown command is reported as a usage error
		// (exit 2) instead of cobra's default help-and-exit-0.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return unknownCommand(cmd, args[0])
		},
	}
	root.SetVersionTemplate(buildinfo.Get().String() + "\n")
	styleHelp(root)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return clierr.Wrap(clierr.Usage, err, "%v", err)
	})

	// Defined by hand so cobra does not claim -v, which is reserved for --verbose.
	root.Flags().Bool("version", false, "print the version and exit")
	root.PersistentFlags().StringVarP(&opts.output, "output", "o", string(render.Table), "output format: table or json")
	root.PersistentFlags().BoolVar(&opts.json, "json", false, "shorthand for --output json")
	root.PersistentFlags().BoolVarP(&opts.verbose, "verbose", "v", false, "show each request to Tenjin, and each program run, on stderr")
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		if opts.verbose {
			env.verbose = cmd.ErrOrStderr()
		}
	}

	// Help lists the commands in the order a new user needs them, so cobra
	// must not sort them by name.
	cobra.EnableCommandSorting = false
	groups := []struct {
		title    string
		commands []*cobra.Command
	}{
		{"Start here:", []*cobra.Command{newSetupCmd(opts, env)}},
		{"The steps, one at a time:", []*cobra.Command{newProjectCmd(opts, env), newAppsCmd(opts, env), newDoctorCmd(opts, env), newSDKCmd(opts), newAgentCmd(opts, env)}},
		{"Your account:", []*cobra.Command{newLoginCmd(opts, env), newLogoutCmd(opts, env), newAuthCmd(opts, env)}},
	}
	for i, g := range groups {
		id := fmt.Sprint(i)
		root.AddGroup(&cobra.Group{ID: id, Title: g.title})
		for _, c := range g.commands {
			c.GroupID = id
			root.AddCommand(c)
		}
	}
	root.AddCommand(newVersionCmd(opts))
	return root
}

func unknownCommand(cmd *cobra.Command, name string) error {
	if suggestions := cmd.SuggestionsFor(name); len(suggestions) > 0 {
		return clierr.New(clierr.Usage, "unknown command %q for %q; did you mean %q?", name, cmd.CommandPath(), suggestions[0])
	}
	return clierr.New(clierr.Usage, "unknown command %q for %q", name, cmd.CommandPath())
}

// group builds a command that only holds subcommands. Run with no arguments
// it prints help; with an unknown one it fails as a usage error.
func group(use, short string, subcommands ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:                        use,
		Short:                      short,
		SuggestionsMinimumDistance: 2,
		Args:                       cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			return unknownCommand(cmd, args[0])
		},
	}
	cmd.AddCommand(subcommands...)
	return cmd
}

// noArgs rejects positional arguments as a usage error.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return clierr.New(clierr.Usage, "%q takes no arguments, got %q", cmd.CommandPath(), args[0])
	}
	return nil
}

// exactArgs requires n positional arguments; what names them for the error.
func exactArgs(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return clierr.New(clierr.Usage, "%q takes %s", cmd.CommandPath(), what)
		}
		return nil
	}
}

// maxArgs allows up to n positional arguments.
func maxArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return clierr.New(clierr.Usage, "%q takes at most %d argument(s), got %s", cmd.CommandPath(), n, strings.Join(args, " "))
		}
		return nil
	}
}
