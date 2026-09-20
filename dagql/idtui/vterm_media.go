package idtui

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/midterm"
)

// Text and media have separate representations: an image's encoded data and
// placement rows must never be interpreted by either Glamour or midterm.
type vtermSegment struct {
	text     string
	markdown bool
	media    *dagui.MediaRecord
}

type vtermMediaRow struct {
	text     string
	image    bool
	noPrefix bool
}

// WriteMedia appends a media block with a safe, searchable textual description.
// The payload is kept only in the media segment, never in the raw log buffer.
func (term *Vterm) WriteMedia(media dagui.MediaRecord, fallback string) {
	term.mu.Lock()
	defer term.mu.Unlock()

	atBottom := term.Height == 0 || term.Offset+term.Height >= term.usedHeightLocked()
	if term.segments == nil {
		term.segments = []vtermSegment{}
		// Preserve the pre-media renderer's ordering of Markdown then terminal
		// output. After this boundary every write is ordered by arrival.
		if term.markdownBuf.Len() > 0 {
			term.segments = append(term.segments, vtermSegment{text: term.markdownBuf.String(), markdown: true})
		}
		if used := term.vt.UsedHeight(); used > 0 {
			// Highlights belong to the search projection, not the saved text.
			term.vt.SearchClear()
			var snapshot strings.Builder
			for row := 0; row < used; row++ {
				if row > 0 {
					snapshot.WriteString("\r\n")
				}
				term.vt.RenderLineFgBg(&snapshot, row, nil, nil)
			}
			term.segments = append(term.segments, vtermSegment{text: snapshot.String()})
		}
		term.markdownBuf.Reset()
	}
	fallback = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, ansi.Strip(fallback)))
	if fallback == "" {
		fallback = "[media]"
	}
	term.segments = append(term.segments, vtermSegment{text: fallback, media: &media})
	if b := term.rawBuf.Bytes(); len(b) > 0 && b[len(b)-1] != '\n' {
		term.rawBuf.WriteByte('\n')
	}
	term.rawBuf.WriteString(fallback)
	term.rawBuf.WriteByte('\n')
	term.invalidateMedia()
	if atBottom {
		term.Offset = max(0, term.usedHeightLocked()-term.Height)
	}
}

// writeMediaText is called with term.mu held. Adjacent chunks of the same text
// kind are coalesced so streaming Markdown and terminal escape sequences work.
func (term *Vterm) writeMediaText(p []byte, markdown, diff bool) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	atBottom := term.Height == 0 || term.Offset+term.Height >= term.usedHeightLocked()
	text := string(p)
	if diff {
		text = highlightDiff(term.Profile, text)
	}
	n := len(term.segments)
	if n > 0 && term.segments[n-1].media == nil && term.segments[n-1].markdown == markdown {
		term.segments[n-1].text += text
	} else {
		term.segments = append(term.segments, vtermSegment{text: text, markdown: markdown})
	}
	term.rawBuf.Write(p)
	term.invalidateMedia()
	if atBottom {
		term.Offset = max(0, term.usedHeightLocked()-term.Height)
	}
	return len(p), nil
}

func (term *Vterm) invalidateMedia() {
	term.mediaRows = nil
	term.needsRedraw = true
}

func (term *Vterm) mediaWidth() int {
	if term.Width == 0 {
		return 80
	}
	return max(1, term.Width-lipgloss.Width(term.Prefix))
}

// layoutMedia only prepares rows. kittyImages.render returns cached placement
// references; uploads happen at the actual terminal writer, not during layout.
func (term *Vterm) layoutMedia() {
	active := term.Profile != termenv.Ascii && term.images.isActive()
	if term.mediaRows != nil && active == term.mediaActive {
		return
	}
	term.mediaActive = active
	term.mediaRows = []vtermMediaRow{}
	width := term.mediaWidth()
	for _, segment := range term.segments {
		if segment.media != nil {
			for _, line := range strings.Split(ansi.Hardwrap(segment.text, width, true), "\n") {
				term.mediaRows = append(term.mediaRows, vtermMediaRow{text: line})
			}
			if active && segment.media.Kind == "image" {
				if lines, ok := term.images.render(segment.media.Data, segment.media.MIMEType, width); ok {
					for _, line := range lines {
						term.mediaRows = append(term.mediaRows, vtermMediaRow{text: line, image: true})
					}
				}
			}
			continue
		}
		var lines []string
		if segment.markdown {
			renderer, err := glamour.NewTermRenderer(
				glamour.WithWordWrap(width), glamour.WithStyles(MarkdownStyle),
				glamour.WithColorProfile(termenv.ANSI), glamour.WithChromaFormatter("terminal16"),
				glamour.WithPreservedNewLines(), glamour.WithEmoji(),
			)
			if err == nil {
				var rendered string
				rendered, err = renderer.Render(segment.text)
				lines = strings.Split(strings.TrimSpace(rendered), "\n")
			}
			if err != nil {
				lines = []string{fmt.Sprintf("Error rendering Markdown: %s", err)}
			}
		} else {
			terminal := midterm.NewAutoResizingTerminal()
			terminal.ResizeX(width)
			_, _ = terminal.Write([]byte(segment.text))
			for row := 0; row < terminal.UsedHeight(); row++ {
				var line strings.Builder
				terminal.RenderLineFgBg(&line, row, nil, nil)
				lines = append(lines, line.String())
			}
		}
		for _, line := range lines {
			for _, wrapped := range strings.Split(ansi.Hardwrap(line, width, true), "\n") {
				term.mediaRows = append(term.mediaRows, vtermMediaRow{
					text: wrapped, noPrefix: segment.markdown && len(term.mediaRows) == 0,
				})
			}
		}
	}
	term.Offset = min(term.Offset, max(0, len(term.mediaRows)-term.Height))

	// A text-only projection preserves the existing midterm search API, with
	// blank rows reserving image space. No media bytes or markers enter it.
	term.vt = midterm.NewAutoResizingTerminal()
	for i, row := range term.mediaRows {
		if i > 0 {
			_, _ = term.vt.Write([]byte("\r\n"))
		}
		if !row.image {
			_, _ = term.vt.Write([]byte(row.text))
		}
	}
	if term.SearchQuery != "" {
		term.vt.Search(term.SearchQuery)
		term.setCurrentMatchByRow(term.SearchCurrentRow)
	}
}

func (term *Vterm) renderMedia(w io.Writer, offset, height int) {
	end := min(len(term.mediaRows), max(0, offset)+max(0, height))
	for i := max(0, offset); i < end; i++ {
		row := term.mediaRows[i]
		prefix := term.Prefix
		if row.noPrefix {
			prefix = ""
		}
		text := row.text
		if !row.image && term.SearchQuery != "" {
			var highlighted strings.Builder
			term.vt.RenderLineFgBg(&highlighted, i, nil, nil)
			text = highlighted.String()
		}
		if term.Profile == termenv.Ascii {
			prefix, text = ansi.Strip(prefix), ansi.Strip(text)
		}
		fmt.Fprintln(w, prefix+text)
	}
}
