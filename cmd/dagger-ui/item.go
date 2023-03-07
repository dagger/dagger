package main

import (
	"bytes"
	"encoding/json"
	"path"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/core/pipeline"
	bkclient "github.com/moby/buildkit/client"
)

func NewItem(v *bkclient.Vertex) *Item {
	var name pipeline.CustomName
	if json.Unmarshal([]byte(v.Name), &name) != nil {
		name.Name = v.Name
		if pg := v.ProgressGroup.GetId(); pg != "" {
			if err := json.Unmarshal([]byte(pg), &name.Pipeline); err != nil {
				panic(err)
			}
		}
	}

	group := []string{}
	for _, p := range name.Pipeline {
		group = append(group, p.Name)
	}

	return &Item{
		id:    v.Digest.String(),
		name:  name.Name,
		group: group,
		logs:  &bytes.Buffer{},
	}
}

var _ TreeEntry = &Item{}

type Item struct {
	id        string
	name      string
	group     []string
	started   *time.Time
	completed *time.Time
	cached    bool
	logs      *bytes.Buffer
	statuses  []*bkclient.VertexStatus
	internal  bool
}

func (i *Item) ID() string                       { return i.id }
func (i *Item) Name() string                     { return i.name }
func (i *Item) Internal() bool                   { return i.internal }
func (i *Item) Entries() []TreeEntry             { return nil }
func (i *Item) Started() *time.Time              { return i.started }
func (i *Item) Completed() *time.Time            { return i.completed }
func (i *Item) Cached() bool                     { return i.cached }
func (i *Item) Logs() *bytes.Buffer              { return i.logs }
func (i *Item) Status() []*bkclient.VertexStatus { return i.statuses }

func (i *Item) UpdateVertex(v *bkclient.Vertex) {
	// Started clock might reset for each layer when pulling images.
	// We want to keep the original started time and only updated the completed time.
	if i.started == nil && v.Started != nil {
		i.started = v.Started
	}
	i.completed = v.Completed
	i.cached = v.Cached
}

func (i *Item) UpdateLog(log *bkclient.VertexLog) {
	i.logs.Write(log.Data)
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
		return
	}
	*current = *status
}

type Group struct {
	id          string
	name        string
	entries     []TreeEntry
	entriesByID map[string]TreeEntry
}

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

func (g *Group) Logs() *bytes.Buffer {
	return &bytes.Buffer{}
}

func (g *Group) Status() []*bkclient.VertexStatus {
	return []*bkclient.VertexStatus{}
}

func (m Model) waitForActivity() tea.Cmd {
	return func() tea.Msg {
		return <-m.ch
	}
}
