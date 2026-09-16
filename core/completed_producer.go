package core

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// RecordCompletedProducer records an operation on a fresh, exclusively owned
// output before publication. It never evaluates or replaces an existing recipe.
func RecordCompletedProducer[T interface {
	dagql.Typed
	*Directory | *File
}](value T, producer Lazy[T]) error {
	if value == nil || nilProducerValue(producer) {
		return fmt.Errorf("record completed producer: nil value or producer")
	}
	switch value := any(value).(type) {
	case *Directory:
		if value.Lazy != nil || value.completedRecipe != nil || value.completedRecipeKind != "" || len(value.completedRecipeJSON) != 0 {
			return fmt.Errorf("record completed Directory producer: operation already recorded")
		}
		if _, ok := any(producer).(*DirectoryRestoreLazy); ok {
			return fmt.Errorf("record completed Directory producer: restore-only operation")
		}
		if _, _, err := producedDirectoryOutput(value); err != nil {
			return fmt.Errorf("record completed Directory producer: %w", err)
		}
		value.completedRecipe = any(producer).(Lazy[*Directory])
	case *File:
		if value.Lazy != nil || value.completedRecipe != nil || value.completedRecipeKind != "" || len(value.completedRecipeJSON) != 0 {
			return fmt.Errorf("record completed File producer: operation already recorded")
		}
		if _, ok := any(producer).(*FileRestoreLazy); ok {
			return fmt.Errorf("record completed File producer: restore-only operation")
		}
		if _, _, err := producedFileOutput(value); err != nil {
			return fmt.Errorf("record completed File producer: %w", err)
		}
		value.completedRecipe = any(producer).(Lazy[*File])
	}
	return nil
}

func nilProducerValue(value any) bool {
	if value == nil {
		return true
	}
	switch reflect.ValueOf(value).Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return reflect.ValueOf(value).IsNil()
	}
	return false
}

func producedDirectoryOutput(dir *Directory) (string, bkcache.ImmutableRef, error) {
	if dir == nil || dir.Dir == nil || dir.Snapshot == nil {
		return "", nil, fmt.Errorf("missing Directory accessors")
	}
	path, ok := dir.Dir.Peek()
	if !ok {
		return "", nil, fmt.Errorf("Directory path is unset")
	}
	snapshot, ok := dir.Snapshot.Peek()
	if !ok || nilProducerValue(snapshot) {
		return "", nil, fmt.Errorf("Directory snapshot is unset")
	}
	return path, snapshot, nil
}
func producedFileOutput(file *File) (string, bkcache.ImmutableRef, error) {
	if file == nil || file.File == nil || file.Snapshot == nil {
		return "", nil, fmt.Errorf("missing File accessors")
	}
	path, ok := file.File.Peek()
	if !ok {
		return "", nil, fmt.Errorf("File path is unset")
	}
	snapshot, ok := file.Snapshot.Peek()
	if !ok || nilProducerValue(snapshot) {
		return "", nil, fmt.Errorf("File snapshot is unset")
	}
	return path, snapshot, nil
}
func validateProducedDirectoryReceiver(dir *Directory) error {
	if dir == nil || dir.Dir == nil || dir.Snapshot == nil {
		return fmt.Errorf("producer receiver: missing Directory accessors")
	}
	if snapshot, ok := dir.Snapshot.Peek(); ok && !nilProducerValue(snapshot) {
		return fmt.Errorf("producer receiver: Directory snapshot already installed")
	}
	if dir.stored != nil {
		return fmt.Errorf("producer receiver: Directory has stored snapshot")
	}
	return nil
}
func validateProducedFileReceiver(file *File) error {
	if file == nil || file.File == nil || file.Snapshot == nil {
		return fmt.Errorf("producer receiver: missing File accessors")
	}
	if snapshot, ok := file.Snapshot.Peek(); ok && !nilProducerValue(snapshot) {
		return fmt.Errorf("producer receiver: File snapshot already installed")
	}
	if file.stored != nil {
		return fmt.Errorf("producer receiver: File has stored snapshot")
	}
	return nil
}

// moveProducedDirectory transfers the ref owned by a temporary output. Neither
// value may be published, and the source must not be a borrowed dependency.
func moveProducedDirectory(dst, src *Directory) error {
	if err := validateProducedDirectoryReceiver(dst); err != nil {
		return err
	}
	path, snapshot, err := producedDirectoryOutput(src)
	if err != nil {
		return err
	}
	dst.Dir.setValue(path)
	dst.Services = slices.Clone(src.Services)
	dst.Snapshot.setValue(snapshot)
	src.Snapshot = new(LazyAccessor[bkcache.ImmutableRef, *Directory])
	return nil
}
func moveProducedFile(dst, src *File) error {
	if err := validateProducedFileReceiver(dst); err != nil {
		return err
	}
	path, snapshot, err := producedFileOutput(src)
	if err != nil {
		return err
	}
	dst.File.setValue(path)
	dst.Services = slices.Clone(src.Services)
	dst.Snapshot.setValue(snapshot)
	src.Snapshot = new(LazyAccessor[bkcache.ImmutableRef, *File])
	return nil
}
