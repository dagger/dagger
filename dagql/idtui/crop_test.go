package idtui

import (
	"testing"
)

func TestCropEnd(t *testing.T) {
	tests := []struct {
		name           string
		totalLines     int
		viewportHeight int
		focusLine      int // -1 means no focus
		focusHeight    int // height of focused span
		wantEnd        int
	}{
		{
			name:           "focus at line 0 single line",
			totalLines:     100,
			viewportHeight: 40,
			focusLine:      0,
			focusHeight:    1,
			wantEnd:        40, // fill viewport from the top
		},
		{
			name:           "fits in viewport: no cropping",
			totalLines:     30,
			viewportHeight: 40,
			focusLine:      10,
			focusHeight:    5,
			wantEnd:        30,
		},
		{
			name:           "focused near bottom: crop with context below",
			totalLines:     100,
			viewportHeight: 40,
			focusLine:      80,
			focusHeight:    5,
			wantEnd:        100, // focusEnd(85) + below(17) = 102, capped to 100
		},
		{
			name:           "focused in middle: context above and below",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      50,
			focusHeight:    4,
			wantEnd:        62, // focusEnd(54) + below(8) = 62
		},
		{
			name:           "focused span equals viewport",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      50,
			focusHeight:    20,
			wantEnd:        70, // focusEnd=70, remaining=0, below=0, end=70
		},
		{
			name:           "focused span exceeds viewport: anchor top, crop tail",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      50,
			focusHeight:    30,
			wantEnd:        70, // focusLine+viewport=70: header stays, tail cropped
		},
		{
			name:           "focused span exceeds viewport at end of content",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      75,
			focusHeight:    25,
			wantEnd:        95, // focusLine+viewport=95: header stays, tail cropped
		},
		{
			name:           "focused at very top: fill below",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      0,
			focusHeight:    3,
			wantEnd:        20, // focusEnd(3) + below(8) = 11; but focusLine+viewport=20, and we want to fill
		},
		{
			name:           "single line focus in middle",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      50,
			focusHeight:    1,
			wantEnd:        60, // focusEnd(51) + below(9) = 60
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cropEnd(tt.totalLines, tt.viewportHeight, tt.focusLine, tt.focusHeight)
			if got != tt.wantEnd {
				t.Errorf("cropEnd(%d, %d, %d, %d) = %d, want %d",
					tt.totalLines, tt.viewportHeight, tt.focusLine, tt.focusHeight,
					got, tt.wantEnd)
			}
		})
	}
}

// TestCropEndFocusHeaderAlwaysVisible verifies the key invariant: the focused
// span's header (its top line, focusLine) is always within the visible window
// [end-viewport, end). When the focus fits in the viewport its whole content
// stays visible; when it is taller than the viewport its tail is cropped (its
// top anchored) rather than its header scrolling offscreen.
func TestCropEndFocusHeaderAlwaysVisible(t *testing.T) {
	for totalLines := 1; totalLines <= 60; totalLines += 10 {
		for viewport := 1; viewport <= 40; viewport += 5 {
			for focusLine := 0; focusLine < totalLines; focusLine += 5 {
				for focusHeight := 1; focusHeight <= totalLines-focusLine; focusHeight += 5 {
					end := cropEnd(totalLines, viewport, focusLine, focusHeight)
					if end > totalLines {
						t.Errorf("cropEnd(%d, %d, %d, %d) = %d, exceeds totalLines",
							totalLines, viewport, focusLine, focusHeight, end)
					}
					// The header must be visible in the window [visibleStart, end).
					visibleStart := max(0, end-viewport)
					if focusLine < visibleStart || focusLine >= end {
						t.Errorf("cropEnd(%d, %d, %d, %d) = %d: header line %d not visible (window [%d, %d))",
							totalLines, viewport, focusLine, focusHeight, end, focusLine, visibleStart, end)
					}
					// A focus that fits must not be cropped at all.
					focusEnd := min(focusLine+focusHeight, totalLines)
					if focusHeight <= viewport && end < focusEnd {
						t.Errorf("cropEnd(%d, %d, %d, %d) = %d crops a focus that fits (focusEnd = %d)",
							totalLines, viewport, focusLine, focusHeight, end, focusEnd)
					}
				}
			}
		}
	}
}

// TestCropEndNoFocusFocusStartVisible checks that when the focused span
// fits in the viewport, its start line is visible (within [end-viewport, end)).
func TestCropEndNoFocusFocusStartVisible(t *testing.T) {
	for totalLines := 1; totalLines <= 60; totalLines += 10 {
		for viewport := 1; viewport <= 40; viewport += 5 {
			for focusLine := 0; focusLine < totalLines; focusLine += 5 {
				for focusHeight := 1; focusHeight <= min(viewport, totalLines-focusLine); focusHeight += 5 {
					end := cropEnd(totalLines, viewport, focusLine, focusHeight)
					visibleStart := end - viewport
					if visibleStart < 0 {
						visibleStart = 0
					}
					if focusLine < visibleStart {
						t.Errorf("cropEnd(%d, %d, %d, %d) = %d, focusLine %d not visible (visible starts at %d)",
							totalLines, viewport, focusLine, focusHeight, end, focusLine, visibleStart)
					}
				}
			}
		}
	}
}
