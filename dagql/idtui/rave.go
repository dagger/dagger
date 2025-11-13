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

	// user has opted into rave
	colors bool

	// reference point for setting the BPM
	lastBeat time.Time
	interval time.Duration
	bpm      float64

	// moving average for BPM calculation
	recentIntervals []time.Duration
	maxIntervals    int
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
	rave.maxIntervals = 5 // track last 5 intervals for moving average
	rave.recentIntervals = make([]time.Duration, 0, rave.maxIntervals)
	rave.setBPM(DefaultBPM)
}

func (rave *Rave) Init() tea.Cmd {
	return nil
}

func (rave *Rave) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	// NB: these are captured and forwarded at the outer level.
	case tea.KeyMsg:
		if msg.String() == "ctrl+@" {
			last := rave.lastBeat
			rave.colors = true
			rave.Frames = FadeFrames
			rave.lastBeat = time.Now()
			if !last.IsZero() {
				interval := rave.lastBeat.Sub(last)
				rave.addInterval(interval)
				bpm := rave.calculateMovingAverageBPM()
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
	rave.bpm = bpm
	rave.interval = time.Duration(float64(time.Minute) / bpm)
}

func (rave *Rave) addInterval(interval time.Duration) {
	// If interval is extremely long (>10 seconds), reset and start fresh
	if interval > 10*time.Second {
		rave.recentIntervals = make([]time.Duration, 0, rave.maxIntervals)
		rave.recentIntervals = append(rave.recentIntervals, interval)
		return
	}

	// If we have existing intervals, check if this one is an outlier (likely a pause)
	if len(rave.recentIntervals) > 0 {
		var total time.Duration
		for _, existing := range rave.recentIntervals {
			total += existing
		}
		avgInterval := total / time.Duration(len(rave.recentIntervals))

		// If the new interval is more than 3x the average, it's likely a pause - ignore it
		if interval > avgInterval*3 {
			return
		}
	}

	// Add new interval to the slice
	rave.recentIntervals = append(rave.recentIntervals, interval)

	// Keep only the most recent maxIntervals intervals
	if len(rave.recentIntervals) > rave.maxIntervals {
		rave.recentIntervals = rave.recentIntervals[1:]
	}
}

func (rave *Rave) calculateMovingAverageBPM() float64 {
	if len(rave.recentIntervals) == 0 {
		return DefaultBPM
	}

	// Calculate average interval
	var total time.Duration
	for _, interval := range rave.recentIntervals {
		total += interval
	}
	avgInterval := total / time.Duration(len(rave.recentIntervals))

	// Convert to BPM
	return float64(time.Minute) / float64(avgInterval)
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

func (rave *Rave) viewDetails() string {
	details := fmt.Sprintf("%.1fbpm", rave.bpm)
	if len(rave.recentIntervals) > 0 {
		details += fmt.Sprintf(" (avg of %d)", len(rave.recentIntervals))
	}
	return details
}

func (rave *Rave) Progress(now time.Time) (int, float64) {
	curBeatStart := rave.lastBeat
	sinceLastBeat := now.Sub(curBeatStart)
	beats := int(sinceLastBeat / rave.interval)

	start := rave.lastBeat.Add(time.Duration(beats) * rave.interval)

	// found the current beat, and how far we are within it
	return beats, float64(now.Sub(start)) / float64(rave.interval)
}
