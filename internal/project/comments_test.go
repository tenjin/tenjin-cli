package project

import (
	"strings"
	"testing"
)

func TestBlankComments(t *testing.T) {
	src := "val type = \"image/*\"\n" +
		"TenjinSDK.getInstance(this, \"KEY\") // tenjin.connect()\n" +
		"val url = \"https://track.tenjin.com\"\n" +
		"/** Opens the picker. */\n" +
		"tenjin.connect()\n" +
		"val c = '\"'\n" +
		"/* tenjin.connect()\n   still a comment */\n" +
		"val s = \"\"\"raw /* not a comment */\"\"\"\n"
	got := BlankComments(src)
	if strings.Count(got, "\n") != strings.Count(src, "\n") || len(got) != len(src) {
		t.Fatalf("lines or offsets moved:\n%s", got)
	}
	for _, kept := range []string{`"image/*"`, `TenjinSDK.getInstance(this, "KEY")`, `"https://track.tenjin.com"`, "tenjin.connect()\nval c", `"""raw /* not a comment */"""`} {
		if !strings.Contains(got, kept) {
			t.Errorf("lost %q:\n%s", kept, got)
		}
	}
	if strings.Count(got, "tenjin.connect()") != 1 || strings.Contains(got, "Opens the picker") {
		t.Errorf("a comment was kept:\n%s", got)
	}
}

// A regular expression literal is neither a comment nor a string, and what
// looks like one in a division is a division.
func TestBlankCommentsKnowsRegexLiterals(t *testing.T) {
	cases := []struct {
		name, src  string
		kept, lost []string
	}{
		{"a regex that would open a comment", "const base = API_URL.replace(/\\/*$/, '');\nTenjin.initialize({ sdkKey: 'KEY' });\n",
			[]string{"Tenjin.initialize({ sdkKey: 'KEY' })", "replace(/", "/, '')"}, []string{`\/*$`}},
		{"a division, then a comment", "const half = total / 2; /* note */ const x = 1; // c\ntenjin.connect();\n",
			[]string{"total / 2", "const x = 1", "tenjin.connect()"}, []string{"note", "// c"}},
		{"a regex holding slashes and a quote", "const re = /https?:\\/\\/'/g;\ntenjin.connect();\n",
			[]string{"tenjin.connect()", "/g;"}, []string{`https?:\/\/'`}},
		{"a character class holding a slash", "const parts = s.split(/[/\\\\]+/);\ntenjin.connect();\n",
			[]string{"tenjin.connect()", "split(/", "/);"}, []string{`[/\\]+`}},
		{"a regex after a keyword and a bracket", "function f(s) { return /^x/.test(s) }\nif (/y/i.test(s)) { tenjin.connect() }\n",
			[]string{"return /", "/.test(s)", "/i.test(s)", "tenjin.connect()"}, []string{"^x", "/y/"}},
		{"divisions", "const a = x / y / z;\nconst b = arr[0] / n;\nconst c = (a + b) / 2;\nconst d = i++ / 2;\nconst e = 'a' / 2;\n",
			[]string{"x / y / z", "arr[0] / n", "(a + b) / 2", "i++ / 2", "'a' / 2"}, nil},
		{"JSX tags", "<View>{a}</View><Button onPress={() => analytics.logEvent('tap')} />\n",
			[]string{"</View>", "analytics.logEvent('tap')", "/>"}, nil},
		{"Dart's integer division", "final x = a ~/ b; // half\nfinal y = c / d;\n",
			[]string{"a ~/ b", "c / d"}, []string{"half"}},
		{"a slash with no closing one", "const x = 1;\n/ 2\n", []string{"/ 2"}, nil},
	}
	for _, c := range cases {
		got := BlankComments(c.src)
		if strings.Count(got, "\n") != strings.Count(c.src, "\n") || len(got) != len(c.src) {
			t.Errorf("%s: lines or offsets moved:\n%s", c.name, got)
		}
		for _, kept := range c.kept {
			if !strings.Contains(got, kept) {
				t.Errorf("%s: lost %q:\n%s", c.name, kept, got)
			}
		}
		for _, lost := range c.lost {
			if strings.Contains(got, lost) {
				t.Errorf("%s: kept %q:\n%s", c.name, lost, got)
			}
		}
	}
}

func TestBlankStrings(t *testing.T) {
	for _, c := range []struct{ ext, src, want string }{
		{".kt", `Log.d("a", "x.logEvent(\"y\")"); a.logEvent("z")`, `Log.d(" ", "                 "); a.logEvent(" ")`},
		{".kt", "val s = \"\"\"\nx.logEvent(\"y\")\n\"\"\"\nx()", "val s = \"\"\"\n               \n\"\"\"\nx()"},
		{".dart", "const s = '''\nlog('x')\n''';", "const s = '''\n        \n''';"},
		{".ts", "f(`a ${b('c')}\nd`); g()", "f(`           \n `); g()"},
		{".cs", `var p = @"C:\dir\""q"; f()`, `var p = @"          "; f()`},
	} {
		got := BlankStrings(c.src, c.ext)
		if got != c.want {
			t.Errorf("%s %q:\ngot  %q\nwant %q", c.ext, c.src, got, c.want)
		}
	}
}
