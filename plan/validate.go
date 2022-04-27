package plan

import (
	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
)

const (
	ScalarKind cue.Kind = cue.StringKind | cue.NumberKind | cue.BoolKind
)

func isReference(v *compiler.Value) bool {
	// FIXME: this expands an expression into parts and checks if any of them are references.
	// Use case:
	// docker/run.cue: `rootfs: dagger.#FS & _exec.output`
	// This was tricking reference checking into thinking it is NOT a reference.
	// However, this approach can have false positives as well (`dagger.#FS` IS a reference on its own)
	_, expr := v.Expr()
	for _, i := range expr {
		_, refPath := i.ReferencePath()
		if len(refPath.Selectors()) > 0 {
			return true
		}
	}

	return false
}

func fieldMissingErr(p *compiler.Value, field *compiler.Value) error {
	missingErr := cueerrors.Newf(field.Pos(), "%q is not set", field.Path().String())

	// Wrap the error with the position of the parent value.
	//
	// For instance, given `actions: foo: docker.#Run & {}`,
	// if `actions.foo.input` is missing, grab the position of `actions.foo`
	// for the stack trace.
	selectors := field.Path().Selectors()
	if len(selectors) == 0 {
		return missingErr
	}

	parentSelectors := selectors[0 : len(selectors)-1]
	parent := p.LookupPath(cue.MakePath(parentSelectors...))
	parentPos := parent.Pos()
	if !parent.Exists() || parentPos == token.NoPos {
		return missingErr
	}

	return cueerrors.Wrap(cueerrors.Newf(parentPos, ""), missingErr)
}

func isDockerImage(v *compiler.Value) bool {
	return plancontext.IsFSValue(v.Lookup("rootfs")) && v.Lookup("config").Kind() == cue.StructKind
}

func isPlanConcrete(p *compiler.Value, v *compiler.Value) error {
	kind := v.IncompleteKind()
	_, hasDefault := v.Default()

	switch {
	// Scalar types (string, int, etc)
	// Make sure the value is either:
	// - Concrete (e.g. `foo: "bar"`) OR
	// - Has a default (e.g. `foo: *"bar" | string`) OR
	// - Is a reference (e.g. `foo: bar`)
	// Otherwise, abort.
	case kind.IsAnyOf(ScalarKind):
		if !v.IsConcrete() && !hasDefault && !isReference(v) {
			return fieldMissingErr(p, v)
		}
	// Core types (FS, Secret, Socket): make sure they are references, otherwise abort.
	case plancontext.IsFSValue(v) || plancontext.IsSecretValue(v) || plancontext.IsSocketValue(v):
		// Special case: `dagger.#Scratch` is always concrete
		if plancontext.IsFSScratchValue(v) {
			return nil
		}

		// For the rest, ensure they're references.
		if isReference(v) {
			return nil
		}

		return fieldMissingErr(p, v)
	// Docker images: make sure the `rootfs` is a reference
	case isDockerImage(v):
		if isReference(v.Lookup("rootfs")) {
			return nil
		}
		return fieldMissingErr(p, v)

	// For structures, recursively call this function to check sub-fields
	case kind == cue.StructKind:
		if !v.IsConcrete() && !hasDefault {
			return fieldMissingErr(p, v)
		}
		it, err := v.Cue().Fields(cue.All())
		if err != nil {
			return compiler.Err(err)
		}

		for it.Next() {
			if it.IsOptional() {
				continue
			}
			if it.Selector().IsDefinition() {
				continue
			}

			if compiler.Wrap(it.Value()).HasAttr("generated") {
				continue
			}

			if err := isPlanConcrete(p, compiler.Wrap(it.Value())); err != nil {
				return err
			}
		}
	}

	// Ignore all other types.
	return nil
}
