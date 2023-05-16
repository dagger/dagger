package tui

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
	"github.com/vito/progrock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func New(quit func(), r progrock.Reader) *Model {
	return &Model{
		quit: quit,
		tree: &Tree{
			viewport:  viewport.New(80, 1),
			spinner:   newSpinner(),
			collapsed: make(map[TreeEntry]bool),
			focus:     true,
		},
		itemsByID:         make(map[string]*Item),
		groupsByID:        make(map[string]*Group),
		futureMemberships: make(map[string][]*Group),
		details:           Details{},
		follow:            true,
		updates:           r,
		help:              help.New(),
	}
}

type Model struct {
	quit func()

	updates           progrock.Reader
	itemsByID         map[string]*Item
	groupsByID        map[string]*Group
	futureMemberships map[string][]*Group

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

type CommandOutMsg struct {
	Output []byte
}

type CommandExitMsg struct {
	Err error
}

type EditorExitMsg struct {
	Err error
}

type endMsg struct{}

func (m Model) adjustLocalTime(t *timestamppb.Timestamp) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}

	adjusted := t.AsTime().Add(m.localTimeDiff)
	cp := proto.Clone(t).(*timestamppb.Timestamp)
	cp.Seconds = int64(adjusted.Unix())
	cp.Nanos = int32(adjusted.Nanosecond())
	return cp
}

type followMsg struct{}

func Follow() tea.Msg {
	return followMsg{}
}

func followTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
		return Follow()
	})
}

func (m Model) IsDone() bool {
	return m.done
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.tree.SetWidth(msg.Width)
		m.details.SetWidth(msg.Width)
	case tea.KeyMsg:
		return m.processKeyMsg(msg)
	case EditorExitMsg:
		if msg.Err != nil {
			// TODO(vito)
			// fmt.Fprintln(m.rootLogs, errorStyle.Render(msg.Err.Error()))
		}
		return m, nil
	case followMsg:
		if !m.follow {
			return m, nil
		}

		m.tree.Follow()

		return m, tea.Batch(
			m.details.SetItem(m.tree.Current()),
			followTick(),
		)
	case *progrock.StatusUpdate:
		return m.processUpdate(msg)
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
		// ignore; we get an occasional <nil> message, not sure where it's from,
		// but logging will disrupt the UI
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
		return m, Follow
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
	case key.Matches(msg, keys.Home):
		if m.detailsFocus {
			newDetails, cmd := m.details.Update(msg)
			m.details = newDetails.(Details)
			return m, cmd
		}
		m.follow = false
		m.tree.MoveToTop()
		if m.tree.Current() != nil {
			return m, m.details.SetItem(m.tree.Current())
		}
	case key.Matches(msg, keys.End):
		if m.detailsFocus {
			newDetails, cmd := m.details.Update(msg)
			m.details = newDetails.(Details)
			return m, cmd
		}
		m.follow = false
		m.tree.MoveToBottom()
		if m.tree.Current() != nil {
			return m, m.details.SetItem(m.tree.Current())
		}
	case key.Matches(msg, keys.PageUp):
		if m.detailsFocus {
			newDetails, cmd := m.details.Update(msg)
			m.details = newDetails.(Details)
			return m, cmd
		}
		m.follow = false
		m.tree.PageUp()
		if m.tree.Current() != nil {
			return m, m.details.SetItem(m.tree.Current())
		}
	case key.Matches(msg, keys.PageDown):
		if m.detailsFocus {
			newDetails, cmd := m.details.Update(msg)
			m.details = newDetails.(Details)
			return m, cmd
		}
		m.follow = false
		m.tree.PageDown()
		if m.tree.Current() != nil {
			return m, m.details.SetItem(m.tree.Current())
		}
	case key.Matches(msg, keys.Collapse):
		m.tree.Collapse(m.tree.Current(), false)
	case key.Matches(msg, keys.Expand):
		m.tree.Expand(m.tree.Current(), false)
	case key.Matches(msg, keys.CollapseAll):
		m.tree.Collapse(m.tree.Root(), true)
	case key.Matches(msg, keys.ExpandAll):
		m.tree.Expand(m.tree.Root(), true)
	case key.Matches(msg, keys.Switch):
		m.detailsFocus = !m.detailsFocus
		m.tree.Focus(!m.detailsFocus)
		m.details.Focus(m.detailsFocus)
	case key.Matches(msg, keys.Open):
		return m, m.details.Open()
	}
	return m, nil
}

func (m Model) processUpdate(msg *progrock.StatusUpdate) (tea.Model, tea.Cmd) {
	cmds := []tea.Cmd{
		m.waitForActivity(),
	}

	for _, g := range msg.Groups {
		grp, found := m.groupsByID[g.Id]
		if !found {
			grp = NewGroup(g.Id, g.Name)
			m.groupsByID[g.Id] = grp
			if g.Parent != nil {
				parent := m.groupsByID[g.GetParent()]
				parent.Add(grp)
			} else {
				m.tree.SetRoot(grp)
			}
		}
		// TODO: update group completion
		_ = grp
	}

	for _, v := range msg.Vertexes {
		if m.localTimeDiff == 0 && v.Started != nil {
			m.localTimeDiff = time.Since(v.Started.AsTime())
		}
		v.Started = m.adjustLocalTime(v.Started)
		v.Completed = m.adjustLocalTime(v.Completed)

		if v.Internal {
			// ignore
			continue
		}

		item := m.itemsByID[v.Id]
		if item == nil {
			item = NewItem(v, m.width)
			cmds = append(cmds, item.Init())
			m.itemsByID[v.Id] = item
			if !item.Internal() {
				cmds = append(cmds, m.details.SetItem(m.tree.Current()))
			}
		}

		for _, g := range m.futureMemberships[v.Id] {
			g.Add(item)
		}

		delete(m.futureMemberships, v.Id)

		item.UpdateVertex(v)
	}

	for _, mem := range msg.Memberships {
		g, found := m.groupsByID[mem.Group]
		if !found {
			panic("group not found: " + mem.Group)
		}

		for _, id := range mem.Vertexes {
			i, found := m.itemsByID[id]
			if found {
				g.Add(i)
			} else {
				m.futureMemberships[id] = append(m.futureMemberships[id], g)
			}
		}
	}

	for _, s := range msg.Tasks {
		item := m.itemsByID[s.Vertex]
		if item == nil {
			continue
		}
		item.UpdateStatus(s)
	}

	for _, l := range msg.Logs {
		item := m.itemsByID[l.Vertex]
		if item == nil {
			continue
		}
		item.UpdateLog(l)
	}

	return m, tea.Batch(cmds...)
}

func (m Model) statusBarTimerView() string {
	root := m.tree.Root()
	if root == nil || root.Started() == nil {
		return "0.0s"
	}
	current := time.Now()
	if m.done && root.Completed() != nil {
		current = *root.Completed()
	}
	diff := current.Sub(*root.Started())

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

	timer := timerStyle.Render(m.statusBarTimerView())
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
		msg, ok := m.updates.ReadStatus()
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
