package verify

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// release is a version number with the prerelease it may carry, as in
// 1.7.0-beta.1. A prerelease comes before its release.
type release struct {
	parts [3]int
	pre   string
}

// readRelease reads "1.7.0", "v1.7.0", "1.7" or "1.7.0-beta.1+build".
func readRelease(s string) (release, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	base, pre, _ := strings.Cut(s, "-")
	parts, ok := parseVersion(base)
	return release{parts, pre}, ok
}

func (r release) String() string {
	s := fmt.Sprintf("%d.%d.%d", r.parts[0], r.parts[1], r.parts[2])
	if r.pre != "" {
		s += "-" + r.pre
	}
	return s
}

// compare is -1, 0 or 1 as r comes before, with or after o.
func (r release) compare(o release) int {
	for n := range r.parts {
		if r.parts[n] != o.parts[n] {
			return sign(r.parts[n] - o.parts[n])
		}
	}
	switch {
	case r.pre == o.pre:
		return 0
	case r.pre == "":
		return 1
	case o.pre == "":
		return -1
	}
	// Prereleases compare field by field, numbers as numbers.
	a, b := strings.Split(r.pre, "."), strings.Split(o.pre, ".")
	for n := 0; n < len(a) && n < len(b); n++ {
		x, errX := strconv.Atoi(a[n])
		y, errY := strconv.Atoi(b[n])
		switch {
		case errX == nil && errY == nil && x != y:
			return sign(x - y)
		case errX == nil && errY != nil:
			return -1
		case errX != nil && errY == nil:
			return 1
		case errX != nil && a[n] != b[n]:
			return strings.Compare(a[n], b[n])
		}
	}
	return sign(len(a) - len(b))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// bound is one comparison a version must pass: ">=" 1.4.0.
type bound struct {
	op string
	to release
}

func (b bound) admits(v [3]int) bool {
	c := release{parts: v}.compare(b.to)
	switch b.op {
	case ">=":
		return c >= 0
	case ">":
		return c > 0
	case "<=":
		return c <= 0
	case "<":
		return c < 0
	case "!=":
		return c != 0
	}
	return c == 0
}

// rule is what a declaration admits: any one of its alternatives, each of
// whose bounds must all hold. An alternative with no bound admits anything.
type rule [][]bound

func (r rule) admits(v [3]int) bool {
	for _, alt := range r {
		ok := true
		for _, b := range alt {
			ok = ok && b.admits(v)
		}
		if ok {
			return true
		}
	}
	return false
}

func anyVersion() rule { return rule{{}} }

func exactly(v release) rule { return rule{{{"=", v}}} }

// partial reads a version that may stop early or end in a wildcard: "1",
// "1.7", "1.7.x", "1.*", or carry a prerelease. It returns the version and
// how many of its parts are given.
func partial(s string) (release, int, bool) {
	var r release
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+")
	s, r.pre, _ = strings.Cut(s, "-")
	fields := strings.Split(s, ".")
	if s == "" || len(fields) > 3 {
		return r, 0, false
	}
	n := 0
	for _, f := range fields {
		if f == "x" || f == "X" || f == "*" {
			break
		}
		value, err := strconv.Atoi(f)
		if err != nil || value < 0 {
			return r, 0, false
		}
		r.parts[n] = value
		n++
	}
	if n < 3 {
		r.pre = ""
	}
	return r, n, true
}

// next is the first version past every one that matches the first n parts
// of v: 2.0.0 for 1.x, 1.8.0 for 1.7.x.
func next(v release, n int) release {
	p := v.parts
	switch n {
	case 0:
		return release{parts: [3]int{1 << 30}}
	case 1:
		return release{parts: [3]int{p[0] + 1}}
	case 2:
		return release{parts: [3]int{p[0], p[1] + 1}}
	}
	return release{parts: [3]int{p[0], p[1], p[2] + 1}}
}

var spaceAfterOperator = regexp.MustCompile(`([<>=~^]+)\s+`)

// nodeRule reads an npm or pub version range: "^1.7.0", "~1.7.0",
// ">=1.4.0 <1.5.0", "1.7.x", "1.2.0 - 1.4.0", "^1.0.0 || ^2.0.0", "any".
func nodeRule(declared string) (rule, bool) {
	d := strings.TrimSpace(declared)
	switch d {
	case "", "*", "x", "X", "any", "latest":
		return anyVersion(), true
	}
	var r rule
	for _, alt := range strings.Split(d, "||") {
		alt = strings.TrimSpace(alt)
		if from, to, ok := strings.Cut(alt, " - "); ok {
			lo, _, okLo := partial(from)
			hi, n, okHi := partial(to)
			if !okLo || !okHi {
				return nil, false
			}
			bounds := []bound{{">=", lo}}
			if n == 3 {
				bounds = append(bounds, bound{"<=", hi})
			} else {
				bounds = append(bounds, bound{"<", next(hi, n)})
			}
			r = append(r, bounds)
			continue
		}
		var bounds []bound
		for _, token := range strings.Fields(spaceAfterOperator.ReplaceAllString(alt, "$1")) {
			b, ok := nodeBounds(token)
			if !ok {
				return nil, false
			}
			bounds = append(bounds, b...)
		}
		r = append(r, bounds)
	}
	return r, true
}

func nodeBounds(token string) ([]bound, bool) {
	op := strings.TrimRight(token[:len(token)-len(strings.TrimLeft(token, "<>=~^"))], " ")
	v, n, ok := partial(token[len(op):])
	if !ok {
		return nil, false
	}
	if n == 0 {
		return nil, op == "" || op == ">="
	}
	switch op {
	case "^":
		// The leftmost part that is not zero stays: ^1.7.0 is any 1.x from
		// 1.7.0, ^0.3.0 any 0.3.x.
		keep := 1
		if v.parts[0] == 0 && n > 1 {
			keep = 2
			if v.parts[1] == 0 && n > 2 {
				keep = 3
			}
		}
		return []bound{{">=", v}, {"<", next(v, keep)}}, true
	case "~":
		return []bound{{">=", v}, {"<", next(v, min(n, 2))}}, true
	case ">=":
		return []bound{{">=", v}}, true
	case "<":
		return []bound{{"<", v}}, true
	case ">":
		if n < 3 {
			return []bound{{">=", next(v, n)}}, true
		}
		return []bound{{">", v}}, true
	case "<=":
		if n < 3 {
			return []bound{{"<", next(v, n)}}, true
		}
		return []bound{{"<=", v}}, true
	case "", "=":
		if n < 3 {
			return []bound{{">=", v}, {"<", next(v, n)}}, true
		}
		return []bound{{"=", v}}, true
	}
	return nil, false
}

// podRule reads CocoaPods requirements, all of which must hold: "~> 1.19",
// ">= 1.12", "< 1.20", "1.19.1". None admits any version.
func podRule(requirements []string) (rule, bool) {
	bounds := []bound{}
	for _, req := range requirements {
		req = strings.TrimSpace(req)
		op := strings.TrimSpace(req[:len(req)-len(strings.TrimLeft(req, "~<>=!"))])
		v, n, ok := partial(strings.TrimSpace(req[len(op):]))
		if !ok || n == 0 {
			return nil, false
		}
		switch op {
		case "~>":
			// The last part given may grow; the one before it may not.
			keep := max(n-1, 1)
			bounds = append(bounds, bound{">=", v}, bound{"<", next(v, keep)})
		case ">=", ">", "<=", "<", "!=":
			bounds = append(bounds, bound{op, v})
		case "", "=":
			bounds = append(bounds, bound{"=", v})
		default:
			return nil, false
		}
	}
	return rule{bounds}, true
}
