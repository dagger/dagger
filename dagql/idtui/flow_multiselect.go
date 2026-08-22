package idtui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// FlowMultiSelect makes a multi-select compose vertically with the field after
// it: Down on the final option advances focus instead of stopping at the list
// boundary. All other behavior remains owned by huh.MultiSelect.
type FlowMultiSelect[T comparable] struct {
	*huh.MultiSelect[T]
	last T
}

func NewFlowMultiSelect[T comparable](field *huh.MultiSelect[T], last T) *FlowMultiSelect[T] {
	return &FlowMultiSelect[T]{MultiSelect: field, last: last}
}

func (field *FlowMultiSelect[T]) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "down" {
		if hovered, ok := field.Hovered(); ok && hovered == field.last {
			return field, huh.NextField
		}
	}
	model, cmd := field.MultiSelect.Update(msg)
	field.MultiSelect = model.(*huh.MultiSelect[T])
	return field, cmd
}
