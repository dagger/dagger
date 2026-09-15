package idtui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ExplicitConfirm wraps huh.Confirm with a textual focus marker. Huh's stock
// confirm communicates the current choice through color inversion alone,
// which is ambiguous in low-color terminals and absent from copied or spoken
// output.
type ExplicitConfirm struct {
	confirm     *huh.Confirm
	value       *bool
	affirmative string
	negative    string
	focused     bool
	inline      bool
	title       string
	titleLink   string
	description string
	theme       *huh.Theme
	keymap      huh.ConfirmKeyMap
	previous    key.Binding
}

var _ huh.Field = (*ExplicitConfirm)(nil)

func NewExplicitConfirm(affirmative, negative string, value *bool) *ExplicitConfirm {
	field := &ExplicitConfirm{
		confirm:     huh.NewConfirm().Value(value),
		value:       value,
		affirmative: affirmative,
		negative:    negative,
		keymap:      huh.NewDefaultKeyMap().Confirm,
		previous: key.NewBinding(
			key.WithKeys("up", "k", "ctrl+p"),
		),
	}
	field.confirm.Affirmative(affirmative).Negative(negative)
	return field
}

func (field *ExplicitConfirm) Title(title string) *ExplicitConfirm {
	field.title = title
	field.confirm.Title(title)
	return field
}

func (field *ExplicitConfirm) TitleLink(url string) *ExplicitConfirm {
	field.titleLink = url
	return field
}

func (field *ExplicitConfirm) Description(description string) *ExplicitConfirm {
	field.description = description
	field.confirm.Description(description)
	return field
}

func (field *ExplicitConfirm) Inline(inline bool) *ExplicitConfirm {
	field.inline = inline
	field.confirm.Inline(inline)
	return field
}

func (field *ExplicitConfirm) Init() tea.Cmd { return field.confirm.Init() }

func (field *ExplicitConfirm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && key.Matches(keyMsg, field.previous) {
		return field, huh.PrevField
	}
	model, cmd := field.confirm.Update(msg)
	field.confirm = model.(*huh.Confirm)
	return field, cmd
}

func (field *ExplicitConfirm) View() string {
	theme := field.theme
	if theme == nil {
		theme = huh.ThemeBase16()
	}
	styles := &theme.Blurred
	if field.focused {
		styles = &theme.Focused
	}

	var view strings.Builder
	if field.title != "" {
		title := explicitConfirmTitleStyle(styles).Render(field.title)
		if field.titleLink != "" {
			title = ansi.SetHyperlink(field.titleLink) + title + ansi.ResetHyperlink()
		}
		view.WriteString(title)
	}
	if field.description != "" {
		if !field.inline {
			view.WriteByte('\n')
		}
		view.WriteString(styles.Description.Render(field.description))
	}
	if !field.inline && view.Len() > 0 {
		view.WriteString("\n\n")
	}

	selected := field.value != nil && *field.value
	view.WriteString(field.renderChoice(styles, field.affirmative, selected))
	view.WriteString("     ")
	view.WriteString(field.renderChoice(styles, field.negative, !selected))
	return styles.Base.Render(view.String())
}

func explicitConfirmTitleStyle(styles *huh.FieldStyles) lipgloss.Style {
	return styles.Title.
		Foreground(lipgloss.Color("8")).
		Bold(true).
		Italic(true)
}

func (field *ExplicitConfirm) renderChoice(styles *huh.FieldStyles, label string, selected bool) string {
	marker := "  "
	style := styles.BlurredButton
	if field.focused && selected {
		marker = "▶ "
		style = styles.FocusedButton
	}
	return marker + style.Render(label)
}

func (field *ExplicitConfirm) Blur() tea.Cmd {
	field.focused = false
	return field.confirm.Blur()
}

func (field *ExplicitConfirm) Focus() tea.Cmd {
	field.focused = true
	return field.confirm.Focus()
}
func (field *ExplicitConfirm) Error() error { return field.confirm.Error() }
func (field *ExplicitConfirm) Skip() bool   { return field.confirm.Skip() }
func (field *ExplicitConfirm) Zoom() bool   { return field.confirm.Zoom() }
func (field *ExplicitConfirm) KeyBinds() []key.Binding {
	accept := field.keymap.Accept
	accept.SetHelp("y", field.affirmative)
	reject := field.keymap.Reject
	reject.SetHelp("n", field.negative)
	return []key.Binding{
		field.keymap.Toggle,
		field.keymap.Submit,
		accept,
		reject,
	}
}
func (field *ExplicitConfirm) GetKey() string { return field.confirm.GetKey() }
func (field *ExplicitConfirm) GetValue() any  { return field.confirm.GetValue() }

func (field *ExplicitConfirm) Run() error {
	return field.confirm.Run()
}

func (field *ExplicitConfirm) RunAccessible(w io.Writer, r io.Reader) error {
	field.confirm.Title(fmt.Sprintf("%s? (No: %s)", field.affirmative, field.negative))
	return field.confirm.RunAccessible(w, r)
}

func (field *ExplicitConfirm) WithTheme(theme *huh.Theme) huh.Field {
	local := *theme
	button := local.Focused.FocusedButton.
		UnsetBackground().
		Foreground(lipgloss.Color("8")).
		Padding(0).
		MarginRight(0).
		Bold(false).
		Faint(false)
	local.Focused.FocusedButton = button.Bold(true)
	local.Focused.BlurredButton = button
	local.Blurred.FocusedButton = button
	local.Blurred.BlurredButton = button
	field.theme = &local
	field.confirm.WithTheme(&local)
	return field
}

func (field *ExplicitConfirm) WithAccessible(accessible bool) huh.Field {
	// Huh calls RunAccessible directly; its old WithAccessible toggle is
	// deprecated and no longer needs to be forwarded.
	return field
}

func (field *ExplicitConfirm) WithKeyMap(keymap *huh.KeyMap) huh.Field {
	field.keymap = keymap.Confirm
	field.confirm.WithKeyMap(keymap)
	return field
}

func (field *ExplicitConfirm) WithWidth(width int) huh.Field {
	field.confirm.WithWidth(width)
	return field
}

func (field *ExplicitConfirm) WithHeight(height int) huh.Field {
	field.confirm.WithHeight(height)
	return field
}

func (field *ExplicitConfirm) WithPosition(position huh.FieldPosition) huh.Field {
	field.confirm.WithPosition(position)
	return field
}
