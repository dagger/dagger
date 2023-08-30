package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/tonistiigi/units"
	"github.com/vito/progrock"
	"github.com/vito/progrock/ui"
)

func NewItem(v *progrock.Vertex, width int) *Item {
	saneName := strings.Join(strings.Fields(v.Name), " ")

	return &Item{
		id:         v.Id,
		inputs:     v.Inputs,
		name:       saneName,
		logs:       &bytes.Buffer{},
		logsModel:  ui.NewVterm(),
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
	started    *time.Time
	completed  *time.Time
	cached     bool
	error      *string
	logs       *bytes.Buffer
	logsModel  *ui.Vterm
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
	filePath := filepath.Join(dir, sanitizeFilename(i.Name())) + ".log"
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
	var current = -1
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
		i.logsModel = vtermM.(*ui.Vterm)
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
