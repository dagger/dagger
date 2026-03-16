package idtui

import "time"

// streamingCursor returns a block cursor character that pulses between
// a solid block and a thinner form based on wall-clock time. The pulse
// cycle is ~500ms, creating a gentle blink effect. The caller is
// responsible for styling it to match the surrounding content so it
// doesn't look like the real terminal cursor.
//
// The cursor uses fractional block characters to pulse:
//
//	phase 0: █ (full block)
//	phase 1: ▓ (dark shade)
//	phase 2: ▒ (medium shade)
//	phase 3: ░ (light shade)
//
// then back up, creating a smooth breathing effect.
func streamingCursor() string {
	// 8 phases over 800ms = 100ms per phase
	const phaseDuration = 100 * time.Millisecond
	const numPhases = 8

	phase := int(time.Now().UnixMilli()/phaseDuration.Milliseconds()) % numPhases

	// Bounce: 0 1 2 3 3 2 1 0
	blocks := [4]string{"█", "▓", "▒", "░"}
	if phase >= 4 {
		phase = 7 - phase
	}
	return blocks[phase]
}
