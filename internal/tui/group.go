package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/dagql/idtui"
)

type groupModel interface {
	tea.Model

	SetHeight(int)
	SetWidth(int)
	ScrollPercent() float64

	Save(dir string) (string, error)
}

type Group struct {
	groupModel

	id          string
	name        string
	entries     []TreeEntry
	entriesByID map[string]TreeEntry
}

func NewGroup(id, name string) *Group {
	return &Group{
		groupModel: &emptyGroup{},

		id:          id,
		name:        name,
		entries:     []TreeEntry{},
		entriesByID: map[string]TreeEntry{},
	}
}

var _ TreeEntry = &Group{}

func (g *Group) ID() string {
	return g.id
}

func (g *Group) Inputs() []string {
	return nil
}

func (g *Group) Name() string {
	return g.name
}

func (g *Group) Entries() []TreeEntry {
	return g.entries
}

func (g *Group) Save(dir string) (string, error) {
	subDir := filepath.Join(dir, sanitizeFilename(g.Name()))

	if err := os.MkdirAll(subDir, 0700); err != nil {
		return "", err
	}

	if _, err := g.groupModel.Save(subDir); err != nil {
		return "", err
	}

	for _, e := range g.entries {
		if _, err := e.Save(subDir); err != nil {
			return "", err
		}
	}

	return subDir, nil
}

func (g *Group) Open() tea.Cmd {
	dir, err := os.MkdirTemp("", "dagger-logs.*")
	if err != nil {
		return func() tea.Msg { return EditorExitMsg{err} }
	}

	subDir, err := g.Save(dir)
	if err != nil {
		return func() tea.Msg { return EditorExitMsg{err} }
	}

	return openEditor(subDir)
}

func (g *Group) Add(e TreeEntry) {
	if e.ID() == idtui.PrimaryVertex {
		g.name = e.Name()
		g.groupModel = e
		return
	}

	_, has := g.entriesByID[e.ID()]
	if has {
		return
	}
	g.entriesByID[e.ID()] = e
	g.entries = append(g.entries, e)
	g.sort()
}

func (g *Group) Cached() bool {
	for _, e := range g.entries {
		if !e.Cached() {
			return false
		}
	}
	return true
}

func (g *Group) Error() *string {
	for _, e := range g.entries {
		if e.Error() != nil {
			return e.Error()
		}
	}
	return nil
}

func (g *Group) Infinite() bool {
	return false
}

func (g *Group) Started() *time.Time {
	timers := []*time.Time{}
	for _, e := range g.entries {
		timers = append(timers, e.Started())
	}
	sort.Slice(timers, func(i, j int) bool {
		if timers[i] == nil {
			return false
		}
		if timers[j] == nil {
			return true
		}
		return timers[i].Before(*timers[j])
	})

	if len(timers) == 0 {
		return nil
	}

	return timers[0]
}

func (g *Group) Completed() *time.Time {
	timers := []*time.Time{}
	for _, e := range g.entries {
		timers = append(timers, e.Completed())
	}
	sort.Slice(timers, func(i, j int) bool {
		if timers[i] == nil {
			return false
		}
		if timers[j] == nil {
			return true
		}
		return timers[i].Before(*timers[j])
	})

	if len(timers) == 0 {
		return nil
	}

	return timers[len(timers)-1]
}

func (g *Group) SetWidth(w int) {
	g.groupModel.SetWidth(w)
}

func (g *Group) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := g.groupModel.Update(msg)
	g.groupModel = m.(groupModel)
	return g, cmd
}

func (g *Group) ScrollPercent() float64 {
	return g.groupModel.ScrollPercent()
}

func (g *Group) sort() {
	sort.SliceStable(g.entries, func(i, j int) bool {
		ie := g.entries[i]
		je := g.entries[j]
		switch {
		case g.isAncestor(ie, je):
			return true
		case g.isAncestor(je, ie):
			return false
		case ie.Started() == nil && je.Started() == nil:
			// both pending
			return false
		case ie.Started() == nil && je.Started() != nil:
			// j started first
			return false
		case ie.Started() != nil && je.Started() == nil:
			// i started first
			return true
		case ie.Started() != nil && je.Started() != nil:
			return ie.Started().Before(*je.Started())
		default:
			// impossible
			return false
		}
	})
}

func (g *Group) isAncestor(i, j TreeEntry) bool {
	if i == j {
		return false
	}

	id := i.ID()

	for _, d := range j.Inputs() {
		if d == id {
			return true
		}

		e, ok := g.entriesByID[d]
		if ok && g.isAncestor(i, e) {
			return true
		}
	}

	return false
}

type emptyGroup struct {
	height int
}

func (g *emptyGroup) SetHeight(height int) {
	g.height = height
}

func (g *emptyGroup) SetWidth(int) {}

func (g *emptyGroup) ScrollPercent() float64 { return 1 }

func (*emptyGroup) Init() tea.Cmd {
	return nil
}

func (g *emptyGroup) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return g, nil
}

func (g emptyGroup) View() string {
	return strings.Repeat("\n", g.height-1)
}

func (g emptyGroup) Save(dir string) (string, error) {
	return "", nil
}
