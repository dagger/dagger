package dagui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// GrepCalls is the discovery half of the digest→ID path: it must surface the
// digest a line names, search full literals even when their display is bounded,
// and print receiver/ID-argument digests verbatim for chain navigation.
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

	t.Run("searches long literals but shows an excerpt", func(t *testing.T) {
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
		require.Len(t, lines, 1)
		require.Contains(t, lines[0], "/needle")
		require.Contains(t, lines[0], "307 chars omitted")
		require.NotContains(t, lines[0], long)
		// Anchors and patterns crossing the literal boundary still match
		// the original full line, not a truncated or isolated argument.
		lines = db.GrepCalls(regexp.MustCompile(`file\(path: "x{500}/needle"\) -> File$`), 0)
		require.Len(t, lines, 1)
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

func TestGrepCallsPreview(t *testing.T) {
	str := func(s string) *callpbv1.Literal {
		return &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: s}}
	}
	t.Run("nested strings and unicode", func(t *testing.T) {
		frame := &callpbv1.Call{
			Digest: "xxh3:1111111111111111", Field: "nested", ReceiverDigest: "xxh3:2222222222222222",
			Type: &callpbv1.Type{NamedType: "Thing"},
			Args: []*callpbv1.Argument{{Name: "values", Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{
				Values: []*callpbv1.Literal{
					str(strings.Repeat("界", 500)),
					{Value: &callpbv1.Literal_Object{Object: &callpbv1.Object{Values: []*callpbv1.Argument{
						{Name: "text", Value: str(strings.Repeat("é", 500) + "needle" + strings.Repeat("界", 500))},
					}}}},
					{Value: &callpbv1.Literal_CallDigest{CallDigest: "xxh3:3333333333333333"}},
				},
			}}}}},
		}
		db := NewDB()
		db.Calls[frame.Digest] = frame
		lines := db.GrepCalls(regexp.MustCompile("needle"), 0)
		require.Len(t, lines, 1)
		require.True(t, utf8.ValidString(lines[0]))
		require.Contains(t, lines[0], "needle")
		require.Contains(t, lines[0], "300 chars omitted")
		require.Contains(t, lines[0], "450 chars omitted")
		require.Contains(t, lines[0], "356 chars omitted")
		require.Contains(t, lines[0], "xxh3:3333333333333333")
		require.True(t, strings.HasPrefix(lines[0], frame.Digest+"  nested("))
		require.True(t, strings.HasSuffix(lines[0], ") -> Thing  recv="+frame.ReceiverDigest))
	})

	t.Run("large argument collection keeps deep match and metadata", func(t *testing.T) {
		frame := &callpbv1.Call{
			Digest: "xxh3:1111111111111111", Field: "wide", ReceiverDigest: "xxh3:2222222222222222",
			Type: &callpbv1.Type{NamedType: "Thing"},
		}
		for i := range 1000 {
			frame.Args = append(frame.Args, &callpbv1.Argument{Name: fmt.Sprintf("arg%d", i), Value: str(strings.Repeat("x", 100))})
		}
		frame.Args = append(frame.Args, &callpbv1.Argument{Name: "last", Value: str("deep needle")})
		db := NewDB()
		db.Calls[frame.Digest] = frame
		lines := db.GrepCalls(regexp.MustCompile("needle"), 0)
		require.Len(t, lines, 1)
		require.LessOrEqual(t, len(lines[0]), grepCallLineBytes)
		require.Contains(t, lines[0], "deep needle")
		require.Contains(t, lines[0], "chars omitted")
		require.True(t, strings.HasPrefix(lines[0], frame.Digest+"  wide("))
		require.True(t, strings.HasSuffix(lines[0], ") -> Thing  recv="+frame.ReceiverDigest))
	})

	t.Run("response budget and stable ordering", func(t *testing.T) {
		db := NewDB()
		for i := 999; i >= 0; i-- {
			frame := &callpbv1.Call{Digest: fmt.Sprintf("xxh3:%016x", i), Field: "file", Args: []*callpbv1.Argument{
				{Name: "path", Value: str(strings.Repeat("x", 1000) + "needle")},
			}}
			db.Calls[frame.Digest] = frame
		}
		lines := db.GrepCalls(regexp.MustCompile("needle"), 0)
		require.Greater(t, len(lines), 1)
		require.LessOrEqual(t, len(strings.Join(lines, "\n")+"\n"), grepCallsBytes)
		require.Equal(t, fmt.Sprintf("… %d more (response size limit; tighten the pattern)", 1000-(len(lines)-1)), lines[len(lines)-1])
		for i, line := range lines[:len(lines)-1] {
			require.True(t, strings.HasPrefix(line, fmt.Sprintf("xxh3:%016x  file(", i)))
			require.Contains(t, line, "needle")
		}
		require.Equal(t, lines, db.GrepCalls(regexp.MustCompile("needle"), 0))
	})

	t.Run("opaque digested strings", func(t *testing.T) {
		frame := &callpbv1.Call{Digest: "xxh3:1111111111111111", Field: "secret", Args: []*callpbv1.Argument{
			{Name: "value", Value: &callpbv1.Literal{Value: &callpbv1.Literal_DigestedString{DigestedString: &callpbv1.DigestedString{
				Value: "private-needle", Digest: "xxh3:2222222222222222",
			}}}},
		}}
		db := NewDB()
		db.Calls[frame.Digest] = frame
		require.Empty(t, db.GrepCalls(regexp.MustCompile("private-needle"), 0))
	})
}

func TestCallTextExcerpt(t *testing.T) {
	for _, tt := range []struct {
		name  string
		text  string
		query string
		want  string
	}{
		{"short", "hello", "hello", "hello"},
		{"boundary", strings.Repeat("x", 200), "x", strings.Repeat("x", 200)},
		{"prefix", strings.Repeat("x", 201), "x", strings.Repeat("x", 200) + "…(1 chars omitted)"},
		{"escaped", strings.Repeat("a", 300) + "\\nneedle\\t", `\\nneedle`, "needle"},
		{"oversized match", strings.Repeat("x", 1000), "x+", "…(800 chars omitted)"},
		{"zero width", strings.Repeat("x", 1000), "^", "…(800 chars omitted)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			match := regexp.MustCompile(tt.query).FindStringIndex(tt.text)
			require.NotNil(t, match)
			got := callTextExcerpt(tt.text, match[0], match[1], grepCallStringLimit)
			require.Contains(t, got, tt.want)
			require.True(t, utf8.ValidString(got))
		})
	}
}
