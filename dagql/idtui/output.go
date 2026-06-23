package idtui

import (
	"io"
	"os"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/muesli/termenv"
)

// NewOutput returns a termenv.Output that will always use color, regardless of
// whether w is a TTY, unless NO_COLOR is explicitly set.
//
// Progrock is opinionated here. Termenv disables colors by default if
// stdout is not a TTY or if the CI env var is set. We don't want that,
// because folks deserve colorful output in CI too.
//
// To disable colors, set NO_COLOR (https://no-color.org/).
func NewOutput(w io.Writer, opts ...termenv.OutputOption) *termenv.Output {
	return termenv.NewOutput(w, append([]termenv.OutputOption{
		termenv.WithProfile(ColorProfile()),
		termenv.WithTTY(true),
	}, opts...)...)
}

// ColorProfile returns Ascii if NO_COLOR (or similar) is set, or if we're being
// driven by an AI agent. Otherwise it returns termenv.ANSI, allowing colors to
// be used.
//
// Note that color profiles beyond simple ANSI are not used by Progrock. 16
// colors is all you need. Anything else disrespects the user's color scheme
// preferences.
func ColorProfile() termenv.Profile {
	if termenv.EnvNoColor() || RunningInAgent() {
		return termenv.Ascii
	} else {
		return termenv.ANSI
	}
}

// agentEnvVars are environment variables whose mere presence indicates the CLI
// is being driven by an AI coding agent rather than a human at a terminal. The
// value isn't parsed -- tools don't agree on its format, only on setting the
// variable. There's no single ratified standard yet, so we check the emerging
// generic conventions plus the tool-specific variables catalogued by Vercel's
// @vercel/detect-agent.
//
// Deliberately omitted: platform signals that are also set for ordinary human
// sessions (e.g. Replit's REPL_ID), and Devin's /opt/.devin marker, which is a
// file rather than an environment variable.
var agentEnvVars = []string{
	// Generic, cross-tool conventions.
	"AI_AGENT",        // Vercel detect-agent standard; set by Claude Code, Cursor, v0, Copilot, ...
	"AGENT",           // Goose, Amp
	"PI_CODING_AGENT", // pi

	// Tool-specific.
	"CLAUDECODE",           // Claude Code
	"CLAUDE_CODE",          // Claude Code
	"CURSOR_AGENT",         // Cursor CLI
	"CURSOR_TRACE_ID",      // Cursor
	"GEMINI_CLI",           // Gemini CLI
	"CODEX_SANDBOX",        // OpenAI Codex
	"CODEX_THREAD_ID",      // OpenAI Codex
	"ANTIGRAVITY_AGENT",    // Antigravity
	"AUGMENT_AGENT",        // Augment CLI
	"OPENCODE_CLIENT",      // OpenCode
	"COPILOT_MODEL",        // GitHub Copilot CLI
	"COPILOT_GITHUB_TOKEN", // GitHub Copilot CLI
	"COPILOT_ALLOW_ALL",    // GitHub Copilot CLI
}

// RunningInAgent reports whether the CLI is being driven by an AI coding agent
// (Claude Code, Cursor, Codex, Gemini, Copilot, pi, Goose, Amp, etc.) rather
// than a human at a terminal. Agents consume the output as text, so escape
// codes are just noise.
func RunningInAgent() bool {
	for _, name := range agentEnvVars {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

var (
	bgOnce    = &sync.Once{}
	hasDarkBG bool
)

type AdaptiveColor struct {
	Light termenv.Color
	Dark  termenv.Color
}

func (c AdaptiveColor) Sequence(bg bool) string {
	if HasDarkBackground() {
		return c.Dark.Sequence(bg)
	}
	return c.Light.Sequence(bg)
}

func HasDarkBackground() bool {
	bgOnce.Do(func() {
		if os.Getenv("FORCE_LIGHT_MODE") != "" ||
			os.Getenv("THEME_MODE") == "light" ||
			os.Getenv("LIGHT") != "" {
			hasDarkBG = false
		} else if os.Getenv("FORCE_DARK_MODE") != "" ||
			os.Getenv("THEME_MODE") == "dark" ||
			os.Getenv("DARK") != "" {
			hasDarkBG = true
		} else {
			hasDarkBG = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
		}
	})
	return hasDarkBG
}

func hl(st termenv.Style) termenv.Style {
	return st.Reverse()
}
