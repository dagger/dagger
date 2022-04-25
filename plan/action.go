package plan

import (
	"cuelang.org/go/cue"
	"go.dagger.io/dagger/compiler"
)

type Action struct {
	Name          string
	Hidden        bool
	Path          cue.Path
	Documentation string
	Children      []*Action
	Value         *compiler.Value
	inputs        []Input
}

type Input struct {
	Name          string
	Type          string
	Documentation string
}

func (a *Action) AddChild(c *Action) {
	a.Children = append(a.Children, c)
}

func (a *Action) FindByPath(path cue.Path) *Action {
	if a == nil {
		return nil
	}
	queue := []*Action{a}

	for len(queue) > 0 && queue != nil {
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

func (a *Action) FindClosest(path cue.Path) *Action {
	if a == nil {
		return a
	}
	if a.Path.String() == path.String() {
		return a
	}

	commonSubPath := commonSubPath(a.Path, path)
	if commonSubPath.String() == a.Path.String() {
		if len(a.Children) > 0 {
			for _, c := range a.Children {
				if c.Path.String() == path.String() {
					return c.FindClosest(path)
				}
			}
		}
		return a
	}
	return nil
}

func commonSubPath(a, b cue.Path) cue.Path {
	if a.String() == b.String() {
		return a
	}
	aSelectors := a.Selectors()
	bSelectors := b.Selectors()
	commonSelectors := []cue.Selector{}
	for i := 0; i < len(aSelectors) && i < len(bSelectors); i++ {
		if aSelectors[i].String() != bSelectors[i].String() {
			break
		}
		commonSelectors = append(commonSelectors, aSelectors[i])
	}
	return cue.MakePath(commonSelectors...)
}

func (a *Action) Inputs() []Input {
	if a.inputs == nil {
		cueVal := a.Value.Cue()
		inputs := []Input{}
		for iter, _ := cueVal.Fields(cue.All()); iter.Next(); {
			val := iter.Value()
			cVal := compiler.Wrap(val)

			_, refPath := val.ReferencePath()

			ik := val.IncompleteKind()
			validKind := ik == cue.StringKind || ik == cue.NumberKind || ik == cue.BoolKind || ik == cue.IntKind || ik == cue.FloatKind
			if validKind && !val.IsConcrete() && len(refPath.Selectors()) == 0 {
				inputs = append(inputs, Input{
					Name:          iter.Label(),
					Type:          ik.String(),
					Documentation: cVal.DocSummary(),
				})
			}
		}
		a.inputs = inputs
	}
	return a.inputs
}
