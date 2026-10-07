package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// guideURLs are the public integration instructions for each framework.
var guideURLs = map[project.Framework]string{
	project.Flutter:     "https://github.com/tenjin/tenjin-flutter-sdk",
	project.ReactNative: "https://github.com/tenjin/tenjin-react-native-sdk",
	project.Capacitor:   "https://github.com/tenjin/tenjin-ionic-sdk",
	project.Unity:       "https://github.com/tenjin/tenjin-unity-sdk",
	project.Unreal:      "https://github.com/tenjin/tenjin-unreal-sdk",
}

var nativeGuides = map[project.Platform]string{
	project.Android: "https://github.com/tenjin/tenjin-android-sdk",
	project.IOS:     "https://github.com/tenjin/tenjin-ios-sdk",
}

var platformNames = map[project.Platform]string{project.Android: "Android", project.IOS: "iOS"}

func newSetupCmd(opts *options, env *environment) *cobra.Command {
	var (
		yes       bool
		sdkKeys   []string
		offline   bool
		agentFlag string
		builds    []string
		features  []string
	)
	cmd := &cobra.Command{
		Use:   "setup [path]",
		Short: "Set up Tenjin in this project, step by step",
		Long: `Walk through setting up Tenjin in a project:

  1. work out what the project is, and which builds it ships
  2. sign in
  3. find or create the Tenjin app for each build
  4. get each app's SDK key: fetched from Tenjin where it serves keys, or
     copied from the dashboard, and checked against Tenjin at once
  5. add the SDK: with your coding agent if you want, or by hand
  6. check the integration, and check again after each change until it is clean
  7. check a real launch on a device or simulator, until Tenjin has
     processed the install

A Tenjin app is one bundle ID on one platform, and an Android build for
another store (Amazon, or any other) is an app of its own, so a project can
need several apps and SDK keys. setup lists the builds it finds and asks which
ship; --build names them instead.

The Tenjin CLI itself never edits the project, except to write each SDK key
where a placeholder stands for it. In step 5, if Claude Code or Codex is
installed, setup offers to have it make the changes; with both, it asks
which. The agent runs under your own account, in this project only, and
setup checks its work afterwards. Claude Code may edit files and run
'tenjin doctor', and nothing else. Codex runs in its own sandbox: it can
change files in this project only and has no network access. Decline, or
pass --agent none, to do it by hand.

Each step is also a command of its own (project detect, login, apps ensure,
doctor) for scripts and agents.

Without a terminal, setup runs once and asks nothing: it creates apps only
with --yes and starts an agent only with --agent.`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if format == render.JSON {
				return clierr.New(clierr.Usage, "setup is a guided flow and has no JSON output").
					WithHint("For scripts and agents, run the steps themselves: tenjin project detect, tenjin apps ensure, tenjin doctor. Each accepts --json.")
			}
			if agentFlag != "" && agentFlag != "none" && !contains(agent.IDs(), agentFlag) {
				return clierr.New(clierr.Usage, "unknown agent %q (use %s, or none)", agentFlag, strings.Join(agent.IDs(), ", "))
			}
			given, err := givenKeys(sdkKeys, env.getenv)
			if err != nil {
				return err
			}
			s := &setup{cmd: cmd, env: env, args: args, yes: yes, offline: offline, given: given, agentFlag: agentFlag,
				chosen: builds, wantFeatures: features, out: cmd.OutOrStdout(), style: render.NewStyler(cmd.OutOrStdout()),
				appIDs: map[string]string{}, keyURLs: map[string]string{},
				keys: map[string]verify.GivenKey{}, inProject: map[string]bool{}}
			_, _ = io.WriteString(s.out, render.Banner(s.out, tagline))
			return s.run()
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "create missing apps without asking")
	addSDKKeyFlag(cmd, &sdkKeys)
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the SDK key check against Tenjin, and the look for newer SDK releases")
	cmd.Flags().StringVar(&agentFlag, "agent", "", "coding agent to integrate with: "+strings.Join(agent.IDs(), ", ")+", or none to do it by hand (default: ask)")
	addBuildFlag(cmd, &builds)
	addFeaturesFlag(cmd, &features)
	cmd.PreRunE = func(cmd *cobra.Command, _ []string) error {
		// An empty --features means app opens only, not "not given".
		if cmd.Flags().Changed("features") && features == nil {
			features = []string{}
		}
		return nil
	}
	return cmd
}

func addBuildFlag(cmd *cobra.Command, into *[]string) {
	cmd.Flags().StringArrayVar(into, "build", nil, "limit to this build, as tenjin project detect lists them (android, ios, android:<variant>); repeat for several")
}

// narrowBuilds limits a project to the builds named, if any.
func narrowBuilds(p *project.Project, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if unknown := p.KeepBuilds(ids); len(unknown) > 0 {
		var all []string
		for _, b := range p.Builds() {
			all = append(all, b.ID)
		}
		return clierr.New(clierr.Usage, "--build %s: this project has no such build", strings.Join(unknown, ", ")).
			WithHint("Its builds: %s.", strings.Join(all, ", "))
	}
	return nil
}

type setup struct {
	cmd       *cobra.Command
	env       *environment
	args      []string
	yes       bool
	offline   bool
	agentFlag string
	out       io.Writer
	style     render.Styler
	// given are the SDK keys passed in with --sdk-key or the environment.
	given verify.GivenKeys
	// chosen are the IDs of the builds to set up; nil means all of them.
	chosen []string
	// wantFeatures are the features named with --features, and features
	// the ones settled in step 1, app opens first.
	wantFeatures []string
	features     []string
	// appIDs and keyURLs are, for each build ID, its Tenjin app and the
	// dashboard page showing that app's SDK keys.
	appIDs  map[string]string
	keyURLs map[string]string
	// keys are the SDK keys to integrate with, by build ID, and where each
	// came from. A build with no entry either has its key in the project
	// already or has none yet.
	keys map[string]verify.GivenKey
	// inProject marks the builds whose key is already written in the
	// project and accepted by Tenjin.
	inProject map[string]bool
	// helper is the coding agent the user agreed to use, if any.
	helper *agent.Agent
	// pagesShown is set once the dashboard's test-device pages are named.
	pagesShown bool
}

func (s *setup) say(format string, a ...any) {
	_, _ = fmt.Fprintf(s.out, format+"\n", a...)
}

func (s *setup) step(n int, title string) {
	s.say("\n%s", s.style.Heading(fmt.Sprintf("Step %d of 7 · %s", n, title)))
}

func (s *setup) run() error {
	s.step(1, "Your project")
	p, err := loadProject(s.args)
	if err != nil {
		return err
	}
	if err := writeProject(s.out, p); err != nil {
		return err
	}
	if !p.Supported {
		return clierr.New(clierr.NotFound, "%s", strings.Join(p.Notes, " "))
	}
	if err := resolveGiven(&s.given, p, s.env.getenv); err != nil {
		return err
	}
	if err := narrowBuilds(p, s.chosen); err != nil {
		return err
	}
	if s.features, err = chosenFeatures(p, s.wantFeatures); err != nil {
		return err
	}
	if err := s.chooseFeatures(p); err != nil {
		return err
	}

	s.step(2, "Sign in")
	if err := s.signIn(); err != nil {
		return err
	}

	s.step(3, "Tenjin apps")
	if err := s.chooseBuilds(p); err != nil {
		return err
	}
	if err := s.apps(p); err != nil {
		return err
	}

	s.step(4, "SDK keys")
	if err := s.sdkKeys(p); err != nil {
		return err
	}

	s.step(5, "Add the SDK")
	if err := s.integrate(p); err != nil {
		return err
	}

	s.step(6, "Check the integration")
	if err := s.check(); err != nil {
		return err
	}

	s.step(7, "Check a real launch")
	return s.launch(p)
}

func (s *setup) signIn() error {
	manager := s.env.session()
	status, err := manager.Status()
	if err != nil {
		return err
	}
	if !status.SignedIn {
		if !s.env.interactive {
			return clierr.New(clierr.NotSignedIn, "not signed in").
				WithHint("Run 'tenjin login' first, or set TENJIN_API_TOKEN to a token from the Tenjin dashboard.")
		}
		s.say("You are not signed in. New to Tenjin? Create an account first: %s", s.env.endpoints.SignupURL())
		if _, err := s.env.ask(s.out, "Press Enter to sign in through the browser."); err != nil {
			return err
		}
		if status, _, err = login(s.cmd, s.env, false); err != nil {
			return err
		}
	}
	return writeStatus(s.out, status)
}

// chooseBuilds asks which builds ship, when a target has several and none
// were named with --build. Enter keeps them all; without a terminal they are
// all kept, and the list says how to narrow it.
func (s *setup) chooseBuilds(p *project.Project) error {
	all := p.Builds()
	several := false
	for _, t := range p.Targets {
		several = several || len(t.Builds) > 1
	}
	if !several || len(s.chosen) > 0 {
		return nil
	}
	s.say("This project ships builds that are separate apps in Tenjin, each with its own SDK key:")
	for i, b := range all {
		t, _ := p.Build(b.ID)
		s.say("  %d) %s: %s  %s", i+1, buildTitle(t, b), b.BundleID, s.style.Dim(b.ID))
	}
	if !s.env.interactive {
		s.say("All of them are set up. To set up only some, pass --build for each, such as --build %s.", all[len(all)-1].ID)
		return nil
	}
	answer, err := s.env.ask(s.out, "\nWhich of these do you ship? Press Enter for all, or give their numbers, such as 1,3:")
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil
	}
	var picked []string
	for _, part := range strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' }) {
		n := 0
		if _, err := fmt.Sscan(part, &n); err != nil || n < 1 || n > len(all) {
			return clierr.New(clierr.Usage, "%q is not one of the builds listed", part).
				WithHint("Run setup again and give numbers from the list, or pass --build.")
		}
		picked = append(picked, all[n-1].ID)
	}
	s.chosen = picked
	if err := narrowBuilds(p, picked); err != nil {
		return err
	}
	s.say("Setting up %s.", plural(len(picked), "build"))
	return nil
}

// apps finds or creates the Tenjin app for each build the project ships. A
// problem with one build does not stop the walkthrough: the check at the end
// reports it again in context.
func (s *setup) apps(p *project.Project) error {
	if n := len(p.Builds()); n > 1 {
		s.say("A Tenjin app is one bundle ID on one platform, so this project needs %d apps, each with its own SDK key.", n)
	}
	for i := range p.Targets {
		t := &p.Targets[i]
		if len(t.Builds) == 0 {
			s.say("  %s %s: the bundle ID could not be read, so its app was not looked up.", s.style.Yellow("!"), platformNames[t.Platform])
			continue
		}
		for _, b := range t.Builds {
			label := buildTitle(t, b)
			name := appName(p, t, b)
			result, err := ensureApp(s.cmd.Context(), s.env, s.out, b.BundleID, b.TenjinPlatform, name, s.yes)
			if err != nil {
				e := clierr.From(err)
				// Not being allowed to create an app, or choosing not to,
				// leaves the rest of the walkthrough useful. Anything else
				// stops it.
				if e.Kind != clierr.Permission && e.Kind != clierr.NotFound {
					return err
				}
				s.say("  %s %s %s: %s", s.style.Yellow("!"), label, b.BundleID, e.Message)
				if e.Hint != "" {
					s.say("      %s", e.Hint)
				}
				continue
			}
			verb := "found"
			if result.Created {
				verb = "created"
			}
			s.appIDs[b.ID] = result.ID
			s.keyURLs[b.ID] = s.env.endpoints.SDKKeysURL(result.ID)
			s.say("  %s %s %s: %s %q", s.style.Green("✓"), label, b.BundleID, verb, result.Name)
		}
	}
	return nil
}

// appName is the name a new app gets: the project and the platform, and
// for one of several builds its store, or its variants where two builds
// share a store.
func appName(p *project.Project, t *project.Target, b project.Build) string {
	base := filepath.Base(p.Root)
	platform := platformNames[t.Platform]
	if !t.SeveralBuilds() {
		return fmt.Sprintf("%s (%s)", base, platform)
	}
	shared := 0
	for _, other := range t.Builds {
		if other.TenjinPlatform == b.TenjinPlatform {
			shared++
		}
	}
	if shared > 1 {
		return fmt.Sprintf("%s (%s, %s)", base, platform, strings.Join(b.Variants, ", "))
	}
	return fmt.Sprintf("%s (%s, %s)", base, platform, project.StoreName(b.TenjinPlatform))
}

// keyCheck is how SDK keys are checked against Tenjin, or nil when offline.
func (s *setup) keyCheck() verify.KeyChecker {
	if s.offline {
		return nil
	}
	return s.env.trackClient()
}

// sdkKeys settles which SDK key each build will use. A key can already be in
// the project, be passed in, be fetched from Tenjin, or be copied from the
// dashboard by the developer, who is told exactly where; a key is checked
// against the build's own app as soon as it is known, so a wrong one is
// caught here and not after a build.
func (s *setup) sdkKeys(p *project.Project) error {
	if n := len(p.Builds()); n > 1 {
		s.say("Each app has its own SDK key, so this project needs %d.", n)
	}
	report := s.verify(p)
	for i := range p.Targets {
		t := &p.Targets[i]
		for _, b := range t.Builds {
			if err := s.buildKey(t, b, report); err != nil {
				return err
			}
		}
	}
	// A coding agent may have left placeholders in an earlier run.
	return s.fillKeys(p, false)
}

// buildKey settles one build's key.
func (s *setup) buildKey(t *project.Target, b project.Build, report *verify.Report) error {
	label := buildTitle(t, b)
	// A key written in the project that Tenjin accepts settles it.
	if f := accepted(report, t, b); f != nil && f.File != "" {
		s.say("  %s %s: the project already holds a key Tenjin accepts (%s)", s.style.Green("✓"), label, f.File)
		s.inProject[b.ID] = true
		return nil
	}
	if given, ok := s.given.For(b.ID); ok {
		verdict := checkGivenKey(s.cmd.Context(), s.keyCheck(), given.Value, b, s.keyURLs[b.ID] != "")
		s.asker().verdict(label+", key "+maskKey(given.Value)+" from "+given.Source, verdict)
		if verdict.ok {
			s.keys[b.ID] = given
		}
		return nil
	}
	if id := s.appIDs[b.ID]; id != "" {
		fetched, note := keyFromTenjin(s.cmd.Context(), s.env, s.keyCheck(), id, b)
		if fetched != nil {
			s.asker().verdict(fetchedLabel(label, fetched), fetched.verdict)
			s.keys[b.ID] = verify.GivenKey{Value: fetched.key.Value, Source: "Tenjin"}
			return nil
		}
		if note != "" {
			s.say("  %s %s: %s", s.style.Yellow("!"), label, note)
		}
	}

	asker := s.asker()
	asker.instructions(label, b, s.keyURLs[b.ID])
	if !s.env.interactive {
		s.say("    Then pass it with --sdk-key %s=<key>.", b.ID)
		return nil
	}
	key, err := asker.ask(label, b, s.keyURLs[b.ID] != "")
	if err != nil {
		return err
	}
	if key != "" {
		s.keys[b.ID] = verify.GivenKey{Value: key, Source: "the key you pasted"}
	}
	return nil
}

// asker is setup's way of asking for a key.
func (s *setup) asker() keyAsker {
	return keyAsker{env: s.env, out: s.out, style: s.style, ctx: s.cmd.Context(), check: s.keyCheck()}
}

// accepted returns the finding that says Tenjin accepts a build's key.
func accepted(report *verify.Report, t *project.Target, b project.Build) *verify.Finding {
	build := ""
	if t.SeveralBuilds() {
		build = b.ID
	}
	for i := range report.Findings {
		if f := &report.Findings[i]; f.ID == "key.valid" && f.Platform == t.Platform && f.Build == build {
			return f
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// givenForChecks is the keys the checks use. A key written in the project
// is what the app will send, so it is the one checked; the keys settled in
// step 4 fill in where there is none.
func (s *setup) givenForChecks() verify.GivenKeys {
	given := verify.GivenKeys{All: s.given.All, ByBuild: map[string]verify.GivenKey{}, PreferSource: true}
	for id, key := range s.given.ByBuild {
		given.ByBuild[id] = key
	}
	for id, key := range s.keys {
		if _, ok := given.ByBuild[id]; !ok {
			given.ByBuild[id] = key
		}
	}
	return given
}

// verify runs the doctor checks on the project as it is now.
func (s *setup) verify(p *project.Project) *verify.Report {
	opts := verify.Options{Given: s.givenForChecks(), Features: s.features}
	if keys := s.keyCheck(); keys != nil {
		opts.Keys = keys
	}
	if !s.offline {
		opts.Latest = s.env.latestSDKs(s.cmd.Context())
	}
	return verify.Run(s.cmd.Context(), p, opts)
}

// integrate gets the SDK into the project: through the developer's coding
// agent when they agree to it, otherwise by pointing at the guide.
func (s *setup) integrate(p *project.Project) error {
	report := s.verify(p)
	if report.Summary.Errors == 0 && report.Summary.Warnings == 0 {
		s.say("The Tenjin SDK is already set up in this project.")
		return nil
	}
	s.say("The Tenjin SDK is not fully set up in this project yet. %s", outstanding(report.Summary))

	helper, err := s.chooseAgent()
	if err != nil {
		return err
	}
	if helper == nil {
		s.manualGuide(report)
		return nil
	}
	s.helper = helper
	s.runAgent(p, report)
	return nil
}

// chooseAgent decides whether a coding agent does the integration. It
// returns nil when the developer will do it by hand.
func (s *setup) chooseAgent() (*agent.Agent, error) {
	if s.agentFlag == "none" || agent.InsideAgent(s.env.getenv) {
		return nil, nil
	}
	installed := agent.Find(s.env.lookPath)
	if s.agentFlag != "" {
		for i := range installed {
			if installed[i].ID == s.agentFlag {
				return &installed[i], nil
			}
		}
		return nil, clierr.New(clierr.NotFound, "--agent %s was given, but it is not installed on this machine", s.agentFlag)
	}
	if !s.env.interactive || len(installed) == 0 {
		return nil, nil
	}
	if len(installed) > 1 {
		return s.pickAgent(installed)
	}

	a := &installed[0]
	s.say("\n%s is installed on this machine and can do the integration for you:", a.Name)
	s.say("  - it works in this project only, under your own %s account;", a.Name)
	s.say("  - %s;", a.Limits())
	s.say("  - the next step checks its work.")
	ok, err := s.env.confirm(s.out, fmt.Sprintf("\nIntegrate the Tenjin SDK using %s?", a.Name), true)
	if err != nil || !ok {
		return nil, err
	}
	return a, nil
}

// pickAgent asks which of several installed agents should do the work.
// Enter takes the first; anything that is not a choice takes none.
func (s *setup) pickAgent(installed []agent.Agent) (*agent.Agent, error) {
	names := make([]string, len(installed))
	for i, a := range installed {
		names[i] = a.Name
	}
	s.say("\n%s are installed on this machine. Either can do the integration for you, in this project only and under your own account:",
		strings.Join(names, " and "))
	for i, a := range installed {
		s.say("  %d) %s: %s", i+1, s.style.Bold(a.Name), a.Limits())
	}
	s.say("The next step checks the work, whichever you pick.")

	options := make([]string, len(installed))
	for i, a := range installed {
		options[i] = fmt.Sprintf("%s (%d)", a.Name, i+1)
	}
	answer, err := s.env.ask(s.out, fmt.Sprintf("\nIntegrate the Tenjin SDK using %s, or neither (n)? [1]", strings.Join(options, ", ")))
	if err != nil {
		return nil, err
	}
	answer = strings.ToLower(answer)
	for i := range installed {
		a := &installed[i]
		if answer == fmt.Sprint(i+1) || answer == a.ID || answer == strings.ToLower(a.Name) || (answer == "" && i == 0) {
			return a, nil
		}
	}
	return nil, nil
}

// manualGuide says what is left to add to the project and where, for a
// developer doing the integration by hand.
func (s *setup) manualGuide(report *verify.Report) {
	p := report.Project
	parts := verify.Guide(report)
	width := render.TextWidth(s.out)
	if len(parts) > 0 {
		s.say("To integrate it yourself:")
	}
	for _, part := range parts {
		heading := "In the app's code"
		if part.Platform != "" {
			heading = platformNames[part.Platform]
		}
		if part.File != "" {
			heading += " (the app starts in " + part.File + ")"
		}
		s.say("  %s", s.style.Bold(heading))
		for i, step := range part.Steps {
			s.say("%s", render.Indent(fmt.Sprintf("%d. %s", i+1, step), 4, width))
		}
	}
	if len(parts) > 0 {
		var placeholders []string
		for i := range p.Targets {
			for _, b := range p.Targets[i].Builds {
				placeholders = append(placeholders, b.SDKKeyPlaceholder+" for "+buildTitle(&p.Targets[i], b))
			}
		}
		if len(placeholders) == 0 {
			for _, t := range p.Targets {
				placeholders = append(placeholders, keyfill.Placeholder(t.Platform)+" for "+platformNames[t.Platform])
			}
		}
		text := "Where a step says <SDK_KEY>, write " + strings.Join(placeholders, " and ") + " exactly. The next step writes the key from step 4 in its place, so you never handle it."
		if len(p.Builds()) > len(p.Targets) {
			text += " Each build must read its own key: for Android flavors, a buildConfigField in each flavor's block, on a line that says Tenjin, is the usual place."
		}
		s.say("%s", render.Indent(text, 2, width))
	}
	if len(s.features) > 1 {
		s.say("%s", render.Indent("Also integrate the features chosen in step 1: "+featureList(s.features[1:])+". Each has its own section in the guide.", 2, width))
	}
	s.say("The full guide:")
	if p.Framework == project.Native {
		for _, t := range p.Targets {
			s.say("  %s: %s", platformNames[t.Platform], nativeGuides[t.Platform])
		}
	} else {
		s.say("  %s", guideURLs[p.Framework])
	}
	s.say("The next step lists everything that is missing, with the fix for each item.")
}

// runAgent hands the open findings to the coding agent and reports what it
// did. A failure here is not fatal: the check that follows is what decides
// whether the integration works.
func (s *setup) runAgent(p *project.Project, report *verify.Report) {
	toolDir := ""
	if exe, err := s.env.executable(); err == nil {
		toolDir = filepath.Dir(exe)
	}
	task := agent.Task{Dir: p.Root, Prompt: integrationPrompt(p, report, s.inProject, s.features, s.helper.DoctorCommand()), ToolDir: toolDir}

	s.say("\n%s is working on the integration. This can take a few minutes.", s.helper.Name)
	before := project.Fingerprint(p.Root)
	result, runErr := s.helper.Run(s.cmd.Context(), task, func(a agent.Action) {
		s.say("  %s %s %s", s.style.Dim("·"), a.Verb, a.Detail)
	})
	changed := project.Changed(before, project.Fingerprint(p.Root))

	if runErr != nil {
		s.say("%s %s did not finish: %v", s.style.Yellow("!"), s.helper.Name, runErr)
	}
	if len(changed) == 0 {
		s.say("\nNo files were changed.")
	} else {
		s.say("\n%s changed:", plural(len(changed), "file"))
		for _, f := range changed {
			s.say("  %s", f)
		}
	}
	if result.Summary != "" {
		s.say("\n%s", s.style.Bold(s.helper.Name+" reports:"))
		// The agent is never given a key, but it can read one already in
		// the project; anything shaped like a key is masked.
		summary := keyLike.ReplaceAllStringFunc(result.Summary, maskKey)
		for _, line := range strings.Split(summary, "\n") {
			s.say("  %s", line)
		}
	}
	if result.Denied > 0 {
		s.say("\n%s It attempted %s it is not allowed, and was refused.", s.style.Yellow("!"), plural(result.Denied, "action"))
	}
	// A run that failed left nothing worth going back to.
	if runErr == nil && result.SessionID != "" {
		s.say("\nTo ask %s about these changes or to keep going, run this in %s:", s.helper.Name, p.Root)
		s.say("  %s", s.style.Accent(s.helper.ResumeCommand(result.SessionID)))
	}
	// The agent left placeholders where the keys go; the keys go in now.
	if err := s.fillKeys(p, false); err != nil {
		s.say("%s %v", s.style.Yellow("!"), err)
	}
}

// fillKeys writes the SDK keys setup knows where the project has
// placeholders for them. With ask, it first asks at the terminal for the key
// of any placeholder it has no key for.
func (s *setup) fillKeys(p *project.Project, ask bool) error {
	keys := map[string]string{}
	builds, _ := placeholderBuilds(p, keyfill.Find(p.Root))
	// Only keys settled in step 4 are written: each was checked against its
	// own build's app there, and one Tenjin refused never gets this far.
	for _, pb := range builds {
		if key := s.keys[pb.build.ID]; key.Value != "" {
			keys[pb.build.SDKKeyPlaceholder] = key.Value
		}
	}
	if ask && s.env.interactive {
		for _, pb := range builds {
			if keys[pb.build.SDKKeyPlaceholder] != "" {
				continue
			}
			asker := s.asker()
			url := s.keyURLs[pb.build.ID]
			asker.instructions(pb.title, pb.build, url)
			key, err := asker.ask(pb.title, pb.build, url != "")
			if err != nil {
				return err
			}
			if key != "" {
				keys[pb.build.SDKKeyPlaceholder] = key
				s.keys[pb.build.ID] = verify.GivenKey{Value: key, Source: "the key you pasted"}
			}
		}
	}
	filled, err := keyfill.Fill(p.Root, keys)
	for _, spot := range filled {
		s.say("  %s Wrote %s at %s:%d", s.style.Green("✓"), keyNoun(p, spot), spot.File, spot.Line)
	}
	if err != nil {
		return fmt.Errorf("could not write the SDK keys: %w", err)
	}
	return nil
}

// check runs the doctor checks, and in a terminal repeats them after each
// round of changes until nothing would stop events reaching Tenjin.
func (s *setup) check() error {
	for round := 0; ; round++ {
		// Read the project again each time: the point of the loop is that
		// its files have changed.
		p, err := loadProject(s.args)
		if err != nil {
			return err
		}
		if err := narrowBuilds(p, s.chosen); err != nil {
			return err
		}
		// A placeholder whose key was skipped earlier is asked about again
		// once the developer comes back to check.
		if err := s.fillKeys(p, round > 0); err != nil {
			return err
		}
		report := s.verify(p)
		if err := writeReport(s.out, report, s.offline); err != nil {
			return err
		}
		status := reportStatus(report.Summary, false)
		if status == nil {
			s.say("\n%s Nothing here would stop events reaching Tenjin.", s.style.Green("✓"))
			return nil
		}
		if !s.env.interactive {
			return status
		}

		question := "\nFix the items marked ✗, then press Enter to check again (q to stop)."
		if s.helper != nil {
			question = fmt.Sprintf("\nPress Enter to check again, a to continue with %s, or q to stop.", s.helper.Name)
		}
		answer, err := s.env.ask(s.out, question)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		switch {
		case errors.Is(err, io.EOF), strings.EqualFold(answer, "q"):
			return status
		case strings.EqualFold(answer, "a") && s.helper != nil:
			s.runAgent(p, report)
		}
	}
}

// integrationPrompt is the task given to the coding agent: the doctor's open
// findings, each with its fix, and the rules for the work. The agent is
// never given an SDK key: it writes a placeholder, which the CLI fills.
func integrationPrompt(p *project.Project, report *verify.Report, inProject map[string]bool, features []string, doctor string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are adding the Tenjin SDK to the %s project in the current directory.\n\n", frameworkNames[p.Framework])

	b.WriteString("Targets:\n")
	several := false
	for _, t := range p.Targets {
		if !t.SeveralBuilds() {
			key := "write the string " + keyfill.Placeholder(t.Platform) + " exactly, as the key's value, where the key belongs. The Tenjin CLI writes the real key in its place when you finish"
			if len(t.Builds) == 1 && inProject[t.Builds[0].ID] {
				key = "already in the project: leave it as it is"
			}
			fmt.Fprintf(&b, "- %s, bundle ID %s, SDK key: %s\n", t.Platform, t.BundleID, key)
			continue
		}
		several = true
		fmt.Fprintf(&b, "- %s, with %s that are separate apps in Tenjin, each with its own SDK key:\n", t.Platform, plural(len(t.Builds), "build"))
		for _, build := range t.Builds {
			key := "write the string " + build.SDKKeyPlaceholder + " exactly, as the key's value, where this build's key belongs"
			if inProject[build.ID] {
				key = "already in the project: leave it as it is"
			}
			fmt.Fprintf(&b, "  - variants %s, bundle ID %s, %s: %s\n", strings.Join(build.Variants, ", "), build.BundleID, project.StoreName(build.TenjinPlatform), key)
		}
	}
	if several {
		b.WriteString("Each build must send its own key. For Android product flavors, put each placeholder in its flavor's block of the build file, for example buildConfigField(\"String\", \"TENJIN_SDK_KEY\", \"\\\"<placeholder>\\\"\") in the Kotlin DSL, and read BuildConfig.TENJIN_SDK_KEY where the SDK starts; in a Flutter app, choose by appFlavor from package:flutter/services.dart. Keep the word Tenjin on each line that holds a placeholder, so the checks can find the key there later. The Tenjin CLI writes the real keys in their places when you finish.\n")
	}
	if len(features) > 0 {
		b.WriteString("\nFeatures to integrate, as the developer chose them:\n")
		for _, id := range features {
			fmt.Fprintf(&b, "- %s", project.FeatureName(id))
			if f := p.Feature(id); f != nil && len(f.Evidence) > 0 {
				var where []string
				for _, e := range f.Evidence {
					where = append(where, e.What+" in "+e.File)
				}
				fmt.Fprintf(&b, ": the project has %s; send it to Tenjin where that code runs", strings.Join(where, ", "))
			}
			b.WriteString("\n")
		}
		b.WriteString("Integrate these and no others. Each has its own section in the guide below.\n")
	}
	if name, err := guideFor(p); err == nil {
		fmt.Fprintf(&b, "\nThe integration guide for this project, matched to the checks: run `tenjin sdk guide %s`. Read it before you change anything.\n", name)
	} else if p.Framework == project.Native {
		b.WriteString("\nThe integration guides, matched to the checks: run `tenjin sdk guide android` and `tenjin sdk guide ios`. Read them before you change anything.\n")
	}
	b.WriteString("\n`tenjin doctor` checked the project and found the items below. Resolve every one. Each names the file it concerns and the fix.\n\n")
	n := 0
	for _, f := range report.Findings {
		// Whether Tenjin accepts a key, and writing the key, are not the
		// agent's to do.
		// Turning on the iOS SDK's logs in debug builds stops nothing, but
		// it is what lets setup check the launch, so the agent does it too.
		wanted := f.Severity == verify.Error || f.Severity == verify.Warning || (f.ID == "ios.debug_logs" && f.Fix != "")
		if !wanted || strings.HasPrefix(f.ID, "key.") || f.ID == "sdk.key.unfilled" {
			continue
		}
		n++
		fmt.Fprintf(&b, "%d. [%s] %s", n, f.Severity, f.Title)
		if f.File != "" {
			where := f.File
			if f.Line > 0 {
				where = fmt.Sprintf("%s:%d", f.File, f.Line)
			}
			fmt.Fprintf(&b, " (%s)", where)
		}
		b.WriteString("\n")
		if f.Detail != "" {
			fmt.Fprintf(&b, "   %s\n", f.Detail)
		}
		if f.Fix != "" {
			fmt.Fprintf(&b, "   Fix: %s\n", f.Fix)
		}
	}

	b.WriteString(`
Rules:
- Work only inside this directory. Do not commit, do not run the app, do not install or download anything, and run no command that changes anything other than the project's files.
- You are not given the SDK keys, on purpose. Never look for, ask for or write a real key. Where a key belongs, write its placeholder as above; in a project with two platforms, choose between the placeholders at runtime.
- Make the smallest change that resolves each item, in the style the project already uses.
- A Tenjin app is one bundle ID on one platform, and an Android build for another store is an app of its own. Each app has its own SDK key, so two builds never share a placeholder.
- Where a fix needs a choice only the developer can make, such as the wording of the tracking prompt, pick a sensible default and list it at the end.
- When you think you are done, run ` + "`" + doctor + "`" + ` and read its findings. Repeat until none has severity "error" or "warning", apart from findings whose id starts with "key." and the "sdk.key.unfilled" findings for the placeholders, which are expected.
- Finish with a summary of at most six short lines, in plain text without Markdown: what you changed, and anything left for the developer. Do not tell them to write the SDK key or to run tenjin setup for it: this run of the Tenjin CLI writes the keys as soon as you finish.
`)
	return b.String()
}

// outstanding says how much is left to do, in words a person who has not
// seen the findings yet can follow.
func outstanding(s verify.Summary) string {
	blocking := plural(s.Errors, "thing") + " would stop events reaching Tenjin"
	switch {
	case s.Errors > 0 && s.Warnings > 0:
		return blocking + ", and " + pluralVerb(s.Warnings, "more change is", "more changes are") + " recommended."
	case s.Errors > 0:
		return blocking + "."
	}
	return pluralVerb(s.Warnings, "change is", "changes are") + " recommended."
}

func pluralVerb(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// keyLike matches text shaped like an SDK key: a long run of capitals and
// digits.
var keyLike = regexp.MustCompile(`\b[A-Z0-9]{24,}\b`)

// maskKey shows only the end of an SDK key.
func maskKey(key string) string {
	if len(key) <= 4 {
		return "…"
	}
	return "…" + key[len(key)-4:]
}
