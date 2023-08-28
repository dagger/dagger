package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type TreeEntry interface {
	tea.Model

	ID() string
	Inputs() []string

	Name() string

	Entries() []TreeEntry

	Infinite() bool

	Started() *time.Time
	Completed() *time.Time
	Cached() bool
	Error() *string

	SetWidth(int)
	SetHeight(int)
	ScrollPercent() float64

	Save(dir string) (string, error)
	Open() tea.Cmd
}

type Tree struct {
	viewport viewport.Model

	root          TreeEntry
	currentOffset int
	focus         bool

	spinner   spinner.Model
	collapsed map[TreeEntry]bool
}

func (m *Tree) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *Tree) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}

func (m *Tree) SetRoot(root *Group) {
	m.root = root
}

func (m *Tree) SetWidth(width int) {
	m.viewport.Width = width
}

func (m *Tree) SetHeight(height int) {
	m.viewport.Height = height
}

func (m *Tree) UsedHeight() int {
	if m.root == nil {
		return 0
	}

	return m.height(m.root)
}

func (m Tree) Root() TreeEntry {
	return m.root
}

func (m Tree) Current() TreeEntry {
	return m.nth(m.root, m.currentOffset)
}

func (m *Tree) Focus(focus bool) {
	m.focus = focus
}

func (m *Tree) Open() tea.Cmd {
	return m.Current().Open()
}

func (m *Tree) View() string {
	if m.root == nil {
		return ""
	}

	offset := m.currentOffset

	views := m.itemView(0, m.root, []bool{})

	m.viewport.SetContent(strings.Join(views, "\n"))

	if offset >= m.viewport.YOffset+m.viewport.Height {
		m.viewport.SetYOffset(offset - m.viewport.Height + 1)
	}

	if offset < m.viewport.YOffset {
		m.viewport.SetYOffset(offset)
	}

	return m.viewport.View()
}

func (m *Tree) treePrefixView(padding []bool) string {
	pad := strings.Builder{}
	for i, last := range padding {
		leaf := i == len(padding)-1

		switch {
		case leaf && !last:
			pad.WriteString(" ├─")
		case leaf && last:
			pad.WriteString(" └─")
		case !leaf && !last:
			pad.WriteString(" │ ")
		case !leaf && last:
			pad.WriteString("   ")
		}
	}
	return pad.String()
}

func (m *Tree) statusView(item TreeEntry) string {
	if item.Cached() {
		return cachedStatus.String()
	}
	if item.Error() != nil {
		return failedStatus.String()
	}
	if item.Started() != nil {
		if item.Completed() != nil {
			return completedStatus.String()
		}
		return m.spinner.View()
	}
	return " "
}

func (m *Tree) timerView(item TreeEntry) string {
	if item.Started() == nil {
		return ""
	}
	if item.Cached() {
		return itemTimerStyle.Render("CACHED ")
	}
	done := item.Completed()
	if done == nil {
		now := time.Now()
		done = &now
	}
	diff := done.Sub(*item.Started())

	prec := 1
	sec := diff.Seconds()
	if sec < 10 {
		prec = 2
	} else if sec < 100 {
		prec = 1
	}
	return itemTimerStyle.Render(fmt.Sprintf("%.[2]*[1]fs ", sec, prec))
}

func (m *Tree) height(item TreeEntry) int {
	height := 1
	entries := item.Entries()
	if entries == nil || m.collapsed[item] {
		return height
	}

	for _, e := range entries {
		height += m.height(e)
	}

	return height
}

func (m *Tree) itemView(offset int, item TreeEntry, padding []bool) []string {
	renderedItems := []string{}

	status := " " + m.statusView(item) + " "
	treePrefix := m.treePrefixView(padding)
	expandView := ""
	if item.Entries() != nil {
		if collapsed := m.collapsed[item]; collapsed {
			expandView = "▶ "
		} else {
			expandView = "▼ "
		}
	}
	timerView := m.timerView(item)

	itemWidth := m.viewport.Width -
		lipgloss.Width(status) -
		lipgloss.Width(treePrefix) -
		lipgloss.Width(timerView)

	nameWidth := itemWidth -
		lipgloss.Width(expandView) -
		2 // space on each side

	name := trunc(item.Name(), nameWidth)

	itemView := lipgloss.NewStyle().
		Inline(true).
		Width(max(0, itemWidth)).
		Render(" " + expandView + name + " ")

	view := status + treePrefix
	if item == m.Current() {
		if m.focus && offset == m.currentOffset {
			view += selectedStyle.Render(itemView + timerView)
		} else {
			view += selectedStyleBlur.Render(itemView + timerView)
		}
	} else {
		view += itemView + timerView
	}

	renderedItems = append(renderedItems, view)
	offset++

	entries := item.Entries()
	if entries == nil || m.collapsed[item] {
		return renderedItems
	}

	for i, s := range entries {
		pad := append([]bool{}, padding...)
		if i == len(entries)-1 {
			pad = append(pad, true)
		} else {
			pad = append(pad, false)
		}

		views := m.itemView(offset, s, pad)
		offset += len(views)
		renderedItems = append(renderedItems, views...)
	}

	return renderedItems
}

func (m *Tree) MoveUp() {
	if m.currentOffset == 0 {
		return
	}
	m.currentOffset--
}

func (m *Tree) MoveToTop() {
	m.currentOffset = 0
}

func (m *Tree) MoveDown() {
	if m.currentOffset == m.height(m.root)-1 {
		return
	}
	m.currentOffset++
}

func (m *Tree) MoveToBottom() {
	m.currentOffset = m.height(m.root) - 1
}

func (m *Tree) PageUp() {
	for i := 0; i < m.viewport.Height; i++ {
		m.MoveUp()
	}
}

func (m *Tree) PageDown() {
	for i := 0; i < m.viewport.Height; i++ {
		m.MoveDown()
	}
}

func (m *Tree) Collapse(entry TreeEntry, recursive bool) {
	m.setCollapsed(entry, true, recursive)
}

func (m *Tree) Expand(entry TreeEntry, recursive bool) {
	m.setCollapsed(entry, false, recursive)
}

func (m *Tree) setCollapsed(entry TreeEntry, collapsed, recursive bool) {
	// Non collapsible
	if entry == nil || entry.Entries() == nil {
		return
	}
	m.collapsed[entry] = collapsed
	if !recursive {
		return
	}
	for _, e := range entry.Entries() {
		m.setCollapsed(e, collapsed, recursive)
	}
}

func (m *Tree) Follow() {
	if m.root == nil {
		return
	}

	if m.root.Completed() != nil {
		// go back to the root node on completion
		m.currentOffset = 0
		return
	}

	current := m.Current()
	if current == nil {
		return
	}

	if current.Completed() == nil && len(current.Entries()) == 0 {
		return
	}

	oldest := m.findOldestIncompleteEntry(m.root)
	if oldest != -1 {
		m.currentOffset = oldest
	}
}

func (m *Tree) findOldestIncompleteEntry(entry TreeEntry) int {
	var oldestIncompleteEntry TreeEntry
	oldestStartedTime := time.Time{}

	var search func(e TreeEntry)

	search = func(e TreeEntry) {
		started := e.Started()
		completed := e.Completed()
		cached := e.Cached()
		entries := e.Entries()

		if e.Infinite() {
			// avoid following services, since they run forever
			return
		}

		if len(entries) == 0 && started != nil && completed == nil && !cached {
			if oldestIncompleteEntry == nil || started.Before(oldestStartedTime) {
				oldestStartedTime = *started
				oldestIncompleteEntry = e
			}
		}

		for _, child := range entries {
			search(child)
		}
	}

	search(entry)

	if oldestIncompleteEntry == nil {
		return -1
	}

	return m.indexOf(0, entry, oldestIncompleteEntry)
}

func (m *Tree) indexOf(offset int, entry TreeEntry, needle TreeEntry) int {
	if entry == needle {
		return offset
	}
	offset++
	for _, child := range entry.Entries() {
		if found := m.indexOf(offset, child, needle); found != -1 {
			return found
		}
		offset += m.height(child)
	}
	return -1
}

func (m *Tree) nth(entry TreeEntry, n int) TreeEntry {
	if n == 0 {
		return entry
	}
	if m.collapsed[entry] {
		return nil
	}
	skipped := 1
	for _, child := range entry.Entries() {
		if found := m.nth(child, n-skipped); found != nil {
			return found
		}
		skipped += m.height(child)
	}
	return nil
}
