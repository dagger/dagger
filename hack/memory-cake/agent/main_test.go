package main

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestEditsMakeValidGo applies the turns' edits as the agent does and checks
// that the package still parses, and that each turn's edit lands once.
func TestEditsMakeValidGo(t *testing.T) {
	src, test, probe := goFile(0), testFile(0), probeFile(0)
	for turn := 1; turn <= 3; turn++ {
		for _, s := range []string{src, test} {
			if strings.Count(s, marker) != 1 {
				t.Fatalf("turn %d: marker appears %d times", turn, strings.Count(s, marker))
			}
		}
		src = strings.Replace(src, marker, turnFunc(turn)+marker, 1)
		test = strings.Replace(test, marker, turnTest(turn)+marker, 1)
		probe = strings.Replace(probe, probeMarker, probeTurn(turn)+probeMarker, 1)
	}
	if src != goFile(3) || test != testFile(3) || probe != probeFile(3) {
		t.Fatal("edits do not add up to the files of turn 3")
	}
	fset := token.NewFileSet()
	for name, s := range map[string]string{"turns.go": src, "turns_test.go": test} {
		if _, err := parser.ParseFile(fset, name, s, 0); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, s)
		}
	}
	for _, s := range []string{src, test, probe, noteText(3), blockText(3)} {
		dangString(s) // panics on $
	}
}
