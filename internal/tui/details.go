package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Details struct {
	item   TreeEntry
	width  int
	height int
	focus  bool
}

func (m Details) Init() tea.Cmd {
	return nil
}

func (m Details) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.item == nil {
		return m, nil
	}

	itemM, cmd := m.item.Update(msg)
	m.item = itemM.(TreeEntry)
	return m, cmd
}

func (m *Details) SetItem(item TreeEntry) tea.Cmd {
	if item == m.item {
		return nil
	}
	m.item = item
	return m.item.Init()
}

func (m *Details) SetWidth(width int) {
	m.width = width
	if m.item != nil {
		m.item.SetWidth(width)
	}
}

func (m *Details) SetHeight(height int) {
	m.height = height
}

func (m *Details) Focus(focus bool) {
	m.focus = focus
}

func (m *Details) Open() tea.Cmd {
	return m.item.Open()
}

func (m Details) headerView() string {
	title := trunc(m.item.Name(), m.width)
	info := fmt.Sprintf("%3.f%%", m.item.ScrollPercent()*100)
	line := ""
	borderWidth := lipgloss.Width(titleStyle.Render(""))

	if !m.focus {
		info = infoStyle.Copy().Render(info)
		title = trunc(title, m.width-lipgloss.Width(info)-borderWidth)
		title = titleStyle.Copy().Render(title)
		space := max(0, m.width-lipgloss.Width(title)-lipgloss.Width(info))
		line = titleBarStyle.Copy().
			Render(strings.Repeat("─", space))
	} else {
		info = infoStyle.Copy().BorderForeground(colorSelected).Render(info)
		title = trunc(title, m.width-lipgloss.Width(info)-borderWidth)
		title = titleStyle.Copy().BorderForeground(colorSelected).Render(title)
		space := max(0, m.width-lipgloss.Width(title)-lipgloss.Width(info))
		line = titleBarStyle.Copy().
			Foreground(colorSelected).
			Render(strings.Repeat("─", space))
	}

	return lipgloss.JoinHorizontal(lipgloss.Center,
		title,
		line,
		info)
}

func (m Details) View() string {
	if m.item == nil {
		return strings.Repeat("\n", max(0, m.height-1))
	}
	headerView := m.headerView()

	m.item.SetHeight(m.height - lipgloss.Height(headerView))

	return fmt.Sprintf("%s\n%s", headerView, m.item.View())
}
