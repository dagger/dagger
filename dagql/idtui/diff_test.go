package idtui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestHighlightDiffPreservesPatchExactly(t *testing.T) {
	patches := map[string]string{
		"multi-file with trailing newline": `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func old() string { return "old" }
+func next() string { return "new" }
diff --git a/main.py b/main.py
index 3333333..4444444 100644
--- a/main.py
+++ b/main.py
@@ -1,2 +1,2 @@
-def old():
+def next():
     return "value"
`,
		"no trailing newline": `diff --git a/app.js b/app.js
--- a/app.js
+++ b/app.js
@@ -1 +1 @@
-const old = "before";
+const next = "after";`,
		"crlf": "diff --git a/a.go b/a.go\r\n--- a/a.go\r\n+++ b/a.go\r\n@@ -1 +1 @@\r\n-var old = 1\r\n+var next = 2\r\n",
	}

	for name, patch := range patches {
		t.Run(name, func(t *testing.T) {
			got := highlightDiff(termenv.ANSI, patch)
			if stripped := ansi.Strip(got); stripped != patch {
				t.Fatalf("ANSI-stripped output changed patch\nwant: %q\n got: %q", patch, stripped)
			}
		})
	}
}

func TestHighlightDiffMultipleLanguagesAndExactNames(t *testing.T) {
	patch := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1 +1 @@
-func old() string { return "old" }
+func greet() string { return "hello" }
diff --git a/tool.py b/tool.py
--- a/tool.py
+++ b/tool.py
@@ -1 +1 @@
-def old(): return "old"
+def greet(): return "hello"
diff --git a/Dockerfile b/Dockerfile
--- a/Dockerfile
+++ b/Dockerfile
@@ -1 +1 @@
-FROM busybox
+FROM alpine
`
	got := highlightDiff(termenv.ANSI, patch)

	for _, line := range []string{
		`+func greet() string { return "hello" }`,
		`+def greet(): return "hello"`,
		`+FROM alpine`,
	} {
		source := highlightedSourceLine(t, got, line)
		if !strings.Contains(source, "\x1b[") {
			t.Errorf("source for %q was not syntax highlighted: %q", line, source)
		}
	}

	// The green addition marker is reset before Chroma's source styling. In
	// particular, Go's keyword must not inherit the marker's green colour.
	goLine := highlightedLine(t, got, `+func greet() string { return "hello" }`)
	if !strings.Contains(goLine, "\x1b[32m+\x1b[0m") {
		t.Fatalf("addition marker is not independently styled: %q", goLine)
	}
	if strings.Contains(goLine, "\x1b[32m+func") {
		t.Fatalf("addition colour leaked into source: %q", goLine)
	}
}

func TestHighlightDiffAddedAndDeletedFiles(t *testing.T) {
	patch := `diff --git a/new.py b/new.py
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/new.py
@@ -0,0 +1,2 @@
+def hello():
+    return "hi"
diff --git a/old.go b/old.go
deleted file mode 100644
index 2222222..0000000
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-package old
-func goodbye() {}
`
	got := highlightDiff(termenv.ANSI, patch)
	if stripped := ansi.Strip(got); stripped != patch {
		t.Fatalf("ANSI-stripped output changed patch\nwant: %q\n got: %q", patch, stripped)
	}

	for _, line := range []string{`+def hello():`, `-func goodbye() {}`} {
		if source := highlightedSourceLine(t, got, line); !strings.Contains(source, "\x1b[") {
			t.Errorf("source for %q was not highlighted: %q", line, source)
		}
	}
}

func TestHighlightDiffUsesSeparateOldAndNewSourceStreams(t *testing.T) {
	patch := `diff --git a/comment.go b/comment.go
--- a/comment.go
+++ b/comment.go
@@ -1,2 +1,2 @@
-/* deleted comment
-still deleted */
+/* added comment
+still added */
`
	got := highlightDiff(termenv.ANSI, patch)

	// These continuation lines only lex as comments when each side of the hunk
	// is presented to Chroma as a source stream rather than one line at a time.
	for _, line := range []string{`-still deleted */`, `+still added */`} {
		if source := highlightedSourceLine(t, got, line); !strings.Contains(source, "\x1b[3m\x1b[95m") {
			t.Errorf("multiline comment %q did not retain comment syntax: %q", line, source)
		}
	}
}

func TestHighlightDiffUnknownExtension(t *testing.T) {
	patch := `diff --git a/data.unknown-extension b/data.unknown-extension
--- a/data.unknown-extension
+++ b/data.unknown-extension
@@ -1 +1 @@
-before value
+after value
`
	got := highlightDiff(termenv.ANSI, patch)
	if stripped := ansi.Strip(got); stripped != patch {
		t.Fatalf("ANSI-stripped output changed patch\nwant: %q\n got: %q", patch, stripped)
	}

	for _, line := range []string{`-before value`, `+after value`} {
		if source := highlightedSourceLine(t, got, line); strings.Contains(source, "\x1b[") {
			t.Errorf("unknown source for %q unexpectedly received syntax colours: %q", line, source)
		}
	}
}

func TestHighlightDiffMetadataAndNoNewlineMarker(t *testing.T) {
	patch := `diff --git a/name.txt b/renamed.txt
similarity index 88%
rename from name.txt
rename to renamed.txt
index 1111111..2222222 100644
--- a/name.txt
+++ b/renamed.txt
@@ -1 +1 @@
-old value
\ No newline at end of file
+new value
\ No newline at end of file`
	got := highlightDiff(termenv.ANSI, patch)
	if stripped := ansi.Strip(got); stripped != patch {
		t.Fatalf("ANSI-stripped output changed patch\nwant: %q\n got: %q", patch, stripped)
	}

	for _, line := range []string{
		"diff --git a/name.txt b/renamed.txt",
		"similarity index 88%",
		"rename from name.txt",
		"@@ -1 +1 @@",
		`\ No newline at end of file`,
	} {
		if rendered := highlightedLine(t, got, line); !strings.Contains(rendered, "\x1b[") {
			t.Errorf("metadata %q was not styled: %q", line, rendered)
		}
	}
}

func TestHighlightDiffAsciiReturnsInputUnchanged(t *testing.T) {
	patch := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-\x1b[31mold\x1b[0m\n+new\n"
	if got := highlightDiff(termenv.Ascii, patch); got != patch {
		t.Fatalf("ASCII profile changed patch\nwant: %q\n got: %q", patch, got)
	}
}

func highlightedSourceLine(t *testing.T, output, plain string) string {
	t.Helper()
	line := highlightedLine(t, output, plain)
	reset := strings.Index(line, "\x1b[0m")
	if reset < 0 {
		t.Fatalf("diff marker for %q was not styled: %q", plain, line)
	}
	return line[reset+len("\x1b[0m"):]
}

func highlightedLine(t *testing.T, output, plain string) string {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if ansi.Strip(line) == plain {
			return line
		}
	}
	t.Fatalf("line %q not found in output", plain)
	return ""
}
