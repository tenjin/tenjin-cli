package verify

import (
	"regexp"
	"strings"

	"github.com/tenjin/tenjin-cli/internal/project"
)

// Each SDK's calls are recognised on its own receiver: TenjinSDK on native
// Android and iOS, TenjinSDK.instance in Flutter, Tenjin in the JavaScript
// wrappers. A project often holds that receiver under a name of its own: a
// variable set from getInstance, or an import under another name. Those
// names are rewritten to the canonical receiver before the calls are
// matched, so `tenjin.connect()` counts and `mediaBrowser.connect()` does
// not. Line breaks are never touched, so line numbers stay right.

var (
	// Kotlin `val tenjin = TenjinSDK.getInstance(…)`, `tenjin: TenjinSDK`
	// or `val tenjin by lazy { TenjinSDK.getInstance(…) }`, Java
	// `TenjinSDK tenjin = …`.
	androidAlias = regexp.MustCompile(`\b(\w+)\s*(?::\s*TenjinSDK\b[?!]?\s*)?=\s*TenjinSDK\.getInstance|\b(\w+)\s*:\s*TenjinSDK\b|\bTenjinSDK[ \t]+(\w+)[ \t]*[=;)]|\b(\w+)\s+by\s+lazy\s*\{\s*TenjinSDK\.getInstance`)
	// A function of the file's that returns the SDK: Kotlin
	// `fun sdk(…): TenjinSDK`, Java `TenjinSDK sdk(…)`.
	androidHelper = regexp.MustCompile(`\bfun\s+(\w+)\s*\([^()]*\)\s*:\s*TenjinSDK\b|\bTenjinSDK[ \t]+(\w+)[ \t]*\(`)
	// A call chained on getInstance: TenjinSDK.getInstance(…).connect().
	androidChained = regexp.MustCompile(`TenjinSDK\.getInstance\w*\((?:[^()]|\([^()]*\))*\)\s*\.connect\(`)
	flutterAlias   = regexp.MustCompile(`\b(\w+)\s*=\s*TenjinSDK\.instance\b`)
	// import Anything from 'react-native-tenjin', or require().
	reactNativeAlias = regexp.MustCompile(`import\s+(\w+)\s+from\s+['"]react-native-tenjin['"]|(\w+)\s*=\s*require\(\s*['"]react-native-tenjin['"]\s*\)`)
	capacitorAlias   = regexp.MustCompile(`\bTenjin\s+as\s+(\w+)`)
	// C# `BaseTenjin instance = …`, a BaseTenjin field or parameter, or
	// `var t = Tenjin.getInstance(…)`.
	unityAlias = regexp.MustCompile(`\bBaseTenjin\s+(\w+)\b|\b(\w+)\s*=\s*Tenjin\.getInstance`)
	// A call chained on getInstance, or on the component's instance.
	unityChained = regexp.MustCompile(`(?:\bTenjin\.getInstance\w*\((?:[^()]|\([^()]*\))*\)|\bGetTenjinInstance\(\s*\))\s*\.Connect\(`)
)

// aliases returns the names the pattern captures.
func aliases(pattern *regexp.Regexp, text string) []string {
	var names []string
	for _, m := range pattern.FindAllStringSubmatch(text, -1) {
		for _, name := range m[1:] {
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// rewrite replaces `name.` with `receiver.` for each name other than the
// receiver itself.
func rewrite(text string, names []string, receiver string) string {
	for _, name := range names {
		if name == receiver || strings.HasPrefix(receiver, name+".") {
			continue
		}
		text = regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\s*(?:\?|!!)?\.`).ReplaceAllString(text, receiver+".")
	}
	return text
}

// normalizeAndroid makes every connect on a Tenjin receiver read
// TenjinSDK.connect(, which is what the Android spec matches.
func normalizeAndroid(text string) string {
	text = androidChained.ReplaceAllStringFunc(text, func(chain string) string {
		// Only the end of the chain changes: getInstance and its key stay
		// as they are, for the key check to read.
		end := strings.LastIndex(chain, ".connect(")
		return chain[:end] + " TenjinSDK.connect("
	})
	return rewrite(text, append(aliases(androidAlias, text), helperAliases(text)...), "TenjinSDK")
}

// helperAliases are the names set from one of the file's own functions that
// return the SDK: `val sdk = sdk(context)`. One level is followed.
func helperAliases(text string) []string {
	helpers := aliases(androidHelper, text)
	if len(helpers) == 0 {
		return nil
	}
	quoted := make([]string, len(helpers))
	for i, h := range helpers {
		quoted[i] = regexp.QuoteMeta(h)
	}
	set := regexp.MustCompile(`\b(\w+)\s*(?::\s*TenjinSDK\b[?!]?\s*)?=\s*(?:` + strings.Join(quoted, "|") + `)\s*\(`)
	return aliases(set, text)
}

func normalizeFlutter(text string) string {
	return rewrite(text, aliases(flutterAlias, text), "TenjinSDK.instance")
}

func normalizeReactNative(text string) string {
	return rewrite(text, aliases(reactNativeAlias, text), "Tenjin")
}

func normalizeCapacitor(text string) string {
	return rewrite(text, aliases(capacitorAlias, text), "Tenjin")
}

// normalizeUnity makes every Connect on a Tenjin instance read
// Tenjin.Connect(, so that an IAP store's or a socket's Connect() is not
// taken for Tenjin's.
func normalizeUnity(text string) string {
	text = unityChained.ReplaceAllStringFunc(text, func(chain string) string {
		end := strings.LastIndex(chain, ".Connect(")
		return chain[:end] + " Tenjin.Connect("
	})
	return rewrite(text, aliases(unityAlias, text), "Tenjin")
}

func testSource(rel string) bool { return project.TestSource(rel) }
