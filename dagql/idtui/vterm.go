package idtui

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/vito/midterm"
)

type Vterm struct {
	Offset int
	Height int
	Width  int

	Prefix string

	Background termenv.Color
	Profile    termenv.Profile

	vt *midterm.Terminal

	// Separate buffer for Markdown content
	markdownBuf *bytes.Buffer
	// Regular terminal buffer
	viewBuf     *bytes.Buffer
	needsRedraw bool

	mu *sync.Mutex
}

func NewVterm(profile termenv.Profile) *Vterm {
	return &Vterm{
		Profile:     profile,
		vt:          midterm.NewAutoResizingTerminal(),
		viewBuf:     new(bytes.Buffer),
		markdownBuf: new(bytes.Buffer),
		mu:          new(sync.Mutex),
	}
}

func (term *Vterm) Term() *midterm.Terminal {
	return term.vt
}

func (term *Vterm) WriteMarkdown(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	n, err := term.markdownBuf.Write(p)
	if err != nil {
		return n, err
	}

	term.needsRedraw = true
	return n, nil
}

func (term *Vterm) Write(p []byte) (int, error) {
	term.mu.Lock()
	defer term.mu.Unlock()

	atBottom := term.Offset+term.Height >= term.vt.UsedHeight()
	if term.Height == 0 {
		atBottom = true
	}

	n, err := term.vt.Write(p)
	if err != nil {
		return n, err
	}

	if atBottom {
		term.Offset = max(0, term.vt.UsedHeight()-term.Height)
	}

	term.needsRedraw = true

	return n, nil
}

func (term *Vterm) UsedHeight() int {
	return term.vt.UsedHeight()
}

func (term *Vterm) SetHeight(height int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if height == term.Height {
		return
	}
	atBottom := term.Offset+term.Height >= term.vt.UsedHeight()
	term.Height = height
	if atBottom {
		term.Offset = max(0, term.vt.UsedHeight()-term.Height)
	}
	term.needsRedraw = true
}

func (term *Vterm) SetWidth(width int) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if width == term.Width {
		return
	}
	term.Width = width
	prefixWidth := lipgloss.Width(term.Prefix)
	if width > prefixWidth {
		term.vt.ResizeX(width - prefixWidth)
	}
	term.needsRedraw = true
}

func (term *Vterm) SetPrefix(prefix string) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if prefix == term.Prefix {
		return
	}
	term.Prefix = prefix
	prefixWidth := lipgloss.Width(prefix)
	if term.Width > prefixWidth && !term.vt.AutoResizeX {
		term.vt.ResizeX(term.Width - prefixWidth)
	}
	term.needsRedraw = true
}

func (term *Vterm) SetBackground(background termenv.Color) {
	term.mu.Lock()
	defer term.mu.Unlock()
	if background == term.Background {
		return
	}
	term.Background = background
	term.needsRedraw = true
}

func (term *Vterm) Init() tea.Cmd {
	return nil
}

func (term *Vterm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) { //nolint:gocritic
	case tea.KeyMsg:
		_ = msg
		switch {
		// case key.Matches(msg, Keys.Up):
		// 	term.Offset = max(0, term.Offset-1)
		// case key.Matches(msg, Keys.Down):
		// 	term.Offset = min(term.vt.UsedHeight()-term.Height, term.Offset+1)
		// case key.Matches(msg, Keys.PageUp):
		// 	term.Offset = max(0, term.Offset-term.Height)
		// case key.Matches(msg, Keys.PageDown):
		// 	term.Offset = min(term.vt.UsedHeight()-term.Height, term.Offset+term.Height)
		// case key.Matches(msg, Keys.Home):
		// 	term.Offset = 0
		// case key.Matches(msg, Keys.End):
		// 	term.Offset = term.vt.UsedHeight() - term.Height
		}
	}
	return term, nil
}

func (term *Vterm) ScrollPercent() float64 {
	return min(1, float64(term.Offset+term.Height)/float64(term.vt.UsedHeight()))
}

const reset = termenv.CSI + termenv.ResetSeq + "m"

// View returns the output for the current region of the terminal, with ANSI
// formatting or rendered Markdown if present.
func (term *Vterm) View() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	if term.needsRedraw {
		term.redraw()
		term.needsRedraw = false
	}
	return term.viewBuf.String()
}

var style = styles.LightStyleConfig

func init() {
	if isDark &&
		// termenv is unable to detect Windows Terminal (at least) background
		// color, and light Markdown on light background is unreadable, so we
		// use this env var to force the color.
		os.Getenv("LIGHT") == "" {
		style = styles.DarkStyleConfig
	}
	style.Document.Margin = nil
}

func (term *Vterm) redraw() {
	term.viewBuf.Reset()

	// First render any Markdown content
	if term.markdownBuf.Len() > 0 {
		st := style
		// HACK: we want "0" or "255", but termenv.Color doesn't have a
		// String() method, only Sequence(bool) which prints the ANSI
		// formatting sequence.
		if term.Background != nil {
			switch x := term.Background.(type) {
			case termenv.ANSIColor, termenv.ANSI256Color:
				// annoyingly, there's no clean conversion from termenv.Color
				// back to the value that lipgloss wants, because ANSI 0
				// translates to "#000000" and we want "0"
				bg := fmt.Sprintf("%d", x)
				st.Document.BackgroundColor = &bg
			default:
				bg := fmt.Sprint(term.Background)
				st.Document.BackgroundColor = &bg
			}
		}
		renderer, _ := glamour.NewTermRenderer(
			glamour.WithWordWrap(term.Width-lipgloss.Width(term.Prefix)),
			glamour.WithStyles(st),
		)

		rendered, err := renderer.Render(term.markdownBuf.String())
		if err != nil {
			fmt.Fprintf(term.viewBuf, "Error rendering Markdown: %s\n", err)
		} else {
			// Remove leading and trailing newlines
			rendered = strings.TrimSpace(rendered)
			// Add prefix to each line of rendered Markdown
			lines := strings.Split(rendered, "\n")
			for i, line := range lines {
				if i > 0 {
					fmt.Fprint(term.viewBuf, term.Prefix)
				}
				fmt.Fprintln(term.viewBuf, line)
			}
		}
	}

	// Then render regular terminal content
	term.Render(term.viewBuf, term.Offset, term.Height)
}

// Bytes returns the output for the given region of the terminal, with
// ANSI formatting.
func (term *Vterm) Render(w io.Writer, offset, height int) {
	used := term.vt.UsedHeight()
	if used == 0 {
		return
	}

	var vt TermOutput = NewOutput(w, termenv.WithProfile(term.Profile))
	if term.Background != nil {
		vt = NewBackgroundOutput(vt, term.Background)
	}

	w = vt

	var lines int
	for row := range term.vt.Content {
		if row < offset {
			continue
		}
		if row+1 > (offset + height) {
			break
		}

		fmt.Fprint(w, vt.String(term.Prefix))
		term.vt.RenderLineFgBg(w, row, nil, term.Background)
		fmt.Fprintln(w)
		lines++

		if row > used {
			break
		}
	}
}

// LastLine returns the last line of visible text, with ANSI formatting, but
// without any trailing whitespace.
func (term *Vterm) LastLine() string {
	used := term.vt.UsedHeight()
	if used == 0 {
		return ""
	}
	var lastLine string
	for row := used - 1; row >= 0; row-- {
		buf := new(strings.Builder)
		_ = term.vt.RenderLine(buf, row)
		if strings.TrimSpace(buf.String()) == "" {
			continue
		}
		lastLine = strings.TrimRightFunc(buf.String(), unicode.IsSpace)
		break
	}
	return lastLine + reset
}

// Print prints the full log output without any formatting.
func (term *Vterm) Print(w io.Writer) error {
	used := term.vt.UsedHeight()

	for row, l := range term.vt.Content {
		_, err := fmt.Fprintln(w, strings.TrimRight(string(l), " "))
		if err != nil {
			return err
		}

		if row > used {
			break
		}
	}

	return nil
}
