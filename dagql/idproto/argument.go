package idproto

import (
	"fmt"

	"github.com/opencontainers/go-digest"
)

type Argument struct {
	raw   *RawArgument
	value *Literal
}

func NewArgument(name string, value *Literal) *Argument {
	return &Argument{
		raw: &RawArgument{
			Name: name,
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

func (arg *Argument) clone(memo map[digest.Digest]*ID) (*Argument, error) {
	if arg == nil {
		return nil, nil
	}

	newArg := &Argument{
		raw: &RawArgument{
			Name: arg.raw.Name,
			// Don't bother cloning the raw value, Argument.encode will set once needed
		},
	}
	var err error
	newArg.value, err = arg.value.clone(memo)
	if err != nil {
		return nil, fmt.Errorf("failed to clone argument value: %w", err)
	}
	return newArg, nil
}

func (arg *Argument) encode(idsByDigest map[string]*RawID_Fields) (*RawArgument, error) {
	if arg == nil {
		return nil, nil
	}

	var err error
	arg.raw.Value, err = arg.value.encode(idsByDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to encode argument value: %w", err)
	}
	return arg.raw, nil
}

func (arg *Argument) decode(
	raw *RawArgument,
	idsByDigest map[string]*RawID_Fields,
	memo map[string]*idState,
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
