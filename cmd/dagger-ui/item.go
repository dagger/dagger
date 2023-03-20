package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dagger/dagger/core/pipeline"
	bkclient "github.com/moby/buildkit/client"
	"github.com/tonistiigi/units"
)

func NewItem(v *bkclient.Vertex, width int) *Item {
	var name pipeline.CustomName
	if json.Unmarshal([]byte(v.Name), &name) != nil {
		name.Name = v.Name
		if pg := v.ProgressGroup.GetId(); pg != "" {
			if err := json.Unmarshal([]byte(pg), &name.Pipeline); err != nil {
				panic("unmarshal pipeline: " + err.Error())
			}
		}
	}

	group := []string{}
	for _, p := range name.Pipeline {
		if p.Name == "" {
			p.Name = "pipeline"
		}
		group = append(group, p.Name)
	}

	return &Item{
		id:            v.Digest.String(),
		name:          name.Name,
		group:         group,
		logs:          &bytes.Buffer{},
		logsModel:     NewVterm(width),
		statusesModel: viewport.New(width, 1),
		spinner:       newSpinner(),
		width:         width,
	}
}

var _ TreeEntry = &Item{}

type Item struct {
	id            string
	name          string
	group         []string
	started       *time.Time
	completed     *time.Time
	cached        bool
	error         string
	logs          *bytes.Buffer
	logsModel     *Vterm
	statuses      []*bkclient.VertexStatus
	statusesModel viewport.Model
	internal      bool
	spinner       spinner.Model
	width         int
}

func (i *Item) ID() string            { return i.id }
func (i *Item) Name() string          { return i.name }
func (i *Item) Internal() bool        { return i.internal }
func (i *Item) Entries() []TreeEntry  { return nil }
func (i *Item) Started() *time.Time   { return i.started }
func (i *Item) Completed() *time.Time { return i.completed }
func (i *Item) Cached() bool          { return i.cached }
func (i *Item) Error() string         { return i.error }

func (i *Item) UpdateVertex(v *bkclient.Vertex) {
	// Started clock might reset for each layer when pulling images.
	// We want to keep the original started time and only updated the completed time.
	if i.started == nil && v.Started != nil {
		i.started = v.Started
	}
	i.completed = v.Completed
	i.cached = v.Cached
	i.error = v.Error
}

func (i *Item) UpdateLog(log *bkclient.VertexLog) {
	i.logsModel.Write(log.Data)
}

func (i *Item) UpdateStatus(status *bkclient.VertexStatus) {
	var current *bkclient.VertexStatus
	for _, s := range i.statuses {
		if s.ID == status.ID {
			current = s
			break
		}
	}

	if current == nil {
		i.statuses = append(i.statuses, status)
	} else {
		*current = *status
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
		if len(i.statuses) > 0 {
			statusM, cmd := i.statusesModel.Update(msg)
			i.statusesModel = statusM
			return i, cmd
		}
		vtermM, cmd := i.logsModel.Update(msg)
		i.logsModel = vtermM.(*Vterm)
		return i, cmd
	}
}

func (i *Item) View() string {
	if len(i.statuses) > 0 {
		i.statusesModel.SetContent(i.statusView())
		return i.statusesModel.View()
	}

	return i.logsModel.View()
}

func (i *Item) SetHeight(height int) {
	i.logsModel.SetHeight(height)
	i.statusesModel.Height = height
}

func (i *Item) SetWidth(width int) {
	i.width = width
	i.logsModel.SetWidth(width)
	i.statusesModel.Width = width
}

func (i *Item) ScrollPercent() float64 {
	if len(i.statuses) > 0 {
		return i.statusesModel.ScrollPercent()
	}
	return i.logsModel.ScrollPercent()
}

func (i *Item) statusView() string {
	statuses := []string{}

	bar := progress.New(progress.WithSolidFill("2"))
	bar.Width = i.width / 4

	for _, s := range i.statuses {
		status := completedStatus.String() + " "
		if s.Completed == nil {
			status = i.spinner.View() + " "
		}

		name := s.ID

		progress := ""
		if s.Total != 0 {
			progress = fmt.Sprintf("%.2f / %.2f", units.Bytes(s.Current), units.Bytes(s.Total))
			progress += " " + bar.ViewAs(float64(s.Current)/float64(s.Total))
		} else if s.Current != 0 {
			progress = fmt.Sprintf("%.2f", units.Bytes(s.Current))
		}

		pad := strings.Repeat(" ", i.width-lipgloss.Width(status)-lipgloss.Width(name)-lipgloss.Width(progress))
		view := status + name + pad + progress
		statuses = append(statuses, view)
	}

	return strings.Join(statuses, "\n")
}

type Group struct {
	id          string
	name        string
	entries     []TreeEntry
	entriesByID map[string]TreeEntry
	height      int
}

var _ TreeEntry = &Group{}

func (g *Group) ID() string {
	return g.id
}

func (g *Group) Name() string {
	return g.name
}

func (g *Group) Entries() []TreeEntry {
	return g.entries
}

func NewGroup(id, name string) *Group {
	return &Group{
		id:          id,
		name:        name,
		entries:     []TreeEntry{},
		entriesByID: make(map[string]TreeEntry),
	}
}

func (g *Group) Add(group []string, e TreeEntry) {
	if len(group) == 0 {
		g.entries = append(g.entries, e)
		g.entriesByID[e.ID()] = e
		return
	}
	parent := group[0]
	sub, ok := g.entriesByID[parent]
	if !ok {
		sub = NewGroup(path.Join(g.id, parent), parent)
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

func (g *Group) Error() string {
	for _, e := range g.entries {
		if e.Error() != "" {
			return e.Error()
		}
	}
	return ""
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

func (g *Group) SetHeight(height int)   { g.height = height }
func (g *Group) SetWidth(int)           {}
func (g *Group) ScrollPercent() float64 { return 1 }

func (g *Group) Init() tea.Cmd {
	return nil
}

func (g *Group) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return g, nil
}

func (g *Group) View() string {
	return strings.Repeat("\n", g.height-1)
}
