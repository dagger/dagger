package core

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"slices"

	"github.com/dagger/dagger/dagql"
	bkcache "github.com/dagger/dagger/engine/snapshots"
)

// RecordCompletedContainerMountProducer is a construction-only recorder for
// the two eager schema mount operations. Its inputs remain borrowed results.
func RecordCompletedContainerMountProducer(value *Container, producer Lazy[*Container]) error {
	if value == nil || nilProducerValue(producer) {
		return fmt.Errorf("record completed Container mount: nil value or producer")
	}
	if value.Lazy != nil || value.completedRecipe != nil || len(value.completedRecipeJSON) != 0 || value.acquiredOutput.Load() != nil || len(value.storedParts) != 0 {
		return fmt.Errorf("record completed Container mount: pending, restored or already recorded value")
	}
	var target string
	var directory bool
	switch p := producer.(type) {
	case *ContainerWithMountedDirectoryLazy:
		if p.Parent.Self() == nil || p.Source.Self() == nil {
			return fmt.Errorf("record completed Container mount: missing parent or source")
		}
		target, directory = p.Target, true
	case *ContainerWithMountedFileLazy:
		if p.Parent.Self() == nil || p.Source.Self() == nil {
			return fmt.Errorf("record completed Container mount: missing parent or source")
		}
		target = p.Target
	default:
		return fmt.Errorf("record completed Container mount: unsupported producer %T", producer)
	}
	if !path.IsAbs(target) || path.Clean(target) != target {
		return fmt.Errorf("record completed Container mount: unresolved target %q", target)
	}
	mount := value.mountAt(target)
	if mount == nil {
		return fmt.Errorf("record completed Container mount: missing target %q", target)
	}
	if directory {
		if mount.DirectorySource == nil {
			return fmt.Errorf("record completed Container mount: target is not a Directory")
		}
		dir, ok := mount.DirectorySource.Peek()
		if !ok {
			return fmt.Errorf("record completed Container mount: unset Directory")
		}
		if _, _, err := producedDirectoryOutput(dir); err != nil {
			return err
		}
	} else {
		if mount.FileSource == nil {
			return fmt.Errorf("record completed Container mount: target is not a File")
		}
		file, ok := mount.FileSource.Peek()
		if !ok {
			return fmt.Errorf("record completed Container mount: unset File")
		}
		if _, _, err := producedFileOutput(file); err != nil {
			return err
		}
	}
	value.completedRecipe = producer
	return nil
}

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
	dst.SetPath(path)
	dst.Services = slices.Clone(src.Services)
	dst.SetSnapshot(snapshot)
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
	dst.SetPath(path)
	dst.Services = slices.Clone(src.Services)
	dst.SetSnapshot(snapshot)
	src.Snapshot = new(LazyAccessor[bkcache.ImmutableRef, *File])
	return nil
}

func attachCompletedProducerInput[T dagql.Typed](attach func(dagql.AnyResult) (dagql.AnyResult, error), input dagql.ObjectResult[T], label string) (dagql.ObjectResult[T], error) {
	if nilProducerValue(input.Self()) {
		return dagql.ObjectResult[T]{}, fmt.Errorf("%s: missing input", label)
	}
	attached, err := attach(input)
	if err != nil {
		return dagql.ObjectResult[T]{}, fmt.Errorf("%s: %w", label, err)
	}
	typed, ok := attached.(dagql.ObjectResult[T])
	if !ok || nilProducerValue(typed.Self()) {
		return dagql.ObjectResult[T]{}, fmt.Errorf("%s: unexpected result %T", label, attached)
	}
	return typed, nil
}

// attachFilesystemDependencyResultsKinds is the shared body of the Directory
// and File AttachDependencyResultsKinds methods: the service bindings' results
// first, then the live recipe's (or, once published, the completed recipe's)
// dependencies as liveness-only receiver/prerequisite links.
func attachFilesystemDependencyResultsKinds[T dagql.Typed](
	ctx context.Context,
	label string,
	services ServiceBindings,
	lazy Lazy[T],
	completedRecipe Lazy[T],
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.DependencyResult, error) {
	serviceDeps, err := services.AttachDependencyResults(label, attach)
	if err != nil {
		return nil, err
	}
	if lazy == nil {
		// A live recipe belongs to exactly one value. Concurrent publication of
		// one shared value is out of scope; attachment updates the recipe's inputs.
		lazy = completedRecipe
	}
	if lazy == nil {
		return serviceDeps, nil
	}
	lazyDeps, err := lazy.AttachDependencies(ctx, attach)
	if err != nil {
		return nil, err
	}
	deps := make([]dagql.DependencyResult, 0, len(serviceDeps)+len(lazyDeps))
	deps = append(deps, serviceDeps...)
	for _, dep := range lazyDeps {
		// Liveness-only: receiver/prerequisite chain. Failures attribute to
		// the parent's own install span via direct lookup, not transitively
		// onto downstream chained calls.
		deps = append(deps, dagql.DependencyResult{Result: dep, Owned: false})
	}
	return deps, nil
}
