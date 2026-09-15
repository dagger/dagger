package call

import (
	"fmt"

	"github.com/dagger/dagger/dagql/call/callpbv1"
)

type Argument struct {
	pb    *callpbv1.Argument
	value Literal

	// isSensitive is true if the argument is sensitive and should not be displayed or
	// included in the encoded call.
	isSensitive bool
}

func NewArgument(name string, value Literal, isSensitive bool) *Argument {
	return &Argument{
		pb: &callpbv1.Argument{
			Name:  name,
			Value: value.pb(),
		},
		value:       value,
		isSensitive: isSensitive,
	}
}

func (arg *Argument) Name() string {
	return arg.pb.Name
}

func (arg *Argument) Value() Literal {
	return arg.value
}

func (arg *Argument) IsSensitive() bool {
	return arg.isSensitive
}

func (arg *Argument) WithValue(value Literal) *Argument {
	return NewArgument(arg.Name(), value, arg.isSensitive)
}

// The pb representation of the arg.
//
// WARRANTY VOID IF MUTATIONS ARE MADE TO THE INNER PROTOBUF. Perform a
// proto.Clone before mutating.
func (arg *Argument) PB() *callpbv1.Argument {
	return arg.pb
}

func (arg *Argument) gatherCalls(callsByDigest map[string]*callpbv1.Call) {
	if arg == nil || arg.isSensitive {
		return
	}
	arg.value.gatherCalls(callsByDigest)
}

func (arg *Argument) decode(
	pb *callpbv1.Argument,
	callsByDigest map[string]*callpbv1.Call,
	memo map[string]*ID,
) error {
	if pb == nil {
		return nil
	}

	arg.pb = pb
	if pb.Value != nil {
		var err error
		arg.value, err = decodeLiteral(pb.Value, callsByDigest, memo)
		if err != nil {
			return fmt.Errorf("failed to decode argument value: %w", err)
		}
	}
	return nil
}
