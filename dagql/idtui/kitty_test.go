package idtui

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

func kittyTestManager(t *testing.T) (*kittyImages, *kittyTerminal, *tuist.HeadlessTerminal) {
	t.Helper()
	k := newKittyImages()
	require.NotNil(t, k)
	out := tuist.NewHeadlessTerminal(80, 24)
	term := k.wrapTerminal(out).(*kittyTerminal)
	require.NoError(t, term.Start(nil, nil))
	t.Cleanup(term.Stop)
	return k, term, out
}

func kittyTestPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: uint8(x * y), A: 255})
		}
	}
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestKittyCapability(t *testing.T) {
	for _, tc := range []struct {
		name, mode, term, tmux, sty string
		want                        bool
	}{
		{name: "kitty", term: "xterm-kitty", want: true},
		{name: "unsupported", term: "xterm-256color"},
		{name: "empty"},
		{name: "disabled", mode: "off", term: "xterm-kitty"},
		{name: "invalid", mode: "yes", term: "xterm-kitty"},
		{name: "tmux", term: "xterm-kitty", tmux: "session"},
		{name: "screen", term: "xterm-kitty", sty: "session"},
		{name: "override", mode: "kitty", term: "xterm-256color", tmux: "session", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"DAGGER_TUI_IMAGES": tc.mode, "TERM": tc.term, "TMUX": tc.tmux, "STY": tc.sty}
			require.Equal(t, tc.want, kittyImagesEnabled(func(key string) string { return env[key] }))
		})
	}
}

func TestKittyRowsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		w, h, width, cols, rows int
	}{
		{"native", 160, 64, 80, 20, 4},
		{"width", 160, 64, 10, 10, 2},
		{"height", 64, 768, 80, 4, 24},
		{"cell padding", 3, 5, 80, 1, 1},
		{"wide", 1600, 32, 1000, 80, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _, out := kittyTestManager(t)
			lines, ok := k.render(kittyTestPNG(t, tc.w, tc.h), "image/png", tc.width)
			require.True(t, ok)
			require.Empty(t, out.Output(), "render must never upload")
			require.Len(t, lines, tc.rows)
			for row, line := range lines {
				require.True(t, isKittyImageLine(line))
				require.Equal(t, tc.cols, ansi.StringWidth(line))
				runes := []rune(ansi.Strip(line))
				require.Len(t, runes, tc.cols*3)
				for col := range tc.cols {
					require.Equal(t, []rune{kittyPlaceholder, kittyDiacritics[row], kittyDiacritics[col]}, runes[col*3:col*3+3])
				}
			}
			for id, entry := range k.byID {
				require.NotZero(t, id)
				require.Less(t, id, uint32(1<<24))
				require.Equal(t, tc.cols, entry.columns)
				require.Equal(t, tc.rows, entry.rows)
				require.Contains(t, lines[0], fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16, (id>>8)&255, id&255))
				decoded, err := base64.StdEncoding.DecodeString(entry.payload)
				require.NoError(t, err)
				img, err := png.Decode(bytes.NewReader(decoded))
				require.NoError(t, err)
				require.Equal(t, image.Rect(0, 0, tc.cols*8, tc.rows*16), img.Bounds())
				if tc.name == "cell padding" {
					_, _, _, alpha := img.At(3, 4).RGBA()
					require.Zero(t, alpha, "small images are padded, not upscaled")
					_, _, _, alpha = img.At(2, 4).RGBA()
					require.Equal(t, uint32(65535), alpha)
				}
			}
		})
	}
}

func TestKittyFormats(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 16))
	for _, tc := range []struct {
		mime   string
		encode func(*bytes.Buffer) error
	}{
		{"image/png", func(b *bytes.Buffer) error { return png.Encode(b, img) }},
		{"image/jpeg", func(b *bytes.Buffer) error { return jpeg.Encode(b, img, nil) }},
		{"image/gif", func(b *bytes.Buffer) error { return gif.Encode(b, img, nil) }},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			k, _, _ := kittyTestManager(t)
			var b bytes.Buffer
			require.NoError(t, tc.encode(&b))
			_, ok := k.render(base64.StdEncoding.EncodeToString(b.Bytes()), tc.mime, 80)
			require.True(t, ok)
		})
	}
	t.Run("image/webp", func(t *testing.T) {
		k, _, _ := kittyTestManager(t)
		// A 1x1 VP8 image; the renderer converts it to PNG like other formats.
		_, ok := k.render("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA", "image/webp", 80)
		require.True(t, ok)
	})
}

func TestKittyRejectsInvalidImages(t *testing.T) {
	valid := kittyTestPNG(t, 8, 16)
	oversized, err := base64.StdEncoding.DecodeString(valid)
	require.NoError(t, err)
	// Valid IHDR config, deliberately no IDAT: rejection must happen before
	// decoding pixels, whose claimed allocation would exceed the pixel budget.
	binary.BigEndian.PutUint32(oversized[16:20], 4096)
	binary.BigEndian.PutUint32(oversized[20:24], 4096)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	oversized = oversized[:33]
	cfg, _, err := image.DecodeConfig(bytes.NewReader(oversized))
	require.NoError(t, err)
	require.Equal(t, 4096, cfg.Width)
	for _, tc := range []struct {
		name, data, mime string
		width            int
	}{
		{"invalid base64", "not-base64", "image/png", 80},
		{"empty", "", "image/png", 80},
		{"invalid image", "aGVsbG8=", "image/png", 80},
		{"mime mismatch", valid, "image/jpeg", 80},
		{"SVG", valid, "image/svg+xml", 80},
		{"too many pixels", base64.StdEncoding.EncodeToString(oversized), "image/png", 80},
		{"too many bytes", strings.Repeat("A", base64.StdEncoding.EncodedLen(kittyMaxInput)+1), "image/png", 80},
		{"zero width", valid, "image/png", 0},
		{"negative width", valid, "image/png", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _, out := kittyTestManager(t)
			lines, ok := k.render(tc.data, tc.mime, tc.width)
			require.False(t, ok)
			require.Nil(t, lines)
			require.Empty(t, k.byID)
			require.Empty(t, out.Output())
		})
	}
}

func TestKittyUploadClippingDedupAndCleanup(t *testing.T) {
	k, term, out := kittyTestManager(t)
	data := kittyTestPNG(t, 640, 384)
	lines, ok := k.render(data, "image/png", 80)
	require.True(t, ok)
	again, ok := k.render(data, "image/png", 80)
	require.True(t, ok)
	require.Equal(t, lines, again)
	require.Len(t, k.byID, 1)
	// A caller cannot corrupt cached rows through the returned slice.
	again[0] = "corrupt"
	again, _ = k.render(data, "image/png", 80)
	require.Equal(t, lines, again)
	var entry *kittyImage
	for _, entry = range k.byID {
	}
	require.Greater(t, len(entry.payload), kittyChunkSize)
	// Simulate clipping: only a later row reaches the terminal. It still owns
	// a marker and supplies its actual source row, not the screen row index.
	term.Write([]byte(lines[10]))
	stream := out.Output()
	require.NotContains(t, stream, kittyMarkerPrefix)
	require.Contains(t, stream, fmt.Sprintf("\x1b_Ga=p,U=1,i=%d,c=80,r=24,q=2\x1b\\", entry.id))
	require.Equal(t, 1, strings.Count(stream, "a=t,"))
	var payload strings.Builder
	for _, part := range strings.Split(stream, "\x1b_G")[1:] {
		command, _, found := strings.Cut(part, "\x1b\\")
		require.True(t, found, "all commands have an ST terminator")
		header, chunk, hasPayload := strings.Cut(command, ";")
		if !hasPayload {
			continue
		}
		require.LessOrEqual(t, len(chunk), kittyChunkSize)
		require.Zero(t, len(chunk)%4)
		if strings.Contains(header, "m=1") {
			require.Len(t, chunk, kittyChunkSize)
		}
		payload.WriteString(chunk)
	}
	require.Equal(t, entry.payload, payload.String())
	require.Contains(t, stream, "m=0;")
	out.Reset()
	term.WriteString(lines[11])
	require.NotContains(t, out.Output(), "\x1b_G", "unchanged image data must not upload on repaint")
	require.Contains(t, out.Output(), string(kittyPlaceholder))
	// Rendering another image without writing its row must not upload/free it.
	_, ok = k.render(kittyTestPNG(t, 8, 16), "image/png", 80)
	require.True(t, ok)
	out.Reset()
	term.Stop()
	require.Equal(t, fmt.Sprintf("\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", entry.id), out.Output())
	require.False(t, k.isActive())
	_, ok = k.render(data, "image/png", 80)
	require.False(t, ok, "final reports fall back after Stop")
	out.Reset()
	require.NoError(t, term.Start(nil, nil))
	term.WriteString(lines[15])
	require.Equal(t, 1, strings.Count(out.Output(), "a=t,"), "resume reuploads surviving cached rows")
}

type kittyRowsComponent struct {
	tuist.Compo
	lines []string
}

func (c *kittyRowsComponent) Render(ctx tuist.Context) { ctx.Lines(c.lines...) }

func TestKittyTuistClippingAndRowDiff(t *testing.T) {
	k, term, out := kittyTestManager(t)
	out.Resize(80, 3)
	first, ok := k.render(kittyTestPNG(t, 16, 160), "image/png", 80)
	require.True(t, ok)
	second, ok := k.render(kittyTestPNG(t, 80, 160), "image/png", 80)
	require.True(t, ok)
	component := &kittyRowsComponent{lines: append(first, second...)}
	tui := tuist.New(term)
	tui.AddChild(component)
	tui.RenderOnce()
	// The first image and the second image's first seven rows are clipped by
	// tuist itself. Only the three surviving rows cause a terminal upload.
	require.Equal(t, 1, strings.Count(out.Output(), "a=t,"))
	require.Equal(t, 30, strings.Count(out.Output(), string(kittyPlaceholder)))
	require.NotContains(t, out.Output(), kittyMarkerPrefix)
	for _, entry := range k.byID {
		require.Equal(t, entry.columns == 10, entry.uploaded)
	}
	require.Contains(t, out.Output(), string([]rune{kittyPlaceholder, kittyDiacritics[7], kittyDiacritics[0]}))
	out.Reset()
	tui.RenderOnce()
	require.Empty(t, out.Output(), "identical rows produce no terminal writes")
	// Replacing a placeholder row must issue a normal line erase, not create a
	// persistent graphics placement. Kitty ties visibility to those text cells.
	component.lines[len(component.lines)-1] = "replacement"
	component.Update()
	tui.RenderOnce()
	require.Contains(t, out.Output(), "\x1b[2K")
	require.Contains(t, out.Output(), "replacement")
	require.NotContains(t, out.Output(), "\x1b_G")
	out.Reset()
	component.lines = append(component.lines, "scroll")
	component.Update()
	tui.RenderOnce()
	require.NotContains(t, out.Output(), "\x1b_G")
	require.Contains(t, out.Output(), string([]rune{kittyPlaceholder, kittyDiacritics[8], kittyDiacritics[0]}))
}

func TestKittyHorizontalClipping(t *testing.T) {
	k, term, out := kittyTestManager(t)
	lines, ok := k.render(kittyTestPNG(t, 80, 32), "image/png", 80)
	require.True(t, ok)
	clipped := ansi.Cut(lines[1], 3, 6)
	require.Equal(t, 3, ansi.StringWidth(clipped))
	require.Contains(t, clipped, string([]rune{kittyPlaceholder, kittyDiacritics[1], kittyDiacritics[3]}))
	term.WriteString(clipped)
	require.Contains(t, out.Output(), "a=t,")
	require.NotContains(t, out.Output(), kittyMarkerPrefix)
}

func TestKittySplitAndUnknownMarkers(t *testing.T) {
	for _, split := range []int{1, 10, len(kittyMarkerPrefix), len(kittyMarkerPrefix) + 3} {
		t.Run(fmt.Sprint(split), func(t *testing.T) {
			k, term, out := kittyTestManager(t)
			lines, ok := k.render(kittyTestPNG(t, 8, 16), "image/png", 80)
			require.True(t, ok)
			term.WriteString(lines[0][:split])
			term.Write([]byte(lines[0][split:]))
			require.NotContains(t, out.Output(), kittyMarkerPrefix)
			require.Equal(t, 1, strings.Count(out.Output(), "a=t,"))
		})
	}
	k, term, out := kittyTestManager(t)
	term.WriteString("before" + kittyMarkerPrefix + "other;123\a" + kittyMarkerPrefix + k.nonce + ";123\aafter")
	require.Equal(t, "beforeafter", out.Output())
	out.Reset()
	term.WriteString(kittyMarkerPrefix + strings.Repeat("a", 1000))
	require.Empty(t, term.pending, "unterminated marker memory must stay bounded")
	term.WriteString("junk\aok\x1b[31mred\x1b[0m")
	require.Equal(t, "ok\x1b[31mred\x1b[0m", out.Output())
	out.Reset()
	term.WriteString(kittyMarkerPrefix + "unknown;123\x1b\\safe")
	require.Equal(t, "safe", out.Output())
	out.Reset()
	term.WriteString(kittyMarkerPrefix + strings.Repeat("x", 1000) + "\x1b")
	require.LessOrEqual(t, len(term.pending), 1)
	term.WriteString("\\also safe")
	require.Equal(t, "also safe", out.Output())
}

type kittyFailTerminal struct{ tuist.Terminal }

func (kittyFailTerminal) Start(func([]byte), func()) error { return errors.New("no tty") }

func TestKittyInactiveFallback(t *testing.T) {
	var absent *kittyImages
	require.False(t, absent.isActive())
	_, ok := absent.render("anything", "image/png", 80)
	require.False(t, ok)
	k := newKittyImages()
	require.False(t, k.isActive())
	_, ok = k.render(kittyTestPNG(t, 8, 16), "image/png", 80)
	require.False(t, ok)
	require.Empty(t, k.cache)
	term := k.wrapTerminal(kittyFailTerminal{})
	require.Error(t, term.Start(nil, nil))
	require.False(t, k.isActive())
}

func TestKittyCacheLimits(t *testing.T) {
	t.Run("attempts and negative caching", func(t *testing.T) {
		k, _, _ := kittyTestManager(t)
		for range 2 {
			_, ok := k.render("aGVsbG8=", "image/png", 80)
			require.False(t, ok)
		}
		require.Len(t, k.cache, 1)
		for i := range kittyMaxAttempts {
			k.render(base64.StdEncoding.EncodeToString([]byte(fmt.Sprint(i))), "image/png", 80)
		}
		require.Len(t, k.cache, kittyMaxAttempts)
		_, ok := k.render(kittyTestPNG(t, 8, 16), "image/png", 80)
		require.False(t, ok)
	})
	t.Run("image count", func(t *testing.T) {
		k, _, _ := kittyTestManager(t)
		data := kittyTestPNG(t, 8, 16)
		for width := 1; width <= kittyMaxImages; width++ {
			_, ok := k.render(data, "image/png", width)
			require.True(t, ok)
		}
		require.Len(t, k.byID, kittyMaxImages)
		_, ok := k.render(data, "image/png", kittyMaxImages+1)
		require.False(t, ok)
		_, ok = k.render(data, "image/png", 1)
		require.True(t, ok, "existing images remain valid at the limit")
	})
	t.Run("cache bytes", func(t *testing.T) {
		k, _, _ := kittyTestManager(t)
		k.cacheBytes = kittyMaxCacheBytes - 1
		_, ok := k.render(kittyTestPNG(t, 8, 16), "image/png", 80)
		require.False(t, ok)
		require.Empty(t, k.byID)
		require.Equal(t, kittyMaxCacheBytes-1, k.cacheBytes)
	})
}
