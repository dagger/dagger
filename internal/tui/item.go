package tui

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tonistiigi/units"
	"github.com/vito/progrock"
)

func NewItem(v *progrock.Vertex, width int) *Item {
	saneName := strings.Join(strings.Fields(v.Name), " ")

	return &Item{
		id:         v.Id,
		inputs:     v.Inputs,
		name:       saneName,
		logs:       &bytes.Buffer{},
		logsModel:  NewVterm(width),
		tasksModel: viewport.New(width, 1),
		spinner:    newSpinner(),
		width:      width,
	}
}

var _ TreeEntry = &Item{}

type Item struct {
	id         string
	inputs     []string
	name       string
	group      []string
	started    *time.Time
	completed  *time.Time
	cached     bool
	error      *string
	logs       *bytes.Buffer
	logsModel  *Vterm
	tasks      []*progrock.VertexTask
	tasksModel viewport.Model
	internal   bool
	spinner    spinner.Model
	width      int
	isInfinite bool
}

func (i *Item) ID() string            { return i.id }
func (i *Item) Inputs() []string      { return i.inputs }
func (i *Item) Name() string          { return i.name }
func (i *Item) Internal() bool        { return i.internal }
func (i *Item) Entries() []TreeEntry  { return nil }
func (i *Item) Started() *time.Time   { return i.started }
func (i *Item) Completed() *time.Time { return i.completed }
func (i *Item) Cached() bool          { return i.cached }
func (i *Item) Infinite() bool        { return i.isInfinite }

func (i *Item) Error() *string {
	return i.error
}

func (i *Item) Save(dir string) (string, error) {
	filePath := filepath.Join(dir, sanitizeFilename(i.Name()))
	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("save item to %s as %s: %w", dir, filePath, err)
	}

	if err := i.logsModel.Print(f); err != nil {
		return "", err
	}

	if err := f.Close(); err != nil {
		return "", err
	}

	return filePath, nil
}

func (i *Item) Open() tea.Cmd {
	dir, err := os.MkdirTemp("", "dagger-logs.*")
	if err != nil {
		return func() tea.Msg {
			return EditorExitMsg{err}
		}
	}

	filePath, err := i.Save(dir)
	if err != nil {
		return func() tea.Msg {
			return EditorExitMsg{err}
		}
	}

	return openEditor(filePath)
}

func (i *Item) UpdateVertex(v *progrock.Vertex) {
	// Started clock might reset for each layer when pulling images.
	// We want to keep the original started time and only updated the completed time.
	if i.started == nil && v.Started != nil {
		t := v.Started.AsTime()
		i.started = &t
	}
	if v.Completed != nil {
		t := v.Completed.AsTime()
		i.completed = &t
	}
	i.cached = v.Cached
	i.error = v.Error
}

func (i *Item) UpdateLog(log *progrock.VertexLog) {
	i.logsModel.Write(log.Data)
}

func (i *Item) UpdateStatus(task *progrock.VertexTask) {
	var current int = -1
	for i, s := range i.tasks {
		if s.Name == task.Name {
			current = i
			break
		}
	}

	if current == -1 {
		i.tasks = append(i.tasks, task)
	} else {
		i.tasks[current] = task
	}
}

var _ tea.Model = &Item{}

// Init is called when the item is first created _and_ when it is selected.
func (i *Item) Init() tea.Cmd {
	return i.spinner.Tick
}

func (i *Item) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		spinnerM, cmd := i.spinner.Update(msg)
		i.spinner = spinnerM
		return i, cmd
	default:
		if len(i.tasks) > 0 {
			statusM, cmd := i.tasksModel.Update(msg)
			i.tasksModel = statusM
			return i, cmd
		}
		vtermM, cmd := i.logsModel.Update(msg)
		i.logsModel = vtermM.(*Vterm)
		return i, cmd
	}
}

func (i *Item) View() string {
	if len(i.tasks) > 0 {
		i.tasksModel.SetContent(i.tasksView())
		return i.tasksModel.View()
	}

	return i.logsModel.View()
}

func (i *Item) SetHeight(height int) {
	i.logsModel.SetHeight(height)
	i.tasksModel.Height = height
}

func (i *Item) SetWidth(width int) {
	i.width = width
	i.logsModel.SetWidth(width)
	i.tasksModel.Width = width
}

func (i *Item) ScrollPercent() float64 {
	if len(i.tasks) > 0 {
		return i.tasksModel.ScrollPercent()
	}
	return i.logsModel.ScrollPercent()
}

func (i *Item) tasksView() string {
	tasks := []string{}

	bar := progress.New(progress.WithSolidFill("2"))
	bar.Width = i.width / 4

	for _, t := range i.tasks {
		status := completedStatus.String() + " "
		if t.Completed == nil {
			status = i.spinner.View() + " "
		}

		name := t.Name

		progress := ""
		if t.Total != 0 {
			progress = fmt.Sprintf("%.2f / %.2f", units.Bytes(t.Current), units.Bytes(t.Total))
			progress += " " + bar.ViewAs(float64(t.Current)/float64(t.Total))
		} else if t.Current != 0 {
			progress = fmt.Sprintf("%.2f", units.Bytes(t.Current))
		}

		pad := strings.Repeat(" ", max(0, i.width-lipgloss.Width(status)-lipgloss.Width(name)-lipgloss.Width(progress)))
		view := status + name + pad + progress
		tasks = append(tasks, view)
	}

	return strings.Join(tasks, "\n")
}

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

func NewGroup(id, name string, logs groupModel) *Group {
	return &Group{
		groupModel: logs,

		id:          id,
		name:        name,
		entries:     []TreeEntry{},
		entriesByID: make(map[string]TreeEntry),
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

func (g *Group) Add(group []string, e TreeEntry) {
	defer g.sort()

	if len(group) == 0 {
		g.entries = append(g.entries, e)
		g.entriesByID[string(e.ID())] = e
		return
	}

	parent := group[0]
	sub, ok := g.entriesByID[parent]
	if !ok {
		sub = NewGroup(path.Join(g.id, parent), parent, &emptyGroup{})
		g.entries = append(g.entries, sub)
		g.entriesByID[sub.Name()] = sub
	}
	subGroup, ok := sub.(*Group)
	if !ok {
		panic("add item to non-group")
	}
	subGroup.Add(group[1:], e)
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

		// sort ancestors first
		if g.isAncestor(ie, je) {
			return true
		} else if g.isAncestor(je, ie) {
			return false
		}

		// fall back on name (not sure if this will ever occur)
		return ie.Name() < je.Name()
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

		e, ok := g.entriesByID[string(d)]
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

type logsPrinter struct {
	*Vterm

	name string
}

func (lp logsPrinter) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := lp.Vterm.Update(msg)
	lp.Vterm = m.(*Vterm)
	return lp, cmd
}

func (lp logsPrinter) Save(dir string) (string, error) {
	filePath := filepath.Join(dir, sanitizeFilename(lp.name))
	f, err := os.Create(filePath)
	if err != nil {
		return "", err
	}

	if err := lp.Print(f); err != nil {
		return "", err
	}

	if err := f.Close(); err != nil {
		return "", err
	}

	return filePath, nil
}
