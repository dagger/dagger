package tui

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dagger/dagger/internal/engine/journal"
	bkclient "github.com/moby/buildkit/client"
)

func New(quit func(), r journal.Reader, rootName string, rootLogs groupModel) *Model {
	root := NewGroup("", rootName, rootLogs)
	m := &Model{
		quit: quit,
		tree: &Tree{
			viewport:  viewport.New(80, 1),
			spinner:   newSpinner(),
			collapsed: make(map[TreeEntry]bool),
			focus:     true,
		},
		root:      root,
		itemsByID: make(map[string]*Item),
		details:   Details{},
		follow:    true,
		journal:   r,
		help:      help.New(),
	}

	m.tree.SetRoot(m.root)

	return m
}

type Model struct {
	quit func()

	journal   journal.Reader
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
		followTick(),
	)
}

type endMsg struct{}

func (m Model) adjustLocalTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	adjusted := t.Add(m.localTimeDiff)
	return &adjusted
}

type followMsg struct{}

func followTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
		return followMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tree.SetWidth(msg.Width)
		m.details.SetWidth(msg.Width)
		m.root.SetWidth(msg.Width)
	case tea.KeyMsg:
		return m.processKeyMsg(msg)
	case followMsg:
		if !m.follow {
			return m, nil
		}

		m.tree.Follow()

		return m, tea.Batch(
			m.details.SetItem(m.tree.Current()),
			followTick(),
		)
	case *journal.Entry:
		return m.processSolveStatus(msg.Event)
	case spinner.TickMsg:
		cmds := []tea.Cmd{}

		updatedDetails, cmd := m.details.Update(msg)
		m.details = updatedDetails.(Details)
		cmds = append(cmds, cmd)

		updatedTree, cmd := m.tree.Update(msg)
		tree := updatedTree.(*Tree)
		m.tree = tree
		cmds = append(cmds, cmd)

		return m, tea.Batch(cmds...)
	case endMsg:
		// We've reached the end
		m.done = true
		// TODO(vito): print summary before exiting
		// if m.follow {
		// 	// automatically quit on completion in follow mode
		// 	return m, tea.Quit
		// }
		return m, nil

	default:
		log.Printf("unhandled message: %T (%v)", msg, msg)
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
		return m, func() tea.Msg {
			return followMsg{}
		}
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
	return strings.TrimSpace(fmt.Sprintf("%.[2]*[1]fs", sec, prec))
}

func (m Model) View() string {
	maxTreeHeight := m.height / 2
	// hack: make the details header split the view evenly
	// maxTreeHeight = max(0, maxTreeHeight-2)
	treeHeight := min(maxTreeHeight, m.tree.UsedHeight())
	m.tree.SetHeight(treeHeight)

	helpView := m.helpView()
	statusBarView := m.statusBarView()

	detailsHeight := m.height - treeHeight
	detailsHeight -= lipgloss.Height(helpView)
	detailsHeight -= lipgloss.Height(statusBarView)
	m.details.SetHeight(detailsHeight)

	return lipgloss.JoinVertical(lipgloss.Left,
		statusBarView,
		m.tree.View(),
		m.details.View(),
		helpView,
	)
}

func (m Model) statusBarView() string {
	mode := browseMode.String()
	if m.follow {
		mode = followMode.String()
	}
	status := runningStatus.String()
	if m.done {
		status = completeStatus.String()
	}

	timer := timerStyle.Render("⌛" + m.statusBarTimerView())
	statusVal := statusText.Copy().
		Width(m.width - lipgloss.Width(mode) - lipgloss.Width(status) - lipgloss.Width(timer)).
		Render("")

	return mode + statusVal + status + timer
}

func (m Model) helpView() string {
	return m.help.View(keys)
}

func (m Model) waitForActivity() tea.Cmd {
	return func() tea.Msg {
		msg, ok := m.journal.ReadEntry()
		if ok {
			return msg
		}

		return endMsg{}
	}
}

func newSpinner() spinner.Model {
	return spinner.New(
		spinner.WithStyle(lipgloss.NewStyle().Foreground(colorStarted)),
		spinner.WithSpinner(spinner.MiniDot),
	)
}
