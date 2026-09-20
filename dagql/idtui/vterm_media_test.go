package idtui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
)

func mediaTestView(term *Vterm) string {
	term.SetHeight(term.UsedHeight())
	term.ScrollToTop()
	return ansi.Strip(term.View())
}

func TestVtermMediaOrdering(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	term.SetWidth(60)
	_, err := term.WriteMarkdown([]byte("**before**"))
	require.NoError(t, err)
	term.WriteMedia(dagui.MediaRecord{Kind: "image", MIMEType: "image/png", Data: "SECRET_IMAGE_BASE64"}, "[image: image/png]")
	_, err = term.WriteMarkdown([]byte("**after**"))
	require.NoError(t, err)
	_, err = term.Write([]byte("plain\n"))
	require.NoError(t, err)
	term.WriteMedia(dagui.MediaRecord{Kind: "audio", MIMEType: "audio/wav", Data: "SECRET_AUDIO_BASE64"}, "[audio: audio/wav]")
	_, err = term.WriteDiff([]byte("-old\n+new\n"))
	require.NoError(t, err)
	term.WriteMedia(dagui.MediaRecord{Kind: "document", MIMEType: "application/pdf", Data: "SECRET_PDF_BASE64"}, "[file: application/pdf]")
	_, err = term.WriteMarkdown([]byte("last"))
	require.NoError(t, err)

	view := mediaTestView(term)
	last := -1
	for _, text := range []string{"before", "[image: image/png]", "after", "plain", "[audio: audio/wav]", "-old", "+new", "[file: application/pdf]", "last"} {
		idx := strings.Index(view, text)
		require.Greater(t, idx, last, "%q out of order in %q", text, view)
		last = idx
	}
	require.Equal(t, term.UsedHeight(), strings.Count(view, "\n"))
	for _, print := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return term.Print(b) },
		func(b *bytes.Buffer) error { return term.PrintRaw(b) },
	} {
		var out bytes.Buffer
		require.NoError(t, print(&out))
		require.Contains(t, out.String(), "[image: image/png]")
		require.Contains(t, out.String(), "[audio: audio/wav]")
		require.Contains(t, out.String(), "[file: application/pdf]")
		require.NotContains(t, out.String(), "SECRET_")
		require.NotContains(t, out.String(), "\x1b")
	}
	require.NotContains(t, view, "SECRET_")
}

func TestVtermMediaPrefixAndReflow(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	term.SetWidth(30)
	term.SetPrefix("> ")
	_, _ = term.WriteMarkdown([]byte("first\nsecond"))
	term.WriteMedia(dagui.MediaRecord{Kind: "image"}, "[image]")
	_, _ = term.WriteMarkdown([]byte("third\nfourth"))
	_, _ = term.Write([]byte("abcdefghijklmnopqrstuvwx"))

	wide := mediaTestView(term)
	require.True(t, strings.HasPrefix(wide, "first"), wide)
	for _, line := range strings.Split(strings.TrimSuffix(wide, "\n"), "\n")[1:] {
		require.True(t, strings.HasPrefix(line, "> "), line)
	}
	wideHeight := term.UsedHeight()
	term.SetWidth(12)
	narrow := mediaTestView(term)
	require.Greater(t, term.UsedHeight(), wideHeight)
	for _, line := range strings.Split(narrow, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 12, line)
	}
	term.SetPrefix(">>>> ")
	prefixed := mediaTestView(term)
	for _, line := range strings.Split(prefixed, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 12, line)
	}
	term.SetWidth(30)
	term.SetPrefix("> ")
	require.Equal(t, wide, mediaTestView(term))
}

func TestVtermMediaScrollAndSearch(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	term.SetWidth(30)
	_, _ = term.WriteMarkdown([]byte("before"))
	term.WriteMedia(dagui.MediaRecord{Kind: "document"}, "[file]")
	_, _ = term.WriteMarkdown([]byte("after"))
	all := mediaTestView(term)
	rows := strings.Split(strings.TrimSuffix(all, "\n"), "\n")
	term.SetHeight(1)
	term.ScrollToTop()
	require.Equal(t, rows[0]+"\n", term.View())
	term.ScrollBy(1)
	require.Equal(t, rows[1]+"\n", term.View())
	term.ScrollToBottom()
	require.Equal(t, rows[len(rows)-1]+"\n", term.View())
	require.Equal(t, 1.0, term.ScrollPercent())
	count, row := term.Search("after", 0)
	require.Equal(t, 1, count)
	require.Equal(t, len(rows)-1, row)
	require.Equal(t, []int{row}, term.Term().SearchMatchRows())
	term.ScrollToRow(row)
	require.Contains(t, term.View(), "after")
	var clipped bytes.Buffer
	term.Render(&clipped, 1, 1)
	require.Equal(t, rows[1]+"\n", clipped.String())
}

func TestVtermMediaSafeFallback(t *testing.T) {
	term := NewVterm(termenv.ANSI)
	term.SetWidth(80)
	term.WriteMedia(dagui.MediaRecord{Kind: "image", MIMEType: "image/png", Data: "secret"}, "\x1b[31m[image]\x1b[0m\x1b]0;unsafe-title\a\r\n")
	require.Equal(t, "[image]\n", mediaTestView(term))
	var raw bytes.Buffer
	require.NoError(t, term.PrintRaw(&raw))
	require.Equal(t, "[image]\n", raw.String())
	term.WriteMedia(dagui.MediaRecord{Kind: "audio", Data: "secret"}, "")
	require.Contains(t, mediaTestView(term), "[media]")
}

func TestVtermMediaImageLayoutAndClipping(t *testing.T) {
	images, terminal, out := kittyTestManager(t)
	data := kittyTestPNG(t, 160, 64)
	term := NewVterm(termenv.ANSI)
	term.images = images
	term.SetWidth(22)
	term.SetPrefix("> ")
	_, _ = term.WriteMarkdown([]byte("before"))
	term.WriteMedia(dagui.MediaRecord{Kind: "image", MIMEType: "image/png", Data: data}, "[image]")
	_, _ = term.WriteMarkdown([]byte("after"))
	require.Equal(t, 7, term.UsedHeight()) // text, caption, four image rows, text
	require.Empty(t, out.Output(), "layout must not upload images")
	term.SetHeight(term.UsedHeight())
	term.ScrollToTop()
	view := term.View()
	require.Equal(t, 7, strings.Count(view, "\n"))
	require.NotContains(t, view, data)
	require.Empty(t, out.Output(), "View only prepares placement references")

	// Skip both the caption and the first image row. The surviving row must
	// still upload and place the image without relying on offscreen controls.
	var clipped bytes.Buffer
	term.Render(&clipped, 3, 1)
	require.Equal(t, 1, strings.Count(clipped.String(), "\n"))
	require.Contains(t, clipped.String(), kittyMarkerPrefix)
	require.True(t, strings.HasPrefix(clipped.String(), "> "))
	require.Equal(t, 22, ansi.StringWidth(strings.TrimSuffix(clipped.String(), "\n")))
	terminal.Write(clipped.Bytes())
	require.Contains(t, out.Output(), "\x1b_G")
	require.NotContains(t, out.Output(), kittyMarkerPrefix)
	for _, print := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return term.Print(b) },
		func(b *bytes.Buffer) error { return term.PrintRaw(b) },
	} {
		var safe bytes.Buffer
		require.NoError(t, print(&safe))
		require.Equal(t, "before\n[image]\nafter", safe.String())
	}
	count, row := term.Search("after", 0)
	require.Equal(t, 1, count)
	require.Equal(t, 6, row)
	require.NotContains(t, term.LastLine(), kittyMarkerPrefix)

	term.SetWidth(12)
	require.Equal(t, 5, term.UsedHeight()) // image shrinks to ten columns/two rows
	term.SetHeight(1)
	term.ScrollToBottom()
	term.images = nil // final reports disable images even before Stop
	require.Equal(t, 3, term.UsedHeight())
	require.Equal(t, 2, term.Offset)
	require.Contains(t, ansi.Strip(term.View()), "after")
	term.SetHeight(term.UsedHeight())
	term.ScrollToTop()
	require.NotContains(t, term.View(), kittyMarkerPrefix)
	require.NotContains(t, term.View(), string(kittyPlaceholder))
}

func TestVtermMediaUnsupportedAndInactiveImages(t *testing.T) {
	images, terminal, _ := kittyTestManager(t)
	for _, record := range []dagui.MediaRecord{
		{Kind: "image", MIMEType: "image/png", Data: "invalid"},
		{Kind: "image", MIMEType: "image/svg+xml", Data: "PHN2Zz4="},
		{Kind: "audio", MIMEType: "audio/wav", Data: "UklGRg=="},
		{Kind: "document", MIMEType: "application/pdf", Data: "JVBERg=="},
	} {
		term := NewVterm(termenv.ANSI)
		term.images = images
		term.SetWidth(40)
		term.WriteMedia(record, "[attachment]")
		require.Equal(t, "[attachment]\n", mediaTestView(term))
	}
	term := NewVterm(termenv.ANSI)
	term.images = images
	term.SetWidth(40)
	term.WriteMedia(dagui.MediaRecord{Kind: "image", MIMEType: "image/png", Data: kittyTestPNG(t, 80, 32)}, "[image]")
	require.Equal(t, 3, term.UsedHeight())
	terminal.Stop()
	require.Equal(t, "[image]\n", mediaTestView(term))
}

func TestVtermMediaStreamingLayoutIsLazy(t *testing.T) {
	images, _, _ := kittyTestManager(t)
	term := NewVterm(termenv.ANSI)
	term.images = images
	term.SetWidth(40)
	term.SetHeight(2)
	term.WriteMedia(dagui.MediaRecord{Kind: "image", MIMEType: "image/png", Data: kittyTestPNG(t, 80, 32)}, "[image]")
	require.Nil(t, term.mediaRows)
	require.Empty(t, images.cache, "ingestion must not decode images")
	for range 10 {
		_, err := term.WriteMarkdown([]byte("another line\n"))
		require.NoError(t, err)
		require.Nil(t, term.mediaRows, "streamed chunks must not rebuild layout")
	}
	var raw bytes.Buffer
	require.NoError(t, term.PrintRaw(&raw))
	require.Nil(t, term.mediaRows, "raw reports must not render layout")
	height := term.UsedHeight()
	require.Greater(t, height, term.Height)
	require.Equal(t, height-term.Height, term.Offset, "deferred layout follows the tail")
	require.NotNil(t, term.mediaRows)

	_, _ = term.WriteMarkdown([]byte("last line\n"))
	require.Nil(t, term.mediaRows)
	term.ScrollBy(-1)
	require.Equal(t, term.UsedHeight()-term.Height-1, term.Offset)
	before := term.Offset
	_, _ = term.WriteMarkdown([]byte("more lines\nmore lines\n"))
	require.Nil(t, term.mediaRows)
	_ = term.View()
	require.Equal(t, before, term.Offset, "streaming must preserve a scrolled viewport")

	term.ScrollToBottom()
	_, _ = term.WriteMarkdown([]byte("tail\n"))
	term.ScrollToTop()
	_ = term.View()
	require.Zero(t, term.Offset, "explicit top cancels deferred tail following")
}

func TestVtermTextBehaviorUnchangedUntilMedia(t *testing.T) {
	term := NewVterm(termenv.Ascii)
	term.SetWidth(40)
	term.SetPrefix("> ")
	_, _ = term.WriteMarkdown([]byte("before"))
	_, _ = term.Write([]byte("plain\n"))
	term.SetHeight(term.UsedHeight())
	before := term.View()
	require.Nil(t, term.segments)
	term.WriteMedia(dagui.MediaRecord{Kind: "audio"}, "[audio]")
	after := mediaTestView(term)
	require.Less(t, strings.Index(after, "before"), strings.Index(after, "plain"))
	require.True(t, strings.HasPrefix(before, "before"))
	require.True(t, strings.HasPrefix(after, "before"))
	require.Contains(t, after, "> plain")
}
