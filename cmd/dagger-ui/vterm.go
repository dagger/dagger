package main

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/morikuni/aec"
	"github.com/vito/vt100"
)

type Vterm struct {
	vt *vt100.VT100

	Offset int
	Height int
}

func NewVterm(width int) *Vterm {
	vt := vt100.NewVT100(1, width)
	vt.AutoResize = true
	return &Vterm{
		vt: vt,
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
	switch msg := msg.(type) {
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

func (term *Vterm) View() string {
	used := term.vt.UsedHeight()

	lines := []string{}
	for row, l := range term.vt.Content {
		if row < term.Offset {
			continue
		}
		if row+1 > (term.Offset + term.Height) {
			break
		}

		var lastFormat vt100.Format

		var line string
		for col, r := range l {
			f := term.vt.Format[row][col]

			if f != lastFormat {
				lastFormat = f
				line += renderFormat(f)
			}

			line += string(r)
		}

		line += aec.Reset

		lines = append(lines, line)

		if row > used {
			break
		}
	}

	for i := len(lines); i < term.Height; i++ {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}

// TODO: 256colors
func renderFormat(f vt100.Format) string {
	if f == (vt100.Format{}) {
		return aec.Reset
	}

	b := aec.EmptyBuilder

	if f.FgBright {
		switch f.Fg {
		case vt100.Black:
			b = b.LightBlackF()
		case vt100.Red:
			b = b.LightRedF()
		case vt100.Green:
			b = b.LightGreenF()
		case vt100.Yellow:
			b = b.LightYellowF()
		case vt100.Blue:
			b = b.LightBlueF()
		case vt100.Magenta:
			b = b.LightMagentaF()
		case vt100.Cyan:
			b = b.LightCyanF()
		case vt100.White:
			b = b.LightWhiteF()
		}
	} else {
		switch f.Fg {
		case vt100.Black:
			b = b.BlackF()
		case vt100.Red:
			b = b.RedF()
		case vt100.Green:
			b = b.GreenF()
		case vt100.Yellow:
			b = b.YellowF()
		case vt100.Blue:
			b = b.BlueF()
		case vt100.Magenta:
			b = b.MagentaF()
		case vt100.Cyan:
			b = b.CyanF()
		case vt100.White:
			b = b.WhiteF()
		}
	}

	if f.BgBright {
		switch f.Bg {
		case vt100.Black:
			b = b.LightBlackB()
		case vt100.Red:
			b = b.LightRedB()
		case vt100.Green:
			b = b.LightGreenB()
		case vt100.Yellow:
			b = b.LightYellowB()
		case vt100.Blue:
			b = b.LightBlueB()
		case vt100.Magenta:
			b = b.LightMagentaB()
		case vt100.Cyan:
			b = b.LightCyanB()
		case vt100.White:
			b = b.LightWhiteB()
		}
	} else {
		switch f.Bg {
		case vt100.Black:
			b = b.BlackB()
		case vt100.Red:
			b = b.RedB()
		case vt100.Green:
			b = b.GreenB()
		case vt100.Yellow:
			b = b.YellowB()
		case vt100.Blue:
			b = b.BlueB()
		case vt100.Magenta:
			b = b.MagentaB()
		case vt100.Cyan:
			b = b.CyanB()
		case vt100.White:
			b = b.WhiteB()
		}
	}

	switch f.Intensity {
	case vt100.Bold:
		b = b.Bold()
	case vt100.Dim:
		b = b.Faint()
	}

	return b.ANSI.String()
}
