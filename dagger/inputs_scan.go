package dagger

import (
	"context"

	"dagger.io/go/dagger/compiler"
	"github.com/rs/zerolog/log"
)

func isReference(val *compiler.Value) bool {
	_, ref := val.ReferencePath()

	if ref.String() == "" || val.Path().String() == ref.String() {
		return false
	}

	for _, s := range ref.Selectors() {
		if s.IsDefinition() {
			return false
		}
	}

	return true
}

func ScanInputs(ctx context.Context, value *compiler.Value) []*compiler.Value {
	lg := log.Ctx(ctx)
	inputs := []*compiler.Value{}

	value.Walk(
		func(val *compiler.Value) bool {
			if isReference(val) {
				lg.Debug().Str("value.Path", val.Path().String()).Msg("found reference, stop walk")
				return false
			}

			if !val.HasAttr("input") {
				return true
			}

			lg.Debug().Str("value.Path", val.Path().String()).Msg("found input")
			inputs = append(inputs, val)

			return true
		}, nil,
	)

	return inputs
}
