package plan

import (
	"cuelang.org/go/cue"
)

type Action struct {
	Name     string
	Hidden   bool
	Path     cue.Path
	Comment  string
	Children []*Action
	// pkg      string
}

func (a *Action) AddChild(c *Action) {
	a.Children = append(a.Children, c)
}

func (a *Action) FindByPath(path cue.Path) *Action {
	queue := []*Action{a}

	for len(queue) > 0 {
		nextUp := queue[0]
		queue = queue[1:]
		if nextUp.Path.String() == path.String() {
			return nextUp
		}
		if len(nextUp.Children) > 0 {
			queue = append(queue, nextUp.Children...)
		}
	}
	return nil
}
