package idtui

import (
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// ExplicitChoice renders a select as a row of plainly labeled choices with a
// textual focus marker. It is the multi-option counterpart to ExplicitConfirm.
type ExplicitChoice[T comparable] struct {
	selectField *huh.Select[T]
	options     []huh.Option[T]
	value       *T
	focused     bool
	title       string
	titleLink   string
	description string
	theme       *huh.Theme
	keymap      huh.SelectKeyMap
}

var _ huh.Field = (*ExplicitChoice[string])(nil)

func NewExplicitChoice[T comparable](value *T, options ...huh.Option[T]) *ExplicitChoice[T] {
	field := &ExplicitChoice[T]{
		options: options,
		value:   value,
		keymap:  huh.NewDefaultKeyMap().Select,
	}
	field.selectField = huh.NewSelect[T]().Options(options...).Value(value).Inline(true)
	return field
}

func (field *ExplicitChoice[T]) Title(title string) *ExplicitChoice[T] {
	field.title = title
	field.selectField.Title(title)
	return field
}

func (field *ExplicitChoice[T]) TitleLink(url string) *ExplicitChoice[T] {
	field.titleLink = url
	return field
}

func (field *ExplicitChoice[T]) Description(description string) *ExplicitChoice[T] {
	field.description = description
	field.selectField.Description(description)
	return field
}

func (field *ExplicitChoice[T]) Init() tea.Cmd { return field.selectField.Init() }

func (field *ExplicitChoice[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := field.selectField.Update(msg)
	field.selectField = model.(*huh.Select[T])
	return field, cmd
}

func (field *ExplicitChoice[T]) View() string {
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
		view.WriteByte('\n')
		view.WriteString(styles.Description.Render(field.description))
	}
	if view.Len() > 0 {
		view.WriteString("\n\n")
	}

	for i, option := range field.options {
		if i > 0 {
			view.WriteString("     ")
		}
		selected := field.value != nil && option.Value == *field.value
		marker := "  "
		style := styles.BlurredButton
		if field.focused && selected {
			marker = "▶ "
			style = styles.FocusedButton
		}
		view.WriteString(marker + style.Render(option.Key))
	}
	return styles.Base.Render(view.String())
}

func (field *ExplicitChoice[T]) Blur() tea.Cmd {
	field.focused = false
	return field.selectField.Blur()
}

func (field *ExplicitChoice[T]) Focus() tea.Cmd {
	field.focused = true
	return field.selectField.Focus()
}

func (field *ExplicitChoice[T]) Error() error { return field.selectField.Error() }
func (field *ExplicitChoice[T]) Skip() bool   { return field.selectField.Skip() }
func (field *ExplicitChoice[T]) Zoom() bool   { return field.selectField.Zoom() }
func (field *ExplicitChoice[T]) KeyBinds() []key.Binding {
	choose := key.NewBinding(
		key.WithKeys("left", "right", "h", "l"),
		key.WithHelp("←/→", "choose"),
	)
	return []key.Binding{choose, field.keymap.Submit}
}
func (field *ExplicitChoice[T]) GetKey() string { return field.selectField.GetKey() }
func (field *ExplicitChoice[T]) GetValue() any  { return field.selectField.GetValue() }
func (field *ExplicitChoice[T]) Run() error     { return field.selectField.Run() }
func (field *ExplicitChoice[T]) RunAccessible(w io.Writer, r io.Reader) error {
	return field.selectField.RunAccessible(w, r)
}

func (field *ExplicitChoice[T]) WithTheme(theme *huh.Theme) huh.Field {
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
	field.selectField.WithTheme(&local)
	return field
}

func (field *ExplicitChoice[T]) WithAccessible(accessible bool) huh.Field {
	// Huh calls RunAccessible directly; its old WithAccessible toggle is
	// deprecated and no longer needs to be forwarded.
	return field
}

func (field *ExplicitChoice[T]) WithKeyMap(keymap *huh.KeyMap) huh.Field {
	local := *keymap
	local.Select.Left.SetEnabled(true)
	local.Select.Right.SetEnabled(true)
	local.Select.Up.SetEnabled(false)
	local.Select.Down.SetEnabled(false)
	field.keymap = local.Select
	field.selectField.WithKeyMap(&local)
	return field
}

func (field *ExplicitChoice[T]) WithWidth(width int) huh.Field {
	field.selectField.WithWidth(width)
	return field
}

func (field *ExplicitChoice[T]) WithHeight(height int) huh.Field {
	field.selectField.WithHeight(height)
	return field
}

func (field *ExplicitChoice[T]) WithPosition(position huh.FieldPosition) huh.Field {
	field.selectField.WithPosition(position)
	return field
}
