package main

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

func (m Details) headerView() string {
	title := m.item.Name()
	info := fmt.Sprintf("%3.f%%", m.item.ScrollPercent()*100)
	line := ""

	if !m.focus {
		title = titleStyle.Copy().
			Render(title)
		info = infoStyle.Copy().
			Render(info)
		space := max(0, m.width-lipgloss.Width(title)-lipgloss.Width(info))
		line = titleBarStyle.Copy().
			Render(strings.Repeat("─", space))
	} else {
		title = titleStyle.Copy().
			BorderForeground(colorSelected).
			Render(title)
		info = infoStyle.Copy().
			BorderForeground(colorSelected).
			Render(info)
		space := max(0, m.width-lipgloss.Width(title)-lipgloss.Width(info))
		line = titleBarStyle.Copy().
			BorderForeground(colorSelected).
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
		return strings.Repeat("\n", max(0, m.height))
	}
	headerView := m.headerView()

	m.item.SetHeight(m.height - lipgloss.Height(headerView))

	return lipgloss.JoinVertical(lipgloss.Left, headerView, m.item.View())
}
