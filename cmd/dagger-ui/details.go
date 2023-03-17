package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tonistiigi/units"
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
	item     TreeEntry
	viewport viewport.Model
	spinner  spinner.Model
	height   int
	focus    bool
	tail     bool
}

func (m Details) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m Details) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Up):
			m.tail = false
			m.viewport.LineUp(1)
		case key.Matches(msg, keys.Down):
			m.tail = false
			m.viewport.LineDown(1)
		}
	}
	return m, nil
}

func (m *Details) SetItem(item TreeEntry) {
	if item == m.item {
		return
	}
	m.item = item
	m.tail = true
}

func (m *Details) SetWidth(width int) {
	m.viewport.Width = width
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
		line = strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(title)))
	} else {
		title = titleStyle.Copy().BorderForeground(lipgloss.Color("5")).Render(title)
		line = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render(strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(title))))
	}
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

func (m Details) footerView() string {
	info := fmt.Sprintf("%3.f%%", m.viewport.ScrollPercent()*100)
	line := ""

	if !m.focus {
		info = infoStyle.Render(info)
		line = strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(info)))
	} else {
		info = infoStyle.Copy().BorderForeground(lipgloss.Color("5")).Render(info)
		line = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render(strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(info))))
	}

	return lipgloss.JoinHorizontal(lipgloss.Center, line, info)
}

func (m Details) statusView() string {
	statuses := []string{}

	bar := progress.New(progress.WithScaledGradient("#A550DF", "#6124DF"))
	bar.Width = m.viewport.Width / 4

	for _, s := range m.item.Status() {
		status := completedStatus.String() + " "
		if s.Completed == nil {
			status = m.spinner.View() + " "
		}

		name := s.ID

		progress := ""
		if s.Total != 0 {
			progress = fmt.Sprintf("%.2f / %.2f", units.Bytes(s.Current), units.Bytes(s.Total))
			progress += " " + bar.ViewAs(float64(s.Current)/float64(s.Total))
		} else if s.Current != 0 {
			progress = fmt.Sprintf("%.2f", units.Bytes(s.Current))
		}

		pad := strings.Repeat(" ", m.viewport.Width-lipgloss.Width(status)-lipgloss.Width(name)-lipgloss.Width(progress))
		view := status + name + pad + progress
		statuses = append(statuses, view)
	}

	return strings.Join(statuses, "\n")
}

func (m Details) View() string {
	if m.item == nil {
		return strings.Repeat("\n", max(0, m.height))
	}
	headerView := m.headerView()
	footerView := m.footerView()
	m.viewport.Height = m.height - lipgloss.Height(headerView) - lipgloss.Height(footerView)

	if len(m.item.Status()) > 0 {
		m.viewport.SetContent(m.statusView())
	} else {
		m.viewport.SetContent(m.item.Logs().String())
	}
	if m.tail {
		m.viewport.GotoBottom()
	}

	return fmt.Sprintf("%s\n%s\n%s", headerView, m.viewport.View(), footerView)
}
