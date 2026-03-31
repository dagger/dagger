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
			name:           "focused span exceeds viewport: never crop bottom",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      50,
			focusHeight:    30,
			wantEnd:        80, // focusEnd=80, must not be cropped
		},
		{
			name:           "focused span exceeds viewport at end of content",
			totalLines:     100,
			viewportHeight: 20,
			focusLine:      75,
			focusHeight:    25,
			wantEnd:        100, // focusEnd=100
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

// TestCropEndFocusAlwaysVisible verifies the key invariant: the focused
// span is never cropped, regardless of inputs.
func TestCropEndFocusAlwaysVisible(t *testing.T) {
	for totalLines := 1; totalLines <= 60; totalLines += 10 {
		for viewport := 1; viewport <= 40; viewport += 5 {
			for focusLine := 0; focusLine < totalLines; focusLine += 5 {
				for focusHeight := 1; focusHeight <= totalLines-focusLine; focusHeight += 5 {
					end := cropEnd(totalLines, viewport, focusLine, focusHeight)
					focusEnd := focusLine + focusHeight
					if focusEnd > totalLines {
						focusEnd = totalLines
					}
					if end < focusEnd {
						t.Errorf("cropEnd(%d, %d, %d, %d) = %d, but focusEnd = %d (focus cropped!)",
							totalLines, viewport, focusLine, focusHeight, end, focusEnd)
					}
					if end > totalLines {
						t.Errorf("cropEnd(%d, %d, %d, %d) = %d, exceeds totalLines",
							totalLines, viewport, focusLine, focusHeight, end)
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
