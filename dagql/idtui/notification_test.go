package idtui

import (
	"io"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/dagger/dagger/dagql/dagui"
	"github.com/muesli/termenv"
	"github.com/vito/tuist"
)

// TestNotificationTopBorderWidth guards against a width miscalculation in the
// notification bubble's top border. When the title + keymap nearly fill the
// box, the fill-bar math used to emit one bar too many, so the assembled top
// border was wider than the box. The overlay compositor then truncated the
// overflow, dropping the closing "╮" corner and making the box top look
// shifted/broken (visible at common 80/100/120-column widths). The top border
// must always be exactly the box width and keep both corners.
func TestNotificationTopBorderWidth(t *testing.T) {
	term := tuist.NewHeadlessTerminal(120, 20)
	fe := newWithTerminal(io.Discard, dagui.NewDB(), term)
	fe.profile = termenv.ANSI

	n := newNotificationBubble(fe, SidebarSection{
		Title: "Changes",
		KeyMap: []key.Binding{
			key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "save")),
			key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "reset")),
		},
	})

	// notificationWidth ranges roughly 30..50; exercise inner widths that span
	// "keymap overflows", "keymap exactly fills", and "keymap fits with room".
	for innerWidth := 20; innerWidth <= 60; innerWidth++ {
		top := n.buildTopBorder(fe.profile, termenv.ANSIBrightBlack, innerWidth)

		if w := tuist.VisibleWidth(top); w != innerWidth+2 {
			t.Errorf("innerWidth=%d: top border visible width = %d, want %d: %q",
				innerWidth, w, innerWidth+2, stripANSICodes(top))
		}

		plain := []rune(stripANSICodes(top))
		if len(plain) == 0 || plain[0] != []rune(CornerTopLeft)[0] {
			t.Errorf("innerWidth=%d: top border missing left corner: %q", innerWidth, string(plain))
		}
		if len(plain) == 0 || plain[len(plain)-1] != []rune(CornerTopRight)[0] {
			t.Errorf("innerWidth=%d: top border missing right corner: %q", innerWidth, string(plain))
		}
	}
}
