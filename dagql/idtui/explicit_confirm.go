package idtui

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
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
}

var _ huh.Field = (*ExplicitConfirm)(nil)

func NewExplicitConfirm(affirmative, negative string, value *bool) *ExplicitConfirm {
	field := &ExplicitConfirm{
		confirm:     huh.NewConfirm().Value(value),
		value:       value,
		affirmative: affirmative,
		negative:    negative,
	}
	field.syncLabels()
	return field
}

func (field *ExplicitConfirm) Title(title string) *ExplicitConfirm {
	field.confirm.Title(title)
	return field
}

func (field *ExplicitConfirm) Description(description string) *ExplicitConfirm {
	field.confirm.Description(description)
	return field
}

func (field *ExplicitConfirm) Inline(inline bool) *ExplicitConfirm {
	field.confirm.Inline(inline)
	return field
}

func (field *ExplicitConfirm) syncLabels() {
	if field.value != nil && *field.value {
		field.confirm.Affirmative("▶ " + field.affirmative).Negative("  " + field.negative)
	} else {
		field.confirm.Affirmative("  " + field.affirmative).Negative("▶ " + field.negative)
	}
}

func (field *ExplicitConfirm) Init() tea.Cmd { return field.confirm.Init() }

func (field *ExplicitConfirm) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := field.confirm.Update(msg)
	field.confirm = model.(*huh.Confirm)
	field.syncLabels()
	return field, cmd
}

func (field *ExplicitConfirm) View() string {
	field.syncLabels()
	return field.confirm.View()
}

func (field *ExplicitConfirm) Blur() tea.Cmd  { return field.confirm.Blur() }
func (field *ExplicitConfirm) Focus() tea.Cmd { return field.confirm.Focus() }
func (field *ExplicitConfirm) Error() error   { return field.confirm.Error() }
func (field *ExplicitConfirm) Skip() bool     { return field.confirm.Skip() }
func (field *ExplicitConfirm) Zoom() bool     { return field.confirm.Zoom() }
func (field *ExplicitConfirm) KeyBinds() []key.Binding {
	return field.confirm.KeyBinds()
}
func (field *ExplicitConfirm) GetKey() string { return field.confirm.GetKey() }
func (field *ExplicitConfirm) GetValue() any  { return field.confirm.GetValue() }

func (field *ExplicitConfirm) Run() error {
	field.syncLabels()
	return field.confirm.Run()
}

func (field *ExplicitConfirm) RunAccessible(w io.Writer, r io.Reader) error {
	field.syncLabels()
	field.confirm.Title(fmt.Sprintf("%s? (No: %s)", field.affirmative, field.negative))
	return field.confirm.RunAccessible(w, r)
}

func (field *ExplicitConfirm) WithTheme(theme *huh.Theme) huh.Field {
	local := *theme
	local.Focused.FocusedButton = local.Focused.FocusedButton.UnsetBackground().Bold(true)
	local.Focused.BlurredButton = local.Focused.BlurredButton.UnsetBackground().Bold(false)
	local.Blurred.FocusedButton = local.Blurred.FocusedButton.UnsetBackground().Bold(false)
	local.Blurred.BlurredButton = local.Blurred.BlurredButton.UnsetBackground().Bold(false)
	field.confirm.WithTheme(&local)
	return field
}

func (field *ExplicitConfirm) WithAccessible(accessible bool) huh.Field {
	field.confirm.WithAccessible(accessible)
	return field
}

func (field *ExplicitConfirm) WithKeyMap(keymap *huh.KeyMap) huh.Field {
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
