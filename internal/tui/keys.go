package tui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up   key.Binding
	Down key.Binding

	Home, End        key.Binding
	PageUp, PageDown key.Binding

	Switch key.Binding

	Collapse    key.Binding
	Expand      key.Binding
	CollapseAll key.Binding
	ExpandAll   key.Binding

	Follow key.Binding

	Open key.Binding

	Help key.Binding
	Quit key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{
		k.Up, k.Down,
		k.Collapse, k.Expand,
		k.Open, k.Switch, k.Follow,
		k.Help, k.Quit,
	}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Home, k.End, k.PageUp, k.PageDown},
		{k.Collapse, k.CollapseAll, k.Expand, k.ExpandAll},
		{k.Open, k.Switch, k.Follow, k.Help, k.Quit},
	}
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "move up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "move down"),
	),
	Home: key.NewBinding(
		key.WithKeys("home"),
		key.WithHelp("home", "go to top"),
	),
	End: key.NewBinding(
		key.WithKeys("end"),
		key.WithHelp("end", "go to bottom"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("pgdown"),
		key.WithHelp("pgdn", "page down"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("pgup"),
		key.WithHelp("pgup", "page up"),
	),
	Switch: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch focus"),
	),
	Collapse: key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←", "collapse"),
	),
	Expand: key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("→", "expand"),
	),
	CollapseAll: key.NewBinding(
		key.WithKeys("["),
		key.WithHelp("[", "collapse all"),
	),
	ExpandAll: key.NewBinding(
		key.WithKeys("]"),
		key.WithHelp("]", "expand all"),
	),
	Follow: key.NewBinding(
		key.WithKeys("f"),
		key.WithHelp("f", "toggle follow"),
	),
	Open: key.NewBinding(
		key.WithKeys("o"),
		key.WithHelp("o", "open logs in $EDITOR"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "esc", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}
