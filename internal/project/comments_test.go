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
