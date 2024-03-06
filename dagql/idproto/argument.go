package idproto

import (
	"fmt"
)

type Argument struct {
	raw   *RawArgument
	value *Literal
}

func NewArgument(name string, value *Literal) *Argument {
	return &Argument{
		raw: &RawArgument{
			Name:  name,
			Value: value.raw,
		},
		value: value,
	}
}

func (arg *Argument) Name() string {
	return arg.raw.Name
}

func (arg *Argument) Value() *Literal {
	return arg.value
}

// Tainted returns true if the ID contains any tainted selectors.
func (arg *Argument) Tainted() bool {
	return arg.value.Tainted()
}

func (arg *Argument) gatherIDs(idsByDigest map[string]*RawID_Fields) {
	if arg == nil {
		return
	}
	arg.value.gatherIDs(idsByDigest)
}

func (arg *Argument) decode(
	raw *RawArgument,
	idsByDigest map[string]*RawID_Fields,
	memo map[string]*ID,
) error {
	if raw == nil {
		return nil
	}

	arg.raw = raw
	if raw.Value != nil {
		arg.value = new(Literal)
		if err := arg.value.decode(raw.Value, idsByDigest, memo); err != nil {
			return fmt.Errorf("failed to decode argument value: %w", err)
		}
	}
	return nil
}
