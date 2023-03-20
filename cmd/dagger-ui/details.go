package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = func() lipgloss.Style {
		b := lipgloss.RoundedBorder()
		b.Right = "├"
		return lipgloss.NewStyle().Bold(true).BorderStyle(b).Padding(0, 1)
	}()

	infoStyle = func() lipgloss.Style {
		b := lipgloss.RoundedBorder()
		b.Left = "┤"
		return titleStyle.Copy().BorderStyle(b)
	}()
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
	line := ""

	if !m.focus {
		title = titleStyle.Render(title)
		line = strings.Repeat("─", max(0, m.width-lipgloss.Width(title)))
	} else {
		title = titleStyle.Copy().BorderForeground(lipgloss.Color("5")).Render(title)
		line = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render(strings.Repeat("─", max(0, m.width-lipgloss.Width(title))))
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

func (m Details) footerView() string {
	info := fmt.Sprintf("%3.f%%", m.item.ScrollPercent()*100)
	line := ""

	if !m.focus {
		info = infoStyle.Render(info)
		line = strings.Repeat("─", max(0, m.width-lipgloss.Width(info)))
	} else {
		info = infoStyle.Copy().BorderForeground(lipgloss.Color("5")).Render(info)
		line = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render(strings.Repeat("─", max(0, m.width-lipgloss.Width(info))))
	}

	return lipgloss.JoinHorizontal(lipgloss.Center, line, info)
}

func (m Details) View() string {
	if m.item == nil {
		return strings.Repeat("\n", max(0, m.height))
	}
	headerView := m.headerView()
	footerView := m.footerView()

	m.item.SetHeight(m.height - lipgloss.Height(headerView) - lipgloss.Height(footerView))

	return lipgloss.JoinVertical(lipgloss.Left, headerView, m.item.View(), footerView)
}
