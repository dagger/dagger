package core

import (
	"encoding/json"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
)

type Group []string

func (g Group) Add(name ...string) Group {
	// make a copy of this group, don't modify in-place
	copy := make(Group, 0, len(g)+len(name))
	copy = append(copy, g...)

	// create the sub-group
	copy = append(copy, name...)
	return copy
}

func (g Group) ID() string {
	id, err := json.Marshal(g)
	if err != nil {
		panic(err)
	}
	return string(id)
}

func (g Group) Name() string {
	if len(g) == 0 {
		return ""
	}
	return g[len(g)-1]
}

func (g Group) ProgressGroup() *pb.ProgressGroup {
	return &pb.ProgressGroup{
		Id:   g.ID(),
		Name: g.Name(),
	}
}

func (g Group) LLBOpt() llb.ConstraintsOpt {
	pg := g.ProgressGroup()
	return llb.ProgressGroup(pg.Id, pg.Name, pg.Weak)
}

type CustomName struct {
	Name     string `json:"name,omitempty"`
	Group    Group  `json:"group,omitempty"`
	Internal bool   `json:"internal,omitempty"`
}

func (c CustomName) String() string {
	enc, err := json.Marshal(c)
	if err != nil {
		return ""
	}
	return string(enc)
}

func (c CustomName) LLBOpt() llb.ConstraintsOpt {
	return llb.WithCustomName(c.String())
}
