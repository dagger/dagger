package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	bkclient "github.com/moby/buildkit/client"
)

func New(quit func(), ch chan *bkclient.SolveStatus) *Model {
	m := &Model{
		quit: quit,
		tree: &Tree{
			viewport:  viewport.New(0, 10),
			spinner:   newSpinner(),
			collapsed: make(map[TreeEntry]bool),
			focus:     true,
		},
		root:      NewGroup("root", ""),
		itemsByID: make(map[string]*Item),
		details:   Details{},
		follow:    true,
		ch:        ch,
		help:      help.New(),
	}

	return m
}

type Model struct {
	quit func()

	ch        chan *bkclient.SolveStatus
	itemsByID map[string]*Item
	root      *Group

	tree    *Tree
	details Details
	help    help.Model

	width  int
	height int

	localTimeDiff time.Duration
	done          bool

	follow       bool
	detailsFocus bool
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.tree.Init(),
		m.details.Init(),
		m.waitForActivity(),
	)
}

func (m Model) adjustLocalTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	adjusted := t.Add(m.localTimeDiff)
	return &adjusted
}

type followMsg string

func debounceFollow(id string) tea.Cmd {
	return tea.Tick(300*time.Millisecond, func(_ time.Time) tea.Msg {
		return followMsg(id)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tree.SetWidth(msg.Width)
		m.details.SetWidth(msg.Width)
	case tea.KeyMsg:
		return m.processKeyMsg(msg)
	case followMsg:
		if !m.follow {
			return m, nil
		}
		item := m.itemsByID[string(msg)]
		if item == nil {
			return m, nil
		}

		if item.Completed() == nil && len(item.Entries()) == 0 {
			// Not completed -- try again
			return m, debounceFollow(string(msg))
		}

		m.tree.Follow()
		cmd := m.details.SetItem(m.tree.Current())

		if item == m.tree.Current() {
			// There was no "next" item (maybe everything is pending)
			// Try again
			return m, tea.Batch(cmd, debounceFollow(string(msg)))
		}
		return m, cmd
	case *bkclient.SolveStatus:
		return m.processSolveStatus(msg)
	case spinner.TickMsg:
		cmds := []tea.Cmd{}

		updatedDetails, cmd := m.details.Update(msg)
		m.details = updatedDetails.(Details)
		cmds = append(cmds, cmd)

		updatedTree, cmd := m.tree.Update(msg)
		tree := updatedTree.(Tree)
		m.tree = &tree
		cmds = append(cmds, cmd)

		return m, tea.Batch(cmds...)
	}

	return m, nil
}

func (m Model) processKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Help):
		m.help.ShowAll = !m.help.ShowAll
	case key.Matches(msg, keys.Quit):
		m.quit()
		return m, tea.Quit
	case key.Matches(msg, keys.Follow):
		m.follow = !m.follow
		m.tree.Follow()
	case key.Matches(msg, keys.Up):
		if m.detailsFocus {
			newDetails, cmd := m.details.Update(msg)
			m.details = newDetails.(Details)
			return m, cmd
		}
		m.follow = false
		m.tree.MoveUp()
		if m.tree.Current() != nil {
			return m, m.details.SetItem(m.tree.Current())
		}
	case key.Matches(msg, keys.Down):
		if m.detailsFocus {
			newDetails, cmd := m.details.Update(msg)
			m.details = newDetails.(Details)
			return m, cmd
		}
		m.follow = false
		m.tree.MoveDown()
		if m.tree.Current() != nil {
			return m, m.details.SetItem(m.tree.Current())
		}
	case key.Matches(msg, keys.Collapse):
		m.tree.Collapse(m.tree.Current(), false)
	case key.Matches(msg, keys.Expand):
		m.tree.Expand(m.tree.Current(), false)
	case key.Matches(msg, keys.CollapseAll):
		m.tree.Collapse(m.root, true)
	case key.Matches(msg, keys.ExpandAll):
		m.tree.Expand(m.root, true)
	case key.Matches(msg, keys.Switch):
		m.detailsFocus = !m.detailsFocus
		m.tree.Focus(!m.detailsFocus)
		m.details.Focus(m.detailsFocus)
	}
	return m, nil
}

func (m Model) processSolveStatus(msg *bkclient.SolveStatus) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{
		m.waitForActivity(),
	}

	// We've reached the end
	if msg == nil {
		m.done = true
		if m.follow {
			// automatically quit on completion in follow mode
			return m, tea.Quit
		}
		return m, nil
	}

	// var currentCompleted bool
	for _, v := range msg.Vertexes {
		if m.localTimeDiff == 0 && v.Started != nil {
			m.localTimeDiff = time.Since(*v.Started)
		}
		v.Started = m.adjustLocalTime(v.Started)
		v.Completed = m.adjustLocalTime(v.Completed)

		item := m.itemsByID[v.Digest.String()]
		if item == nil {
			item = NewItem(v, m.width)
			cmds = append(cmds, item.Init())
			m.itemsByID[item.id] = item
			if !item.Internal() {
				m.root.Add(item.group, item)
				m.tree.SetRoot(m.root)
				cmds = append(cmds, m.details.SetItem(m.tree.Current()))
			}
		}

		item.UpdateVertex(v)
		if item == m.tree.Current() && item.completed != nil {
			// currentCompleted = true
		}
	}
	for _, s := range msg.Statuses {
		item := m.itemsByID[s.Vertex.String()]
		if item == nil {
			continue
		}
		item.UpdateStatus(s)
	}
	for _, l := range msg.Logs {
		item := m.itemsByID[l.Vertex.String()]
		if item == nil {
			continue
		}
		item.UpdateLog(l)
	}

	// if currentCompleted && m.follow {
	// 	// Ideally we could go to the next item directly,
	// 	// however buildkit might decide later on this vertex was not completed.
	// 	// So we need to debounce -- wait 100ms, if it's still finished, then
	// 	// move to the next one.
	cmds = append(cmds, debounceFollow(m.tree.Current().ID()))
	// }

	return m, tea.Batch(cmds...)
}

func (m Model) statusBarTimerView() string {
	if m.root.Started() == nil {
		return "0.0s"
	}
	current := time.Now()
	if m.done && m.root.Completed() != nil {
		current = *m.root.Completed()
	}
	diff := current.Sub(*m.root.Started())

	prec := 1
	sec := diff.Seconds()
	if sec < 10 {
		prec = 2
	} else if sec < 100 {
		prec = 1
	}
	return fmt.Sprintf("%.[2]*[1]fs", sec, prec)
}

var (
	// Status Bar.
	statusNugget = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7")).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "8", Dark: "15"}).
			Background(lipgloss.AdaptiveColor{Light: "15", Dark: "8"})

	followMode = lipgloss.NewStyle().
			Inherit(statusBarStyle).
			Background(lipgloss.Color("13")).
			Foreground(lipgloss.Color("0")).
			Padding(0, 1).
			MarginRight(1).
			SetString("FOLLOW")

	browseMode = followMode.Copy().
			Background(lipgloss.Color("5")).
			Foreground(lipgloss.Color("0")).
			SetString("BROWSE")

	runningStatus = statusNugget.Copy().
			Background(lipgloss.Color("3")).
			Foreground(lipgloss.Color("0")).
			Align(lipgloss.Right).
			SetString("RUNNING")

	completeStatus = runningStatus.Copy().
			Background(lipgloss.Color("6")).
			Foreground(lipgloss.Color("0")).
			Align(lipgloss.Right).
			SetString("COMPLETE")

	statusText = lipgloss.NewStyle().Inherit(statusBarStyle)

	timerStyle = statusNugget.Copy().
			Background(lipgloss.Color("3")).
			Foreground(lipgloss.Color("0"))
)

func (m Model) statusBarView() string {
	mode := browseMode.String()
	if m.follow {
		mode = followMode.String()
	}
	status := runningStatus.String()
	if m.done {
		status = completeStatus.String()
	}

	timer := timerStyle.Render("⌛ " + m.statusBarTimerView())
	statusVal := statusText.Copy().
		Width(m.width - lipgloss.Width(mode) - lipgloss.Width(status) - lipgloss.Width(timer)).
		Render("")

	return lipgloss.JoinHorizontal(lipgloss.Top,
		mode,
		statusVal,
		status,
		timer,
	)
}

func (m Model) helpView() string {
	return m.help.View(keys)
}

func (m Model) View() string {
	treeHeight := m.height / 2
	detailsHeight := m.height / 2
	// Add leftover space to tree
	treeHeight += m.height - (treeHeight + detailsHeight)
	m.tree.SetHeight(treeHeight)

	treeView := m.tree.View()
	treeView += strings.Repeat("\n", max(0, treeHeight-lipgloss.Height(treeView)))

	helpView := m.helpView()
	detailsHeight -= lipgloss.Height(helpView)

	statusBarView := "\n" + m.statusBarView()
	detailsHeight -= lipgloss.Height(statusBarView)

	m.details.SetHeight(detailsHeight)
	detailsView := m.details.View()

	return lipgloss.JoinVertical(lipgloss.Left,
		treeView,
		detailsView,
		statusBarView,
		helpView,
	)
}

func (m Model) waitForActivity() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.ch
		if ok {
			return msg
		}

		return nil
	}
}

func newSpinner() spinner.Model {
	return spinner.New(
		spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("3"))),
		spinner.WithSpinner(spinner.MiniDot),
	)
}
