package idtui

import (
	"context"
	"errors"
	"image/color"
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/require"
	"github.com/vito/tuist"
)

func TestBlendPromptBackground(t *testing.T) {
	for _, tc := range []struct {
		name string
		bg   color.Color
		want color.RGBA
	}{
		{"black", color.Black, color.RGBA{30, 30, 30, 255}},
		{"white", color.White, color.RGBA{244, 244, 244, 255}},
		{"dark tint", color.RGBA{20, 30, 40, 255}, color.RGBA{48, 57, 65, 255}},
		{"light tint", color.RGBA{240, 230, 220, 255}, color.RGBA{230, 220, 211, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := blendPromptBackground(tc.bg, termenv.TrueColor)
			require.Equal(t, tc.want, got.cell)
			require.Equal(t, promptRGBColor(tc.want), got.term)
		})
	}
	indexed := blendPromptBackground(color.Black, termenv.ANSI256)
	require.IsType(t, termenv.ANSI256Color(0), indexed.term)
	require.IsType(t, ansi.IndexedColor(0), indexed.cell)
	require.Equal(t, ansi.IndexedColor(indexed.term.(termenv.ANSI256Color)), indexed.cell)
	require.GreaterOrEqual(t, int(indexed.term.(termenv.ANSI256Color)), 16, "do not assume RGB values for theme-defined ANSI colors")
	for _, profile := range []termenv.Profile{termenv.Ascii, termenv.ANSI} {
		require.Equal(t, promptBackground{}, blendPromptBackground(color.Black, profile))
	}
	require.Equal(t, promptBackground{}, blendPromptBackground(nil, termenv.TrueColor))
}

func TestPromptColorQueryEligibility(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("CLICOLOR", "1")
	t.Setenv("CLICOLOR_FORCE", "")
	t.Setenv("COLORTERM", "")
	t.Setenv("GOOGLE_CLOUD_SHELL", "")
	for _, tc := range []struct {
		term    string
		profile termenv.Profile
		query   bool
	}{
		{"xterm-kitty", termenv.ANSI, true},
		{"xterm-256color", termenv.ANSI, true},
		{"xterm", termenv.ANSI, true},
		{"dumb", termenv.ANSI, false},
		{"xterm-kitty", termenv.Ascii, false},
	} {
		t.Run(tc.term+tc.profile.Name(), func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			fe := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewStdTerminal(), tc.profile)
			probe, queries := fe.term.(*promptColorTerminal)
			require.Equal(t, tc.query, queries)
			if queries {
				require.Equal(t, tc.term != "xterm-kitty", probe.queryCapabilities)
			}
			require.Equal(t, promptBackground{}, fe.promptBackground)
		})
	}
	t.Setenv("TERM", "xterm-kitty")
	headless := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(40, 10), termenv.ANSI)
	_, queries := headless.term.(*promptColorTerminal)
	require.False(t, queries)
	require.Equal(t, termenv.Ascii, headless.promptColorProfile)
	t.Setenv("NO_COLOR", "1")
	noColor := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewStdTerminal(), termenv.ANSI)
	_, queries = noColor.term.(*promptColorTerminal)
	require.False(t, queries)
}

type promptProbeTerminal struct {
	tuist.Terminal
	started           bool
	startErr          error
	queries           int
	capabilityQueries []string
	onInput           func([]byte)
	reply             bool
}

func (t *promptProbeTerminal) Start(onInput func([]byte), _ func()) error {
	if t.startErr != nil {
		return t.startErr
	}
	t.started = true
	t.onInput = onInput
	return nil
}

func (t *promptProbeTerminal) WriteString(s string) {
	for capability, reply := range map[string]string{
		"RGB": "\x1bP1+r524742\x1b\\",
		"Tc":  "\x1bP0+r5463\x1b\\", // unsupported: must not produce a capability event
		"Co":  "\x1bP1+r436f=323536\x1b\\",
	} {
		if s == ansi.RequestTermcap(capability) {
			if !t.started {
				panic("queried capabilities before starting input")
			}
			t.capabilityQueries = append(t.capabilityQueries, capability)
			if t.reply {
				// Split inside the DCS payload to exercise the existing reader.
				t.onInput([]byte(reply[:6]))
				t.onInput([]byte(reply[6:]))
			}
			return
		}
	}
	if s != ansi.RequestBackgroundColor {
		return
	}
	if !t.started {
		panic("queried colors before starting input")
	}
	t.queries++
	if t.reply {
		// Both fragmented replies and surrounding keyboard input use Tuist's
		// existing reader. OSC payloads must never be dispatched as keystrokes.
		for _, chunk := range []string{"a\x1b]11;rgb:", "0000/0000/", "0000\x1b\\b"} {
			t.onInput([]byte(chunk))
		}
	}
}

func TestPromptColorQueryLifecycle(t *testing.T) {
	terminal := &promptProbeTerminal{Terminal: tuist.NewHeadlessTerminal(40, 10)}
	probe := &promptColorTerminal{Terminal: terminal, queryCapabilities: true}
	// No reply is required to start, and a resume reissues the queries.
	require.NoError(t, probe.Start(func([]byte) {}, func() {}))
	require.Equal(t, 1, terminal.queries)
	require.Equal(t, []string{"RGB", "Tc", "Co"}, terminal.capabilityQueries)
	probe.Stop()
	require.NoError(t, probe.Start(func([]byte) {}, func() {}))
	require.Equal(t, 2, terminal.queries)
	terminal.startErr = errors.New("no terminal")
	require.ErrorIs(t, probe.Start(func([]byte) {}, func() {}), terminal.startErr)
	require.Equal(t, 2, terminal.queries)
	require.Equal(t, []string{"RGB", "Tc", "Co", "RGB", "Tc", "Co"}, terminal.capabilityQueries)
}

func TestPromptColorQueryUsesTuistReader(t *testing.T) {
	terminal := &promptProbeTerminal{Terminal: tuist.NewHeadlessTerminal(40, 10), reply: true}
	tui := tuist.New(&promptColorTerminal{Terminal: terminal, queryCapabilities: true})
	events := make(chan uv.Event, 8)
	tui.AddInputListener(func(_ tuist.Context, event uv.Event) bool {
		switch event.(type) {
		case uv.KeyPressEvent, uv.BackgroundColorEvent, uv.CapabilityEvent:
			events <- event
		}
		return true
	})
	require.NoError(t, tui.Start())
	defer tui.Stop()
	var got []uv.Event
	for len(got) < 5 {
		select {
		case event := <-events:
			got = append(got, event)
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for decoded terminal events")
		}
	}
	require.Equal(t, "a", got[0].(uv.KeyPressEvent).String())
	reply, ok := got[1].(uv.BackgroundColorEvent)
	require.True(t, ok, "%T", got[1])
	r, g, b, _ := reply.Color.RGBA()
	require.Zero(t, r|g|b)
	require.Equal(t, "b", got[2].(uv.KeyPressEvent).String())
	require.Equal(t, uv.CapabilityEvent{Content: "RGB"}, got[3])
	require.Equal(t, uv.CapabilityEvent{Content: "Co=256"}, got[4])
}

func TestPromptCapabilityProfile(t *testing.T) {
	for _, tc := range []struct {
		content string
		want    termenv.Profile
	}{
		{"RGB", termenv.TrueColor},
		{"Tc", termenv.TrueColor},
		{"RGB=8", termenv.TrueColor},
		{"Tc=1", termenv.TrueColor},
		{"Co=256", termenv.ANSI256},
		{"colors=256", termenv.ANSI256},
		{"Co=16777216", termenv.TrueColor},
		{"RGB;Co=256", termenv.TrueColor},
		{"Co=256;RGB", termenv.TrueColor},
		{"Co=16", termenv.Ascii},
		{"RGB=0", termenv.Ascii},
		{"Tc=-1", termenv.Ascii},
		{"Tc=garbage", termenv.Ascii},
		{"RGB=", termenv.Ascii},
		{"Co=9999999999999999999999999", termenv.Ascii},
		{"TN=xterm-kitty", termenv.Ascii},
		{"", termenv.Ascii},
	} {
		t.Run(tc.content, func(t *testing.T) {
			require.Equal(t, tc.want, promptCapabilityProfile(tc.content))
		})
	}
}

func TestPromptCapabilityAndBackgroundReplyOrder(t *testing.T) {
	for _, capability := range []string{"RGB", "Co=256"} {
		for _, order := range []string{"background first", "capability first"} {
			t.Run(capability+"/"+order, func(t *testing.T) {
				fe := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(40, 10), termenv.ANSI)
				// Dagger's nested PTY defaults to TERM=xterm, with no COLORTERM.
				fe.promptColorProfile = termenv.ANSI
				events := []uv.Event{uv.BackgroundColorEvent{Color: color.Black}, uv.CapabilityEvent{Content: capability}}
				if order == "capability first" {
					events[0], events[1] = events[1], events[0]
				}
				fe.tui.Inject(events[0])
				fe.tui.Step()
				require.Equal(t, promptBackground{}, fe.promptBackground, "need both color and capability")
				fe.tui.Inject(events[1])
				fe.tui.Step()
				profile := promptCapabilityProfile(capability)
				require.Equal(t, profile, fe.promptColorProfile)
				require.Equal(t, blendPromptBackground(color.Black, profile), fe.promptBackground)
				// Separate replies can arrive in any order without downgrading
				// true color, and unsupported reports must not enable anything.
				fe.tui.Inject(uv.CapabilityEvent{Content: "Co=16"})
				fe.tui.Inject(uv.CapabilityEvent{Content: "Co=256"})
				fe.tui.Step()
				require.Equal(t, profile, fe.promptColorProfile)
			})
		}
	}
}

func TestPromptBackgroundReplyUpdatesDraftAndHistory(t *testing.T) {
	db := dagui.NewDB()
	rootID, userID := prettyTestSpanID(1), prettyTestSpanID(2)
	start := time.Unix(100, 0)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: rootID, TraceID: prettyTestTraceID(), Name: "shell", StartTime: start},
		{ID: userID, TraceID: prettyTestTraceID(), Name: "LLM prompt", Message: "received", LLMRole: "user", ParentID: rootID, StartTime: start, EndTime: start.Add(time.Second), Final: true},
	})
	db.SetPrimarySpan(rootID)
	fe := newWithTerminalProfile(io.Discard, db, tuist.NewHeadlessTerminal(60, 20), termenv.ANSI)
	fe.promptColorProfile = termenv.ANSI
	fe.setupTUI()
	fe.startShell(context.Background(), stubShellHandler{})
	fe.promptFrame.SetEnabled(true)
	fe.textInput.SetValue("draft")
	fe.FrontendOpts.Verbosity = dagui.ShowCompletedVerbosity
	logs := NewVterm(termenv.ANSI)
	logs.SetWidth(60)
	_, err := logs.WriteMarkdown([]byte("submitted\n"))
	require.NoError(t, err)
	fe.logs.Logs[userID] = logs
	fe.recalculateViewLocked()
	fe.tui.Step()
	before := strings.Join(fe.tui.Frame(), "\n")
	require.Contains(t, before, "submitted")
	require.Contains(t, before, "draft")
	require.NotContains(t, before, "48;2;")
	require.NotContains(t, before, "\x1b[100m")

	// Receiving the background alone is not proof of extended color support.
	fe.tui.Inject(uv.BackgroundColorEvent{Color: color.Black})
	fe.tui.Step()
	require.Equal(t, promptBackground{}, fe.promptBackground)
	for _, capability := range []string{"Co=256", "RGB"} {
		fe.tui.Inject(uv.CapabilityEvent{Content: capability})
		fe.tui.Step()
		want := blendPromptBackground(color.Black, promptCapabilityProfile(capability))
		require.Equal(t, want, fe.promptBackground)
		frame := strings.Join(fe.tui.Frame(), "\n")
		sequence := "\x1b[" + want.term.Sequence(true) + "m"
		require.True(t, containsStyledLine(frame, "submitted", sequence), visibleEscapes(frame))
		require.True(t, containsStyledLine(frame, "draft", sequence), visibleEscapes(frame))
	}

	for _, bg := range []color.Color{color.Black, color.White} {
		fe.tui.Inject(uv.BackgroundColorEvent{Color: bg})
		fe.tui.Step()
		want := blendPromptBackground(bg, termenv.TrueColor)
		require.Equal(t, want, fe.promptBackground)
		require.Equal(t, want.cell, fe.promptFrame.background)
		frame := strings.Join(fe.tui.Frame(), "\n")
		sequence := "\x1b[" + want.term.Sequence(true) + "m"
		require.True(t, containsStyledLine(frame, "submitted", sequence), visibleEscapes(frame))
		require.True(t, containsStyledLine(frame, "draft", sequence), visibleEscapes(frame))
		require.Equal(t, "draft", fe.textInput.Value())
	}
}

func TestPromptBackgroundReplyBeforeShellAndFallback(t *testing.T) {
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI, termenv.Ascii} {
		fe := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(40, 10), termenv.ANSI)
		fe.promptColorProfile = profile
		fe.tui.Inject(uv.BackgroundColorEvent{Color: color.Black})
		fe.tui.Step()
		fe.setupTUI()
		fe.startShell(context.Background(), stubShellHandler{})
		require.Equal(t, blendPromptBackground(color.Black, profile).cell, fe.promptFrame.background)
		previous := fe.promptBackground
		fe.tui.Inject(uv.BackgroundColorEvent{})
		fe.tui.Step()
		require.Equal(t, previous, fe.promptBackground, "ignore malformed replies")
	}
	fe := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(40, 10), termenv.Ascii)
	fe.promptColorProfile = termenv.ANSI
	fe.tui.Inject(uv.CapabilityEvent{Content: "RGB"})
	fe.tui.Inject(uv.BackgroundColorEvent{Color: color.Black})
	fe.tui.Step()
	require.Equal(t, promptBackground{}, fe.promptBackground, "ASCII renderers must not enable shading")
	require.Equal(t, termenv.ANSI, fe.promptColorProfile)

	headless := newWithTerminalProfile(io.Discard, dagui.NewDB(), tuist.NewHeadlessTerminal(40, 10), termenv.ANSI)
	headless.tui.Inject(uv.CapabilityEvent{Content: "RGB"})
	headless.tui.Inject(uv.BackgroundColorEvent{Color: color.Black})
	headless.tui.Step()
	require.Equal(t, termenv.Ascii, headless.promptColorProfile)
	require.Equal(t, promptBackground{}, headless.promptBackground)
}
