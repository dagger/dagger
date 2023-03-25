package tui

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/vito/vt100"
)

type Vterm struct {
	vt *vt100.VT100

	viewBuf *bytes.Buffer

	Offset int
	Height int
}

var debugVterm = os.Getenv("_DEBUG_VTERM") != ""

func NewVterm(width int) *Vterm {
	vt := vt100.NewVT100(1, width)
	vt.AutoResize = true
	if debugVterm {
		vt.DebugLogs = os.Stderr
	}
	return &Vterm{
		vt:      vt,
		viewBuf: new(bytes.Buffer),
	}
}

func (term *Vterm) Write(p []byte) (int, error) {
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

	return n, nil
}

func (term *Vterm) SetHeight(height int) {
	atBottom := term.Offset+term.Height >= term.vt.UsedHeight()

	term.Height = height

	if atBottom {
		term.Offset = max(0, term.vt.UsedHeight()-term.Height)
	}
}

func (term *Vterm) SetWidth(width int) {
	term.vt.Resize(term.vt.Height, width)
}

func (term *Vterm) Init() tea.Cmd {
	return nil
}

func (term *Vterm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) { // nolint:gocritic
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Up):
			term.Offset = max(0, term.Offset-1)
		case key.Matches(msg, keys.Down):
			term.Offset = min(term.vt.UsedHeight()-term.Height, term.Offset+1)
		}
	}
	return term, nil
}

func (term *Vterm) ScrollPercent() float64 {
	return min(1, float64(term.Offset+term.Height)/float64(term.vt.UsedHeight()))
}

const reset = termenv.CSI + termenv.ResetSeq + "m"

func (term *Vterm) View() string {
	used := term.vt.UsedHeight()

	buf := term.viewBuf
	buf.Reset()

	var lines int
	for row, l := range term.vt.Content {
		if row < term.Offset {
			continue
		}
		if row+1 > (term.Offset + term.Height) {
			break
		}

		var lastFormat vt100.Format

		for col, r := range l {
			f := term.vt.Format[row][col]

			if f != lastFormat {
				lastFormat = f
				buf.Write([]byte(renderFormat(f)))
			}

			buf.Write([]byte(string(r)))
		}

		buf.Write([]byte(reset + "\n"))
		lines++

		if row > used {
			break
		}
	}

	for i := lines; i < term.Height; i++ {
		buf.Write([]byte("\n"))
	}

	// discard final trailing linebreak
	return buf.String()[0 : buf.Len()-1]
}

func renderFormat(f vt100.Format) string {
	styles := []string{}
	if f.Fg != nil {
		styles = append(styles, f.Fg.Sequence(false))
	}
	if f.Bg != nil {
		styles = append(styles, f.Bg.Sequence(true))
	}

	switch f.Intensity {
	case vt100.Bold:
		styles = append(styles, termenv.BoldSeq)
	case vt100.Faint:
		styles = append(styles, termenv.FaintSeq)
	}

	if f.Italic {
		styles = append(styles, termenv.ItalicSeq)
	}

	if f.Underline {
		styles = append(styles, termenv.UnderlineSeq)
	}

	if f.Blink {
		styles = append(styles, termenv.BlinkSeq)
	}

	if f.Reverse {
		styles = append(styles, termenv.ReverseSeq)
	}

	if f.Conceal {
		styles = append(styles, "8")
	}

	if f.CrossOut {
		styles = append(styles, termenv.CrossOutSeq)
	}

	if f.Overline {
		styles = append(styles, termenv.OverlineSeq)
	}

	var res string
	if f.Reset || f == (vt100.Format{}) {
		res = reset
	}
	if len(styles) > 0 {
		res += fmt.Sprintf("%s%sm", termenv.CSI, strings.Join(styles, ";"))
	}
	return res
}
