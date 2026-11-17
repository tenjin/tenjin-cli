package verify

import (
	"strings"
	"testing"
)

// A receiver under the project's own name is rewritten to the SDK's, and
// the line breaks between the name and the call stay, so that every line
// after it keeps its number.
func TestRewritingAReceiverKeepsEveryLineBreak(t *testing.T) {
	for name, tc := range map[string]struct {
		normalize func(string) string
		in, call  string
	}{
		"android":            {normalizeAndroid, "val tenjin = TenjinSDK.getInstance(this, \"K\")\ntenjin\n    .connect()\nnext()\n", "TenjinSDK.connect("},
		"android, safe call": {normalizeAndroid, "val tenjin: TenjinSDK? = TenjinSDK.getInstance(this, \"K\")\ntenjin\n    ?.connect()\nnext()\n", "TenjinSDK.connect("},
		"android, not null":  {normalizeAndroid, "val tenjin: TenjinSDK? = TenjinSDK.getInstance(this, \"K\")\ntenjin!!\n    .connect()\nnext()\n", "TenjinSDK.connect("},
		"flutter":            {normalizeFlutter, "final tenjin = TenjinSDK.instance;\ntenjin\n    .connect();\nnext();\n", "TenjinSDK.instance.connect("},
		"react native":       {normalizeReactNative, "import sdk from 'react-native-tenjin';\nsdk\n  .connect();\nnext();\n", "Tenjin.connect("},
		"capacitor":          {normalizeCapacitor, "import { Tenjin as sdk } from 'ionic-capacitor-tenjin';\nsdk\n  .connect();\nnext();\n", "Tenjin.connect("},
		"unity":              {normalizeUnity, "BaseTenjin instance = Tenjin.getInstance(key);\ninstance\n    .Connect();\nNext();\n", "Tenjin.Connect("},
	} {
		t.Run(name, func(t *testing.T) {
			out := tc.normalize(tc.in)
			if !strings.Contains(out, tc.call) {
				t.Errorf("the call was not rewritten:\n%s", out)
			}
			if got, want := strings.Count(out, "\n"), strings.Count(tc.in, "\n"); got != want {
				t.Errorf("%d line breaks, want %d:\n%s", got, want, out)
			}
			if lineAt(out, strings.Index(out, "ext(")) != 4 {
				t.Errorf("the line after the call moved:\n%s", out)
			}
		})
	}
}
