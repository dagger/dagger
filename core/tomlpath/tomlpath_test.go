package tomlpath

import "testing"

func TestFormatSegment(t *testing.T) {
	cases := map[string]string{
		"name":       "name",       // bare
		"with_under": "with_under", // bare
		"with-dash":  "with-dash",  // bare
		"123":        "123",        // bare
		"":           `""`,         // empty is not bare
		"full name":  `"full name"`,
		"with.dot":   `"with.dot"`,
		`has"quote`:  `"has\"quote"`,
		`back\slash`: `"back\\slash"`,
		"tab\there":  `"tab\there"`,
		"a\bb":       `"a\bb"`, // backspace (previously unescaped by the old duplicate)
		"a\fb":       `"a\fb"`, // form feed (previously unescaped by the old duplicate)
	}
	for in, want := range cases {
		if got := FormatSegment(in); got != want {
			t.Errorf("FormatSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDotted(t *testing.T) {
	if got := Dotted("owner", "name"); got != "owner.name" {
		t.Errorf("Dotted = %q", got)
	}
	if got := Dotted("owner", "full name"); got != `owner."full name"` {
		t.Errorf("Dotted = %q", got)
	}
	if got := Dotted("with.dot"); got != `"with.dot"` {
		t.Errorf("Dotted = %q", got)
	}
}
