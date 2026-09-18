package dagui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// GrepCalls is the discovery half of the digest→ID path: it must surface the
// digest a line names, keep ordinary literals untruncated so a value baked
// deep into an argument is findable, and print receiver/ID-argument digests
// verbatim so a chain can be walked by grepping for them.
func TestGrepCalls(t *testing.T) {
	root, unspanned := callPayloadTestChain()
	withSkills, dir := unspanned[0], unspanned[1]

	db := NewDB()
	exportCallPayloads(t, db, spanID(1), root, withSkills, dir)

	t.Run("matches by argument value and names the digest", func(t *testing.T) {
		lines := db.GrepCalls(regexp.MustCompile(`/skills`), 0)
		if len(lines) != 1 {
			t.Fatalf("want 1 line, got %d: %q", len(lines), lines)
		}
		if !strings.HasPrefix(lines[0], dir.Digest+"  directory(path: \"/skills\") -> Directory") {
			t.Fatalf("unexpected line: %q", lines[0])
		}
	})

	t.Run("prints receiver and ID-argument digests verbatim", func(t *testing.T) {
		lines := db.GrepCalls(regexp.MustCompile(regexp.QuoteMeta(dir.Digest)), 0)
		// The directory frame itself, plus the withSkills frame that
		// references it as an ID literal.
		if len(lines) != 2 {
			t.Fatalf("want 2 lines, got %d: %q", len(lines), lines)
		}
		lines = db.GrepCalls(regexp.MustCompile("recv="+regexp.QuoteMeta(withSkills.Digest)), 0)
		if len(lines) != 1 || !strings.HasPrefix(lines[0], root.Digest+"  agent()") {
			t.Fatalf("want the agent frame by receiver, got %q", lines)
		}
	})

	t.Run("does not truncate long literals", func(t *testing.T) {
		long := strings.Repeat("x", 500) + "/needle"
		frame := &callpbv1.Call{
			Field: "file",
			Type:  &callpbv1.Type{NamedType: "File"},
			Args: []*callpbv1.Argument{{
				Name:  "path",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: long}},
			}},
		}
		setCallDigest(frame)
		exportCallPayloads(t, db, spanID(1), frame)
		lines := db.GrepCalls(regexp.MustCompile(`/needle`), 0)
		if len(lines) != 1 || !strings.Contains(lines[0], long) {
			t.Fatalf("long literal must be greppable in full, got %q", lines)
		}
	})

	t.Run("caps and reports the remainder", func(t *testing.T) {
		lines := db.GrepCalls(regexp.MustCompile(`.`), 2)
		if len(lines) != 3 {
			t.Fatalf("want 2 lines + remainder, got %d: %q", len(lines), lines)
		}
		if !strings.HasPrefix(lines[2], "… 2 more") {
			t.Fatalf("unexpected remainder line: %q", lines[2])
		}
	})

	t.Run("no match", func(t *testing.T) {
		if lines := db.GrepCalls(regexp.MustCompile(`nope`), 0); len(lines) != 0 {
			t.Fatalf("want no lines, got %q", lines)
		}
	})
}
