package idtui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fogleman/ease"
	"github.com/muesli/termenv"
)

type Spinner interface {
	tea.Model

	ViewFancy(time.Time) string
	ViewFrame(time.Time, SpinnerFrames) (string, int)
}

type Rave struct {
	// Show extra details useful for debugging a desynced rave.
	ShowDetails bool

	// The animation to display.
	Frames SpinnerFrames

	// color profile configured at start (to respect NO_COLOR etc)
	colorProfile termenv.Profile

	// refresh rate
	fps float64

	// user has opted into rave
	colors bool

	// reference point for setting the BPM
	lastBeat time.Time
	interval time.Duration
	bpm      float64
}

var _ Spinner = &Rave{}

var colors = []termenv.Color{
	termenv.ANSIRed,
	termenv.ANSIGreen,
	termenv.ANSIYellow,
	termenv.ANSIBlue,
	termenv.ANSIMagenta,
	termenv.ANSICyan,
}

// DefaultBPM is a sane default of 123 beats per minute.
const DefaultBPM = 123

// SpinnerFrames contains animation frames.
type SpinnerFrames struct {
	Frames []string
	Easing ease.Function
}

var MeterFrames = SpinnerFrames{
	[]string{"█", "█", "▇", "▆", "▅", "▄", "▃", "▂", "▁", " "},
	ease.InOutCubic,
}

var FadeFrames = SpinnerFrames{
	[]string{"█", "█", "▓", "▓", "▒", "▒", "░", "░", " ", " "},
	ease.InCubic,
}

var DotFrames = SpinnerFrames{
	[]string{"⣾", "⣷", "⣧", "⣏", "⡟", "⡿", "⢿", "⢻", "⣹", "⣼"},
	ease.Linear,
}

var MiniDotFrames = SpinnerFrames{
	[]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	ease.Linear,
}

func NewRave() *Rave {
	r := &Rave{
		Frames: DotFrames,

		colorProfile: ColorProfile(),
	}

	r.reset()

	return r
}

func (rave *Rave) reset() {
	rave.lastBeat = time.Now()
	rave.setBPM(DefaultBPM)
}

func (rave *Rave) Init() tea.Cmd {
	return nil
}

func (rave *Rave) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	// NB: these are captured and forwarded at the outer level.
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+@":
			last := rave.lastBeat
			rave.colors = true
			rave.Frames = FadeFrames
			rave.lastBeat = time.Now()
			rave.interval = rave.lastBeat.Sub(last)
			if !last.IsZero() {
				bpm := float64(time.Minute) / float64(rave.interval)
				rave.setBPM(bpm)
			}
			return rave, nil
		}

		return rave, nil

	default:
		return rave, nil
	}
}

func (rave *Rave) setBPM(bpm float64) {
	bps := bpm / 60.0
	framesPerBeat := len(rave.Frames.Frames)
	fps := bps * float64(framesPerBeat)
	fps *= 2 // decrease chance of missing a frame due to timing
	rave.bpm = bpm
	rave.interval = time.Duration(float64(time.Second) / bps)
}

func (rave *Rave) View() string {
	frame, _ := rave.ViewFrame(time.Now(), rave.Frames)
	return frame
}

func (rave *Rave) ViewFancy(now time.Time) string {
	frame, beats := rave.ViewFrame(now, rave.Frames)
	if rave.ShowDetails {
		frame += " " + rave.viewDetails()
	}

	if rave.colors {
		frame = rave.colorProfile.String(frame).
			Foreground(colors[beats%len(colors)]).
			String()
	}

	return frame
}

func (rave *Rave) ViewFrame(now time.Time, frames SpinnerFrames) (string, int) {
	framesPerBeat := len(frames.Frames)

	beats, pct := rave.Progress(now)

	frame := int(frames.Easing(pct) * float64(framesPerBeat))

	// some animations go > 100% or <100%, so be defensive and clamp to the
	// frames since that doesn't actually make sense
	if frame < 0 {
		frame = 0
	} else if frame >= framesPerBeat {
		frame = framesPerBeat - 1
	}

	return frames.Frames[frame], beats
}

func (model *Rave) viewDetails() string {
	return fmt.Sprintf(
		"%.1fbpm %.1ffps",
		model.bpm,
		model.fps,
	)
}

func (sched *Rave) Progress(now time.Time) (int, float64) {
	curBeatStart := sched.lastBeat
	sinceLastBeat := now.Sub(curBeatStart)
	beats := int(sinceLastBeat / sched.interval)

	start := sched.lastBeat.Add(time.Duration(beats) * sched.interval)

	// found the current beat, and how far we are within it
	return beats, float64(now.Sub(start)) / float64(sched.interval)
}
