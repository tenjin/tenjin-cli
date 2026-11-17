package render

import (
	"reflect"
	"strings"
)

// Clean removes the control characters from text that came from outside
// the CLI, such as a project's files or a coding agent: escape sequences in
// it could move the cursor, rewrite lines already printed, or set the
// clipboard. Tabs and line breaks stay; bytes that are not UTF-8 go.
func Clean(s string) string {
	// A byte that is not UTF-8, such as 0x9b, some terminals read as a
	// control sequence too.
	s = strings.ToValidUTF8(s, "")
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

// CleanCell is Clean for text that must stay on one line, such as a table's
// cell: tabs and line breaks become spaces, so that a value cannot make
// columns or rows of its own.
func CleanCell(s string) string {
	return flatten(Clean(s))
}

var oneLine = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")

func flatten(s string) string {
	return oneLine.Replace(s)
}

func isControl(r rune) bool {
	return (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// CleanAll applies Clean to every string reachable from v, which must be a
// pointer. It is for the copy of a result that is written out for people.
func CleanAll(v any) {
	clean(reflect.ValueOf(v))
}

func clean(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			clean(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() || v.Field(i).Kind() == reflect.Pointer || v.Field(i).Kind() == reflect.Slice {
				clean(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			clean(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			if e := v.MapIndex(k); e.Kind() == reflect.String {
				v.SetMapIndex(k, reflect.ValueOf(Clean(e.String())).Convert(e.Type()))
			}
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(Clean(v.String()))
		}
	}
}
