package plan

import (
	"bytes"
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
)

const (
	ScalarKind cue.Kind = cue.StringKind | cue.NumberKind | cue.BoolKind
)

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

	// NOTE: THE ORDER OF WRAPPING IS IMPORTANT.
	// Here we're wrapping the missing error with the parent position so the stack trace will be:
	// xxx.path is not set:
	//   cue.mod/pkg/.../def.cue:51
	//   userfile.cue:15
	// If we were to do it the other way around (userfile.cue first,
	// def.cue last) then we would end up with multiple errors pointing to the
	// same userfile position.
	//
	// cueerrors.Sanitize() will REMOVE errors for the same position,
	// thus hiding the error.
	return cueerrors.Wrap(missingErr, cueerrors.Newf(parentPos, ""))
}

func isDockerImage(v *compiler.Value) bool {
	return plancontext.IsFSValue(v.Lookup("rootfs")) && v.Lookup("config").Kind() == cue.StructKind
}

func isPlanConcrete(p *compiler.Value, v *compiler.Value) (err error) {
	// Always assume generated fields are concrete.
	if v.HasAttr("generated") {
		return nil
	}

	defer func() {
		if e := recover(); e != nil {
			cueerr := v.Cue().Err()
			codeRaw, _ := v.Source()
			codeLines := bytes.Split(bytes.TrimSpace(codeRaw), []byte("\n"))
			err = fmt.Errorf("%s: %s", cueerr, codeLines[len(codeLines)-1])
		}
	}()

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
		if !v.IsConcrete() && !hasDefault && !v.IsReference() {
			return fieldMissingErr(p, v)
		}
	// Core types (FS, Secret, Socket): make sure they are references, otherwise abort.
	case plancontext.IsFSValue(v) || plancontext.IsSecretValue(v) || plancontext.IsSocketValue(v):
		// Special case: `dagger.#Scratch` is always concrete
		if plancontext.IsFSScratchValue(v) {
			return nil
		}

		// For the rest, ensure they're references.
		if v.IsReference() {
			return nil
		}

		return fieldMissingErr(p, v)
	// Docker images: make sure the `rootfs` is a reference
	case isDockerImage(v):
		if v.Lookup("rootfs").IsReference() {
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

		var errGroup cueerrors.Error
		for it.Next() {
			if it.IsOptional() {
				continue
			}
			if it.Selector().IsDefinition() {
				continue
			}
			if err := isPlanConcrete(p, compiler.Wrap(it.Value())); err != nil {
				errGroup = cueerrors.Append(errGroup, cueerrors.Promote(err, err.Error()))
			}
		}
		return errGroup
	case kind == cue.BottomKind:
		if err := v.Cue().Err(); err != nil {
			// FIXME: for now only raise `undefined field` errors as `BottomKind`
			// can raise false positives.
			if strings.Contains(err.Error(), "undefined field: ") {
				return err
			}
			return nil
		}
	}

	// Ignore all other types.
	return nil
}
