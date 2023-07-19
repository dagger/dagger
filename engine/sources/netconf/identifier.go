package netconf

import (
	"github.com/moby/buildkit/solver/llbsolver/provenance"
	"github.com/moby/buildkit/source"
)

type Identifier struct {
	SessionID string `json:"session"`
}

const AttrSessionID = "netconf.session"

var _ source.Identifier = (*Identifier)(nil)

func (*Identifier) Scheme() string {
	return Scheme
}

func (*Identifier) Capture(*provenance.Capture, string) error {
	// nothing to capture
	return nil
}
