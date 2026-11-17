package verify

import "testing"

// The user ID chosen is the expression the app passes as a whole: only
// spaces between tokens and parentheses around all of it are left out.
func TestSameExpression(t *testing.T) {
	for _, tc := range []struct {
		passed, chosen string
		same           bool
	}{
		{"user.id", "user.id", true},
		{" user . id ", "user.id", true},
		{"(user.id)", "user.id", true},
		{"((user.id))", "user.id", true},
		{"( user.id )", "user.id", true},
		{"user.id", "(user.id)", true},
		{"account.getId()", "account.getId()", true},
		{"(account.getId())", "account.getId()", true},
		{"x as String", "x as String", true},
		{"x as String", "(x as String)", true},
		// Another value, or a transformation of it.
		{"previous.user.id", "user.id", false},
		{"(previous.user.id)", "user.id", false},
		{"user.id!!", "user.id", false},
		{"(user.id)!!", "user.id", false},
		{"(user).id", "user.id", false},
		{"(a).b(c)", "a).b(c", false},
		{"user.id.toString()", "user.id", false},
		{"xasString", "x as String", false},
		// Text in a string is the value: its spaces and parentheses count.
		{`user["account id"]`, `user["accountid"]`, false},
		{`("(" + id + ")")`, `"(" + id + ")"`, true},
		{`(")")`, `")"`, true},
		{`(") + (")`, `") + ("`, true},
		{`("(") + (")")`, `"(") + (")"`, false},
		{"(a) + (b)", "a) + (b", false},
	} {
		if got := sameExpression(tc.passed, tc.chosen); got != tc.same {
			t.Errorf("sameExpression(%q, %q) = %v, want %v", tc.passed, tc.chosen, got, tc.same)
		}
	}
}
