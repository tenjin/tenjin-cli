package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tenjin/tenjin-cli/internal/agent"
	"github.com/tenjin/tenjin-cli/internal/clierr"
	"github.com/tenjin/tenjin-cli/internal/keyfill"
	"github.com/tenjin/tenjin-cli/internal/project"
	"github.com/tenjin/tenjin-cli/internal/render"
	"github.com/tenjin/tenjin-cli/internal/tenjinapi"
	"github.com/tenjin/tenjin-cli/internal/track"
	"github.com/tenjin/tenjin-cli/internal/verify"
)

// SDK keys are per app, and an app is one platform. This file is everything
// the commands share about them: how a key is passed in, where a person
// copies one from, and what Tenjin says about one.
//
// A key reaches the CLI in one of four ways, tried in this order: it is
// already written in the project, it is passed in, Tenjin's API serves it, or
// the developer copies it from the dashboard. The API does not serve keys on
// every server yet; where it does not, the dashboard is the way.
//
// A key is never given to a coding agent. The agent writes a placeholder and
// the CLI fills it (internal/keyfill).

const sdkKeyFlagHelp = "the app's SDK key; <key> alone is used for each platform with one build, Android's and iOS's alike, and checked against each one's app; where a platform has several builds, give one per build: android=<key>, ios=<key>, android:<variant>=<key> as tenjin project detect lists them (or set TENJIN_SDK_KEY, TENJIN_SDK_KEY_ANDROID, TENJIN_SDK_KEY_IOS, TENJIN_SDK_KEY_ANDROID_<VARIANT>)"

func addSDKKeyFlag(cmd *cobra.Command, into *[]string) {
	cmd.Flags().StringArrayVar(into, "sdk-key", nil, sdkKeyFlagHelp)
}

// keyPlatforms are the platforms a key can be given for, with the
// environment variable of each.
var keyPlatforms = []struct {
	platform project.Platform
	env      string
}{
	{project.Android, "TENJIN_SDK_KEY_ANDROID"},
	{project.IOS, "TENJIN_SDK_KEY_IOS"},
}

// givenKeys reads the SDK keys passed with --sdk-key and in the environment.
// A flag wins over the variable for the same build. Keys for builds other
// than a platform's main one are matched to the project in resolveGiven. A
// key from either is cleaned of what comes along when it is copied.
func givenKeys(flags []string, getenv func(string) string) (verify.GivenKeys, error) {
	given := verify.GivenKeys{ByBuild: map[string]verify.GivenKey{}}
	if key := cleanKey(getenv("TENJIN_SDK_KEY")); key != "" {
		given.All = verify.GivenKey{Value: key, Source: "TENJIN_SDK_KEY"}
	}
	for _, p := range keyPlatforms {
		if key := cleanKey(getenv(p.env)); key != "" {
			given.ByBuild[string(p.platform)] = verify.GivenKey{Value: key, Source: p.env}
		}
	}
	for _, value := range flags {
		name, key, perBuild := strings.Cut(value, "=")
		if !perBuild {
			given.All = verify.GivenKey{Value: cleanKey(value), Source: sdkKeyFlag}
			continue
		}
		name = strings.TrimSpace(name)
		platform, variant, _ := strings.Cut(name, ":")
		platform = strings.ToLower(platform)
		if platform != string(project.Android) && platform != string(project.IOS) {
			return given, clierr.New(clierr.Usage, "--sdk-key %s=…: unknown platform %q", name, platform).
				WithHint("Use --sdk-key android=<key>, --sdk-key ios=<key>, --sdk-key android:<variant>=<key>, or --sdk-key <key> for each platform with one build.")
		}
		id := platform
		if variant != "" {
			id += ":" + variant
		}
		given.ByBuild[id] = verify.GivenKey{Value: cleanKey(key), Source: sdkKeyFlag}
	}
	return given, nil
}

// resolveGiven matches the keys given for builds to the project's builds,
// and adds the environment variables of builds other than the main ones. A
// key given with --sdk-key for a build the project does not have is a usage
// error: it would otherwise be ignored without a word. One from the
// environment is simply not used, and said so with --verbose: a variable
// exported for another project is no mistake in this one.
func resolveGiven(given *verify.GivenKeys, p *project.Project, env *environment) error {
	known := map[string]string{}
	var names []string
	for _, b := range p.Builds() {
		known[project.Shout(b.ID)] = b.ID
		names = append(names, b.ID)
	}
	resolved := map[string]verify.GivenKey{}
	for id, key := range given.ByBuild {
		canonical, ok := known[project.Shout(id)]
		platform, main := project.Platform(id), !strings.Contains(id, ":")
		var why, hint string
		switch {
		case ok:
			resolved[canonical] = key
			continue
		case main && buildsUnread(p, platform):
			resolved[id] = key // a platform's main build, unread
			continue
		case main && p.Target(platform) != nil:
			// Several builds, none of them the main one: a key for each.
			why = fmt.Sprintf("this project's %s builds are separate apps in Tenjin, each with its own key", platformNames[platform])
			hint = "Give one per build: " + strings.Join(names, ", ") + "."
		case main:
			why = "this project does not build for " + platformNames[platform]
			hint = "Its builds: " + strings.Join(names, ", ") + "."
		default:
			why = fmt.Sprintf("this project has no build %q", id)
			hint = "Its builds: " + strings.Join(names, ", ") + "."
		}
		if key.Source != sdkKeyFlag {
			env.logf("%s is not used: %s", key.Source, why)
			continue
		}
		return clierr.New(clierr.Usage, "%s: %s", givenName(id, key), why).WithHint("%s", hint)
	}
	for _, b := range p.Builds() {
		if b.Main() {
			continue
		}
		if _, ok := resolved[b.ID]; ok {
			continue
		}
		if key := cleanKey(env.getenv(verify.KeyVariable(b.ID))); key != "" {
			resolved[b.ID] = verify.GivenKey{Value: key, Source: verify.KeyVariable(b.ID)}
		}
	}
	given.ByBuild = resolved
	return nil
}

// sdkKeyFlag is the source of a key given with the flag.
const sdkKeyFlag = "--sdk-key"

// givenName names a key given for a build by where it came from, for an
// error about it: the flag as it was written, or the variable.
func givenName(id string, key verify.GivenKey) string {
	if key.Source == sdkKeyFlag {
		return sdkKeyFlag + " " + id + "=…"
	}
	return key.Source
}

// givenFor returns the key given for a build, by the rule the checks use
// too (verify.GivenKeys.For): a key given without naming a build
// (--sdk-key <key>, TENJIN_SDK_KEY) serves a platform with one build only.
// note is why such a key was passed over, for the developer; otherwise "".
// target is nil for a platform whose builds could not be read, which stands
// for its one app.
func givenFor(given verify.GivenKeys, target *project.Target, b project.Build) (key verify.GivenKey, ok bool, note string) {
	if key, ok := given.For(target, b.ID); ok {
		return key, true, ""
	}
	if !given.PassedOver(target) {
		return verify.GivenKey{}, false, ""
	}
	return verify.GivenKey{}, false, fmt.Sprintf("the key from %s is not used for it: %s has %d builds, each an app of its own in Tenjin with its own key. Give this one's with --sdk-key %s=<key>, or in %s.",
		given.All.Source, platformNames[target.Platform], len(target.Builds), b.ID, verify.KeyVariable(b.ID))
}

// cleanKey removes what comes along when a key is copied: surrounding space
// and quotes.
func cleanKey(key string) string {
	return strings.Trim(strings.TrimSpace(key), `"'`)
}

// keyInstructions are the steps to copy an app's SDK key out of the
// dashboard. url is the app's SDK page, or empty when the app is not known.
func keyInstructions(url string) []string {
	open := `Open the app in the Tenjin dashboard and go to its "SDK" tab ("App & SDK" in the older layout).`
	if url != "" {
		open = "Open " + url
	}
	return []string{
		open,
		`In the "SDK Keys" card, copy the key with the button beside it.`,
		`If the card lists no key, choose "Generate SDK Key". If there is no such card, your role cannot see SDK keys: ask a manager of the organization.`,
	}
}

// keyVerdict is what Tenjin says about a key for one app, in words for the
// person who just supplied it.
type keyVerdict struct {
	// ok is false when the key will not work and another should be tried.
	ok      bool
	checked bool
	// later is true when the check could not be made now but can be made
	// again: Tenjin could not be reached, or gave an answer the CLI does not
	// know. What then becomes of the key is the command's to say
	// (keyAsker.later).
	later   bool
	message string
}

// checkGivenKey asks Tenjin whether a key is valid for an app. A check that
// cannot be made is not a refusal: the key is kept, unchecked. appKnown says
// the app was just found in, or added to, the signed-in organization, which
// narrows what a refusal can mean.
func checkGivenKey(ctx context.Context, keys verify.KeyChecker, key string, b project.Build, appKnown bool) keyVerdict {
	switch {
	case keys == nil:
		return keyVerdict{ok: true, message: "not checked against Tenjin (--offline)"}
	case b.BundleID == "":
		return keyVerdict{ok: true, message: "not checked: the bundle ID could not be read"}
	}
	result, err := keys.CheckKey(ctx, key, b.BundleID, b.TenjinPlatform)
	if err != nil {
		return keyVerdict{ok: true, later: true, message: fmt.Sprintf("could not reach Tenjin to check it (%v)", err)}
	}
	switch result.Status {
	case track.KeyValid:
		return keyVerdict{ok: true, checked: true, message: "Tenjin accepts this key for " + b.BundleID}
	case track.KeyRejected:
		return keyVerdict{message: "Tenjin does not accept this key for " + b.BundleID + ". It may be the key of another app: each app, one per platform and store, has its own."}
	case track.KeyNoApp:
		// The app exists in the organization that is signed in, so a key
		// whose organization lacks it is a key from somewhere else.
		if appKnown {
			return keyVerdict{message: fmt.Sprintf("This key is from a different organization than this app: Tenjin knows the key, but the organization it belongs to has no %s app with the bundle ID %s.", b.TenjinPlatform, b.BundleID)}
		}
		return keyVerdict{message: fmt.Sprintf("Tenjin knows this key, but the organization it belongs to has no %s app with the bundle ID %s.", b.TenjinPlatform, b.BundleID)}
	case track.KeyDisabled:
		return keyVerdict{message: "This key is disabled. Enable it in the dashboard, or copy another of the app's keys."}
	}
	return keyVerdict{ok: true, later: true, message: "Tenjin gave an unexpected answer (" + result.Detail + ")"}
}

// checksAgain ends setup's verdict on a key it keeps although Tenjin could
// not check it: setup checks again before it ends.
const checksAgain = "; the last step checks again"

// writtenUnchecked ends set-keys' verdict on such a key. set-keys has no
// later step, so it says what checks the key instead.
const writtenUnchecked = "; it is written unchecked, and 'tenjin doctor' checks it"

// fetchedKey is a key Tenjin's API served, with what Tenjin said
// about it.
type fetchedKey struct {
	key     tenjinapi.SDKKey
	verdict keyVerdict
}

// keyFromTenjin fetches an app's SDK keys and returns the first one Tenjin
// accepts for the target. With none, it returns a note saying why, for the
// developer; the note is empty when the API does not serve keys, since the
// dashboard is then simply where keys come from.
func keyFromTenjin(ctx context.Context, env *environment, check verify.KeyChecker, appID string, b project.Build) (*fetchedKey, string) {
	keys, err := env.api().ListSDKKeys(ctx, appID)
	if err != nil {
		switch {
		case errors.Is(err, tenjinapi.ErrKeysNotServed):
			return nil, ""
		case clierr.From(err).Kind == clierr.Permission:
			return nil, "your Tenjin role cannot read this app's SDK keys."
		}
		return nil, fmt.Sprintf("could not fetch the key from Tenjin (%s).", clierr.From(err).Message)
	}
	if len(keys) == 0 {
		return nil, "the app has no SDK key yet."
	}
	var refused keyVerdict
	for _, k := range keys {
		verdict := checkGivenKey(ctx, check, k.Value, b, true)
		if verdict.ok {
			return &fetchedKey{key: k, verdict: verdict}, ""
		}
		refused = verdict
	}
	return nil, "Tenjin refused the app's own key: " + refused.message
}

// fetchedLabel describes a fetched key without showing it.
func fetchedLabel(label string, f *fetchedKey) string {
	return fmt.Sprintf("%s, key %s (%q) from Tenjin", label, maskKey(f.key.Value), f.key.Name)
}

// keyAsker gets a platform's SDK key from the developer at the terminal: it
// says where to copy the key from, takes what is pasted, and asks Tenjin
// about it before accepting it.
type keyAsker struct {
	env   *environment
	out   io.Writer
	style render.Styler
	ctx   context.Context
	// check asks Tenjin about a key; nil when offline.
	check verify.KeyChecker
	// written is where the project holds a key Tenjin refuses for the
	// build, which no skip changes.
	written string
	// later ends the verdict on a key kept although Tenjin could not check
	// it (keyVerdict.later): what becomes of it.
	later string
}

func (k keyAsker) say(format string, a ...any) {
	_, _ = fmt.Fprintf(k.out, format+"\n", a...)
}

// instructions print where to copy a platform's key from. url is the app's
// SDK page, or empty when the app is not known.
func (k keyAsker) instructions(label string, b project.Build, url string) {
	k.say("\n  %s SDK key, for %s:", render.Clean(label), render.Clean(firstNonEmpty(b.BundleID, "this app")))
	for i, step := range keyInstructions(url) {
		k.say("%s", render.Indent(fmt.Sprintf("%d. %s", i+1, render.Clean(step)), 4, render.TextWidth(k.out)))
	}
}

// ask prompts until Tenjin accepts a key, or the developer goes on without
// one, which returns "". appKnown says the app was found in the signed-in
// organization.
func (k keyAsker) ask(label string, b project.Build, appKnown bool) (string, error) {
	for {
		answer, err := k.env.ask(k.out, fmt.Sprintf("    Paste the %s SDK key (Enter to skip for now):", label))
		if err != nil {
			return "", err
		}
		key := cleanKey(answer)
		if key == "" {
			if k.written != "" {
				k.say("    Skipped. The key written at %s stays, and Tenjin refuses it: replace it there with the app's own key.", k.written)
			} else {
				k.say("    Skipped. The placeholder stays where the key belongs, and the checks will say so.")
			}
			return "", nil
		}
		// Something that cannot be a key is not sent to Tenjin.
		if !verify.LooksLikeKey(key) {
			k.say("    That is not an SDK key: those are 24 to 64 capital letters and digits. Copy it again, or press Enter to skip.")
			continue
		}
		verdict := checkGivenKey(k.ctx, k.check, key, b, appKnown)
		k.verdict("  "+label+", key "+maskKey(key), verdict)
		if verdict.ok {
			return key, nil
		}
		k.say("    Copy it again from the page above, or press Enter to skip.")
	}
}

func (k keyAsker) verdict(what string, v keyVerdict) {
	mark := k.style.Green("✓")
	switch {
	case !v.ok:
		mark = k.style.Red("✗")
	case !v.checked:
		mark = k.style.Yellow("!")
	}
	message := v.message
	if v.later {
		message += k.later
	}
	k.say("  %s %s: %s", mark, render.Clean(what), render.Clean(message))
}

// setKeysResult is what `project set-keys` reports.
type setKeysResult struct {
	Filled []keyfill.Spot `json:"filled"`
	// Missing are the builds whose placeholder is left, for want of a key;
	// Refused, those whose key Tenjin did not accept. Both hold build IDs.
	Missing []string `json:"missing"`
	Refused []string `json:"refused"`
	// Unknown are placeholders that name no build of the project.
	Unknown []string `json:"unknown,omitempty"`
	// Blocked are placeholders in files the CLI does not write to, each
	// with why (keyfill.Spot.Blocked).
	Blocked []keyfill.Spot `json:"blocked,omitempty"`
}

func newProjectSetKeysCmd(opts *options, env *environment) *cobra.Command {
	var (
		sdkKeys []string
		offline bool
	)
	cmd := &cobra.Command{
		Use:   "set-keys [path]",
		Short: "Write SDK keys where a coding agent left placeholders for them",
		Long: `Write each build's SDK key where a placeholder stands for it
(` + keyfill.Placeholder(project.Android) + `, ` + keyfill.Placeholder(project.IOS) + `, or
for another build ` + project.Placeholder("android:amazon") + ` and the like, as
'tenjin project detect' lists them), and change nothing else in the
project. A coding agent integrating Tenjin is never given an SDK key, so it
writes these placeholders instead.

Each key comes from, in this order:

  1. --sdk-key, or the environment
  2. Tenjin, when you are signed in
  3. at a terminal, pasted after the CLI says where to copy it from

A key given without naming a build (--sdk-key <key>, TENJIN_SDK_KEY) is
used for each platform with one build, Android's and iOS's alike, and checked
against each one's app. Where a platform has several builds, it is used for
none of them: each is an app of its own with its own key.

It is checked with Tenjin first, against that build's own app, and a key
Tenjin refuses is not written. One Tenjin cannot check, with --offline or
when Tenjin cannot be reached, is written unchecked, and 'tenjin doctor'
checks it. The keys are never printed whole. set-keys asks for no
confirmation: it writes only over its own placeholders. Run it yourself,
not through a coding agent: once written, a key is in a file the agent
reads.`,
		Args: maxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := opts.format(cmd)
			if err != nil {
				return err
			}
			if agent.InsideAgent(env.getenv) {
				return clierr.New(clierr.Usage, "'tenjin project set-keys' does not run inside a coding agent: it writes the real SDK keys into the project, where the agent would read them").
					WithHint("Leave the placeholders, and have the developer run 'tenjin setup' or 'tenjin project set-keys' in their own terminal.")
			}
			given, err := givenKeys(sdkKeys, env.getenv)
			if err != nil {
				return err
			}
			p, err := loadProject(args)
			if err != nil {
				return err
			}
			if err := resolveGiven(&given, p, env); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// The conversation with the developer goes to stderr when the
			// result is JSON, so stdout stays parseable.
			talk := out
			if format == render.JSON {
				talk = cmd.ErrOrStderr()
			}
			// A key Tenjin could not check is written all the same, as with
			// --offline, and the command does not fail for it: the placeholder
			// is gone, so a script that took a failure to mean nothing was
			// written would be wrong. The verdict says so instead.
			asker := keyAsker{env: env, out: talk, style: render.NewStyler(talk), ctx: cmd.Context(), later: writtenUnchecked}
			if !offline {
				asker.check = env.trackClient()
			}

			spots := keyfill.Find(p.Root)
			result := setKeysResult{Filled: []keyfill.Spot{}, Missing: []string{}, Refused: []string{}}
			keys := map[string]string{}
			for _, s := range spots {
				if s.Blocked != "" && !p.IsOmitted(s.Placeholder) {
					result.Blocked = append(result.Blocked, s)
				}
			}
			builds, unknown := placeholderBuilds(p, keyfill.Writable(spots))
			result.Unknown = unknown
			for _, pb := range builds {
				b, label := pb.build, pb.title
				passedIn, ok, passed := givenFor(given, pb.target, b)
				if passed != "" {
					asker.say("%s %s: %s", asker.style.Yellow("!"), render.Clean(label), render.Clean(passed))
				}
				if ok {
					verdict := checkGivenKey(cmd.Context(), asker.check, passedIn.Value, b, false)
					asker.verdict(label+", key "+maskKey(passedIn.Value)+" from "+passedIn.Source, verdict)
					if !verdict.ok {
						result.Refused = append(result.Refused, b.ID)
						continue
					}
					keys[b.SDKKeyPlaceholder] = passedIn.Value
					continue
				}
				app := signedInApp(cmd.Context(), env, b)
				if app != nil {
					fetched, note := keyFromTenjin(cmd.Context(), env, asker.check, app.ID, b)
					if fetched != nil {
						asker.verdict(fetchedLabel(label, fetched), fetched.verdict)
						keys[b.SDKKeyPlaceholder] = fetched.key.Value
						continue
					}
					if note != "" {
						asker.say("%s %s: %s", asker.style.Yellow("!"), render.Clean(label), render.Clean(note))
					}
				}
				if !env.interactive {
					result.Missing = append(result.Missing, b.ID)
					continue
				}
				url := ""
				if app != nil {
					url = env.endpoints.SDKKeysURL(app.ID)
				}
				asker.instructions(label, b, url)
				key, err := asker.ask(label, b, false)
				if err != nil {
					return err
				}
				if key == "" {
					result.Missing = append(result.Missing, b.ID)
					continue
				}
				keys[b.SDKKeyPlaceholder] = key
			}

			filled, err := keyfill.Fill(p.Root, keys)
			result.Filled = append(result.Filled, filled...)
			if err != nil {
				return clierr.Wrap(clierr.Internal, err, "could not write the keys: %v", err)
			}
			if format == render.JSON {
				if err := render.WriteJSON(out, result); err != nil {
					return err
				}
			} else {
				writeFilled(out, render.NewStyler(out), p, spots, result)
			}
			return setKeysStatus(p, result)
		},
	}
	addSDKKeyFlag(cmd, &sdkKeys)
	cmd.Flags().BoolVar(&offline, "offline", false, "write the keys without checking them with Tenjin first")
	return cmd
}

// placeholderBuild is a build that a placeholder in the project stands for.
type placeholderBuild struct {
	build  project.Build
	target *project.Target
	title  string
}

// buildsUnread reports whether the project builds for the platform but its
// builds could not be read, so that its main placeholder and an unqualified
// key stand for its one app. A platform the project does not build for has
// no build to stand for.
func buildsUnread(p *project.Project, platform project.Platform) bool {
	t := p.Target(platform)
	return t != nil && len(t.Builds) == 0
}

// placeholderBuilds returns the builds whose placeholders are in the
// project, in project order, and the placeholders that name no build.
func placeholderBuilds(p *project.Project, spots []keyfill.Spot) (found []placeholderBuild, unknown []string) {
	present := map[string]bool{}
	for _, s := range spots {
		present[s.Placeholder] = true
	}
	matched := map[string]bool{}
	for i := range p.Targets {
		t := &p.Targets[i]
		for _, b := range t.Builds {
			if present[b.SDKKeyPlaceholder] {
				found = append(found, placeholderBuild{build: b, target: t, title: buildTitle(t, b)})
				matched[b.SDKKeyPlaceholder] = true
			}
		}
	}
	for _, s := range spots {
		if matched[s.Placeholder] || p.IsOmitted(s.Placeholder) {
			continue
		}
		matched[s.Placeholder] = true
		// A platform's main placeholder in a project whose builds could not
		// be read: its key can still be written. Where they were, it names
		// none of them; nor does one for a platform the project lacks.
		if s.Placeholder == keyfill.Placeholder(s.Platform) && buildsUnread(p, s.Platform) {
			id := string(s.Platform)
			b := project.Build{ID: id, TenjinPlatform: id, SDKKeyPlaceholder: s.Placeholder}
			found = append(found, placeholderBuild{build: b, title: platformNames[s.Platform]})
			continue
		}
		unknown = append(unknown, s.Placeholder)
	}
	return found, unknown
}

// buildTitle names a build for people: "Android" for a target's only build,
// "Android, amazon (Amazon Appstore)" where it has several.
func buildTitle(t *project.Target, b project.Build) string {
	name := platformNames[project.BuildPlatform(b.ID)]
	if t == nil || !t.SeveralBuilds() {
		return name
	}
	variant := strings.Join(b.Variants, ", ")
	if variant == "" {
		variant = b.ID
	}
	// Variants are named in the project's files.
	return render.Clean(fmt.Sprintf("%s, %s (%s)", name, variant, project.StoreName(b.TenjinPlatform)))
}

// signedInApp is the Tenjin app for a build, when the developer is signed
// in and the app exists; otherwise nil.
func signedInApp(ctx context.Context, env *environment, b project.Build) *tenjinapi.App {
	if b.BundleID == "" {
		return nil
	}
	if status, err := env.session().Status(); err != nil || !status.SignedIn {
		return nil
	}
	app, err := env.api().FindApp(ctx, b.BundleID, b.TenjinPlatform)
	if err != nil {
		return nil
	}
	return app
}

// titleOf names a build ID for people, from the project's builds.
func titleOf(p *project.Project, id string) string {
	if t, b := p.Build(id); b != nil {
		return buildTitle(t, *b)
	}
	return platformNames[project.BuildPlatform(id)]
}

func writeFilled(w io.Writer, style render.Styler, p *project.Project, spots []keyfill.Spot, r setKeysResult) {
	if len(spots) == 0 {
		_, _ = fmt.Fprintf(w, "No SDK key placeholders in %s.\n", render.Clean(p.Root))
		return
	}
	// Names and paths come from the project, which may hold escape codes.
	for _, s := range r.Filled {
		_, _ = fmt.Fprintf(w, "%s Wrote %s at %s:%d\n", style.Green("✓"), render.Clean(keyNoun(p, s)), render.Clean(s.File), s.Line)
	}
	for _, id := range r.Missing {
		_, _ = fmt.Fprintf(w, "%s The placeholder of %s is left: no key was given for it.\n", style.Yellow("!"), render.Clean(titleOf(p, id)))
	}
	for _, id := range r.Refused {
		_, _ = fmt.Fprintf(w, "%s The placeholder of %s is left: Tenjin refused the key.\n", style.Red("✗"), render.Clean(titleOf(p, id)))
	}
	for _, ph := range r.Unknown {
		_, _ = fmt.Fprintf(w, "%s %s names no build of this project, so nothing was written for it. tenjin project detect lists the builds.\n", style.Red("✗"), render.Clean(ph))
	}
	for _, s := range r.Blocked {
		_, _ = fmt.Fprintf(w, "%s %s at %s:%d is left: %s. Write the key there yourself.\n", style.Red("✗"), render.Clean(s.Placeholder), render.Clean(s.File), s.Line, render.Clean(s.Blocked))
	}
}

// keyNoun names the SDK key a placeholder was for: "the Android SDK key",
// or "the SDK key of Android, amazon (Amazon Appstore)" for one of several
// builds.
func keyNoun(p *project.Project, s keyfill.Spot) string {
	for i := range p.Targets {
		t := &p.Targets[i]
		for _, b := range t.Builds {
			if b.SDKKeyPlaceholder == s.Placeholder && t.SeveralBuilds() {
				return "the SDK key of " + buildTitle(t, b)
			}
		}
	}
	return "the " + platformNames[s.Platform] + " SDK key"
}

// setKeysStatus fails the command when a placeholder is left.
func setKeysStatus(p *project.Project, r setKeysResult) error {
	switch {
	case len(r.Refused) > 0:
		return clierr.New(clierr.ChecksFailed, "Tenjin refused the key for %s; nothing was written for it", joinTitles(p, r.Refused))
	case len(r.Blocked) > 0:
		s := r.Blocked[0]
		return clierr.New(clierr.ChecksFailed, "the key cannot be written at %s:%d: %s", s.File, s.Line, s.Blocked).
			WithHint("Write the key there yourself, from the app's page in the Tenjin dashboard.")
	case len(r.Unknown) > 0:
		return clierr.New(clierr.Usage, "%s names no build of this project", strings.Join(r.Unknown, ", ")).
			WithHint("Replace it with the placeholder of the build it is for; tenjin project detect lists them.")
	case len(r.Missing) > 0:
		// Not a misuse: the key is simply not to be had here. The developer
		// gives it to setup, which asks; a coding agent is never to.
		return clierr.New(clierr.NotFound, "no SDK key for %s", joinTitles(p, r.Missing)).
			WithHint("Run 'tenjin setup', which asks for it, or run this at a terminal to paste it.")
	}
	return nil
}

func joinTitles(p *project.Project, ids []string) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = titleOf(p, id)
	}
	return strings.Join(names, " and ")
}
