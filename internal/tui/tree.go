package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/opencontainers/go-digest"
)

type TreeEntry interface {
	tea.Model

	ID() digest.Digest
	Inputs() []digest.Digest

	Name() string

	Entries() []TreeEntry

	Started() *time.Time
	Completed() *time.Time
	Cached() bool
	Error() string

	SetWidth(int)
	SetHeight(int)
	ScrollPercent() float64
}

type Tree struct {
	viewport viewport.Model

	root    TreeEntry
	current TreeEntry
	focus   bool

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

func (m *Tree) SetRoot(root TreeEntry) {
	m.root = root
	if m.current == nil {
		m.current = root
	}
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

func (m Tree) Current() TreeEntry {
	return m.current
}

func (m *Tree) Focus(focus bool) {
	m.focus = focus
}

func (m *Tree) View() string {
	if m.root == nil {
		return ""
	}

	offset := m.currentOffset(m.root)

	views := m.itemView(m.root, []bool{})

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
	if item.Error() != "" {
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

func (m *Tree) currentOffset(item TreeEntry) int {
	if item == m.current {
		return 0
	}

	offset := 1

	entries := item.Entries()
	for i, entry := range entries {
		if entry == item {
			return i
		}

		if !m.collapsed[entry] {
			entryOffset := m.currentOffset(entry)
			if entryOffset != -1 {
				return offset + entryOffset
			}
		}

		offset += m.height(entry)
	}

	return -1
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

func (m *Tree) itemView(item TreeEntry, padding []bool) []string {
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
	if item == m.current {
		if m.focus {
			view += selectedStyle.Render(itemView + timerView)
		} else {
			view += selectedStyleBlur.Render(itemView + timerView)
		}
	} else {
		view += itemView + timerView
	}

	renderedItems = append(renderedItems, view)

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

		renderedItems = append(renderedItems, m.itemView(s, pad)...)
	}

	return renderedItems
}

func (m *Tree) MoveUp() {
	prev := m.findPrev(m.current)
	if prev == nil {
		prev = lastEntry(m.root)
	}
	m.current = prev
}

func lastEntry(entry TreeEntry) TreeEntry {
	entries := entry.Entries()
	if len(entries) == 0 {
		return entry
	}
	return lastEntry(entries[len(entries)-1])
}

func (m *Tree) MoveDown() {
	next := m.findNext(m.current)
	if next == nil {
		next = m.root
	}
	m.current = next
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

	if m.current == nil {
		return
	}

	if m.current.Completed() == nil && len(m.current.Entries()) == 0 {
		return
	}

	oldest := findOldestIncompleteEntry(m.root)
	if oldest != nil {
		m.current = oldest
	}
}

// findParent returns the parent entry containing the given `entry`
func (m *Tree) findParent(group TreeEntry, entry TreeEntry) TreeEntry {
	entries := group.Entries()
	for _, e := range entries {
		if e == entry {
			return group
		}
		if found := m.findParent(e, entry); found != nil {
			return found
		}
	}
	return nil
}

// findSibilingAfter returns the entry immediately after the specified entry within the same parent.
// `nil` if not found or if entry is the last entry.
func (m *Tree) findSibilingAfter(parent, entry TreeEntry) TreeEntry {
	entries := parent.Entries()
	for i, e := range entries {
		if e != entry {
			continue
		}
		newPos := i + 1
		if newPos >= len(entries) {
			return nil
		}
		return entries[newPos]
	}
	return nil
}

// findSibilingBefore returns the entry immediately preceding the specified entry within the same parent.
// `nil` if not found or if entry is the first entry.
func (m *Tree) findSibilingBefore(parent, entry TreeEntry) TreeEntry {
	entries := parent.Entries()
	for i, e := range entries {
		if e != entry {
			continue
		}
		newPos := i - 1
		if newPos < 0 {
			return nil
		}
		return entries[newPos]
	}
	return nil
}

func (m *Tree) findNext(entry TreeEntry) TreeEntry {
	// If this entry has entries, pick the first child
	if entries := entry.Entries(); !m.collapsed[entry] && len(entries) > 0 {
		return entries[0]
	}

	// Otherwise, pick the next sibiling in the same parent group
	parent := m.findParent(m.root, entry)
	if parent == nil {
		return nil
	}

	for {
		if next := m.findSibilingAfter(parent, entry); next != nil {
			return next
		}
		// We reached the end of the group, try again with the grand-parent
		entry = parent
		parent = m.findParent(m.root, entry)
		if parent == nil {
			return nil
		}
	}
}

func (m *Tree) findPrev(entry TreeEntry) TreeEntry {
	parent := m.findParent(m.root, entry)
	if parent == nil {
		return nil
	}
	prev := m.findSibilingBefore(parent, entry)
	// If there's no previous element, pick the parent.
	if prev == nil {
		return parent
	}
	// If the previous sibiling is a group, go to the last element recursively
	for {
		entries := prev.Entries()
		if m.collapsed[prev] || len(entries) == 0 {
			return prev
		}
		prev = entries[len(entries)-1]
	}
}

func findOldestIncompleteEntry(entry TreeEntry) TreeEntry {
	var oldestIncompleteEntry TreeEntry
	oldestStartedTime := time.Time{}

	var search func(e TreeEntry)

	search = func(e TreeEntry) {
		started := e.Started()
		completed := e.Completed()
		cached := e.Cached()
		entries := e.Entries()

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

	return oldestIncompleteEntry
}
