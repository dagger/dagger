package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"

	"github.com/dagger/dagger/dagql"
)

// Paths name input positions, not cached result numbers. They survive transfer.
type workspaceInputPath []string

type workspaceBoundRead struct {
	Input  workspaceInputPath   `json:"input"`
	Source workspaceInputSource `json:"source"`
}

type workspaceRetainedInput struct {
	Output    workspaceInputPath `json:"output"`
	Input     workspaceInputPath `json:"input"`
	Workspace string             `json:"workspace"`
}

type workspaceInputBinding struct {
	path      workspaceInputPath
	producer  *dagql.ResultCallRef
	workspace string
}

func workspacePathKey(path workspaceInputPath) string {
	encoded, _ := json.Marshal(path)
	return string(encoded)
}

func workspacePathAppend(path workspaceInputPath, step string) workspaceInputPath {
	return append(slices.Clone(path), step)
}

type workspaceModuleObjectInput struct {
	step  string
	value any
}

// These stored inputs are also owned by ModuleObject.AttachDependencyResults.
// Keep this walk read-only: capture must not attach or change cached payloads.
func workspaceModuleObjectInputs(obj *ModuleObject) []workspaceModuleObjectInput {
	var inputs []workspaceModuleObjectInput
	for _, name := range slices.Sorted(maps.Keys(obj.Fields)) {
		inputs = append(inputs, workspaceModuleObjectInput{step: "field:" + name, value: obj.Fields[name]})
	}
	if obj.CollectionBase != nil && obj.CollectionBase.Unwrap() != obj {
		inputs = append(inputs, workspaceModuleObjectInput{step: "collection:base", value: obj.CollectionBase})
	}
	if obj.ArtifactView != nil {
		for i, source := range obj.ArtifactView.sources {
			inputs = append(inputs, workspaceModuleObjectInput{step: "artifactView:" + strconv.Itoa(i), value: source})
		}
	}
	return inputs
}

// workspaceValues visits stored capabilities without following their input
// ancestry. Existing retained records supply the current source route when an
// equal cached object still stores older field IDs.
func (c *workspaceInputCollector) workspaceValues(value any, path workspaceInputPath, checkNative bool, visit func(workspaceInputPath, *dagql.ResultCallRef, string)) error {
	seen := map[uint64]bool{}
	overrides := map[string]string{}
	var walk func(any, workspaceInputPath) error
	walk = func(value any, path workspaceInputPath) error {
		if result, ok := value.(dagql.AnyResult); ok {
			if result == nil || result.Unwrap() == nil {
				return nil
			}
			var present bool
			result, present = result.DerefValue()
			if !present || result == nil || result.Unwrap() == nil {
				return nil
			}
			if c.valueVisitor != nil {
				if err := c.valueVisitor(result); err != nil {
					return err
				}
			}
			id, err := result.ID()
			var ref *dagql.ResultCallRef
			if err == nil && id != nil && id.EngineResultID() != 0 {
				ref = &dagql.ResultCallRef{ResultID: id.EngineResultID()}
				if seen[ref.ResultID] {
					return nil
				}
				seen[ref.ResultID] = true
				defer delete(seen, ref.ResultID)
				facts, complete, err := c.cache.ResultFacts(context.WithoutCancel(c.ctx), c.session, ref, workspaceInputsNamespace)
				if err == nil {
					frame, frameErr := c.cache.ResultCallForRef(ref)
					producerOverrides := map[string]string{}
					for _, record := range facts {
						var fact workspaceInputFact
						if frameErr == nil && frame != nil && json.Unmarshal(record.Payload, &fact) == nil && fact.CapabilitiesComplete && fact.Operation == workspaceInputOperation(c.ctx, frame) {
							if _, object := dagql.UnwrapAs[*ModuleObject](result); object && complete && fact.Complete && len(fact.Retained) == 0 {
								return nil
							}
							for _, retained := range fact.Retained {
								key := workspacePathKey(append(slices.Clone(path), retained.Output...))
								if previous, exists := producerOverrides[key]; exists && previous != retained.Workspace {
									return fmt.Errorf("stored Workspace capability has ambiguous source bindings for %s", fact.Operation)
								}
								producerOverrides[key] = retained.Workspace
								// Current outer bindings take precedence over
								// metadata on the borrowed nested payload.
								if _, exists := overrides[key]; !exists {
									overrides[key] = retained.Workspace
								}
							}
						}
					}
				}
			}
			if ws, ok := dagql.UnwrapAs[*Workspace](result); ok && ws != nil {
				if ref == nil {
					frame, err := result.ResultCall()
					if err != nil {
						return err
					}
					ref = &dagql.ResultCallRef{Call: frame}
				}
				key, overridden := overrides[workspacePathKey(path)]
				if !overridden {
					key = workspaceInputKey(ws)
					if ref.ResultID != 0 {
						key, err = c.workspaceBoundary(ref)
						if err != nil {
							return err
						}
					}
				}
				visit(path, ref, key)
				return nil
			}
			if obj, ok := dagql.UnwrapAs[*ModuleObject](result); ok && obj != nil {
				for _, input := range workspaceModuleObjectInputs(obj) {
					if err := walk(input.value, workspacePathAppend(path, input.step)); err != nil {
						return err
					}
				}
				return nil
			}
			if sdk, ok := dagql.UnwrapAs[*WorkspaceSDK](result); ok && sdk != nil {
				if sdk.Workspace.Self() == nil {
					return fmt.Errorf("WorkspaceSDK has no retained Workspace")
				}
				return walk(sdk.Workspace, workspacePathAppend(path, "field:workspace"))
			}
			if _, ok := dagql.UnwrapAs[*WorkspaceModule](result); ok {
				owner, err := c.workspaceModuleOwner(result)
				if err != nil {
					return err
				}
				return walk(owner, workspacePathAppend(path, "field:workspace"))
			}
			if source, ok := dagql.UnwrapAs[*ModuleSource](result); ok && source != nil {
				return walk(source.Workspace, workspacePathAppend(path, "field:workspace"))
			}
			if git, ok := dagql.UnwrapAs[*WorkspaceGit](result); ok && git != nil {
				return walk(git.Workspace, workspacePathAppend(path, "field:workspace"))
			}
			if address, ok := dagql.UnwrapAs[*Address](result); ok && address != nil {
				return walk(address.BoundWorkspace, workspacePathAppend(path, "field:workspace"))
			}
			if list, ok := result.Unwrap().(dagql.Enumerable); ok {
				frame, err := result.ResultCall()
				if err != nil {
					return err
				}
				for i := 1; i <= list.Len(); i++ {
					item, err := list.NthValue(i, frame)
					if err != nil {
						return err
					}
					if err := walk(item, workspacePathAppend(path, "index:"+strconv.Itoa(i))); err != nil {
						return err
					}
				}
				return nil
			}
			if checkNative {
				// Keep one inventory of native retained inputs. A wrapper
				// outside the structural shapes above uses the existing
				// traversal, and cannot authorize skipping its stored fields.
				probe, err := newWorkspaceInputCollector(c.ctx)
				if err != nil {
					return err
				}
				probe.evaluating = true
				probe.retainedValue(result)
				if len(probe.result.Sources) != 0 || len(probe.result.FindUps) != 0 || len(probe.result.Issues) != 0 {
					c.capabilitiesIncomplete = true
				}
			}
			return nil
		}
		if fields, ok := value.(map[string]any); ok {
			for _, name := range slices.Sorted(maps.Keys(fields)) {
				if err := walk(fields[name], workspacePathAppend(path, "field:"+name)); err != nil {
					return err
				}
			}
			return nil
		}
		if value != nil && reflect.TypeOf(value).Kind() == reflect.Slice {
			list := reflect.ValueOf(value)
			for i := 0; i < list.Len(); i++ {
				if err := walk(list.Index(i).Interface(), workspacePathAppend(path, "index:"+strconv.Itoa(i+1))); err != nil {
					return err
				}
			}
			return nil
		}
		_, _, err := attachModuleObjectValue(c.ctx, func(result dagql.AnyResult) (dagql.AnyResult, error) {
			return result, walk(result, path)
		}, value)
		return err
	}
	return walk(value, path)
}

// WorkspaceModule methods use the exact producer's receiver as their owner.
// A list item first refers to its list producer. Do not infer an owner from
// arbitrary call ancestry or from another producer with equal metadata.
func (c *workspaceInputCollector) workspaceModuleOwner(value dagql.AnyResult) (dagql.AnyResult, error) {
	frame, err := value.ResultCall()
	if err != nil {
		return nil, err
	}
	if frame == nil || frame.Receiver == nil {
		return nil, fmt.Errorf("WorkspaceModule has no Workspace receiver")
	}
	if frame.Nth > 0 {
		list, err := c.cache.ResultCallForRef(frame.Receiver)
		if err != nil {
			return nil, err
		}
		// Attached items point to the list row. Detached items produced
		// while capturing an array keep the list's original receiver.
		if list != nil && list.Type != nil && list.Type.Elem != nil {
			if frame.Field != list.Field || frame.Kind != list.Kind || frame.SyntheticOp != list.SyntheticOp || !reflect.DeepEqual(frame.Type, list.Type.Elem) {
				return nil, fmt.Errorf("WorkspaceModule has an invalid list projection")
			}
			frame = list
		}
	}
	if frame.Receiver == nil || frame.Receiver.ResultID == 0 {
		return nil, fmt.Errorf("WorkspaceModule has no attached Workspace receiver")
	}
	owner, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, frame.Receiver.ResultID)
	if err != nil {
		return nil, err
	}
	if _, sdk := dagql.UnwrapAs[*WorkspaceSDK](owner); sdk {
		// SDK modules/clients are stored metadata, not loadable module
		// handles: the module resolvers require a Workspace receiver.
		selectable, err := c.server.ToSelectable(c.ctx, owner)
		if err != nil {
			return nil, err
		}
		spec, found := selectable.ObjectType().FieldSpec(frame.Field, frame.View)
		if frame.Module == nil && frame.Kind == dagql.ResultCallKindField &&
			(frame.Field == "modules" || frame.Field == "clients") && found && spec.Trivial {
			typ := dagql.NewResultCallType(spec.Type.Type())
			if typ.Elem != nil && typ.Elem.NamedType == "WorkspaceModule" &&
				(reflect.DeepEqual(frame.Type, typ) || frame.Nth > 0 && reflect.DeepEqual(frame.Type, typ.Elem)) {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("WorkspaceModule has an invalid SDK metadata projection")
	}
	if workspace, ok := dagql.UnwrapAs[*Workspace](owner); !ok || workspace == nil {
		return nil, fmt.Errorf("WorkspaceModule receiver is not a Workspace")
	}
	return owner, nil
}

func (c *workspaceInputCollector) inputBindings(frame *dagql.ResultCall) ([]workspaceInputBinding, error) {
	var bindings []workspaceInputBinding
	add := func(path workspaceInputPath, ref *dagql.ResultCallRef, workspace string) {
		bindings = append(bindings, workspaceInputBinding{path: slices.Clone(path), producer: ref, workspace: workspace})
	}
	var literal func(*dagql.ResultCallLiteral, workspaceInputPath) error
	ref := func(ref *dagql.ResultCallRef, path workspaceInputPath) error {
		if ref == nil || ref.ResultID == 0 {
			return nil
		}
		value, err := c.cache.LoadResultByResultID(c.ctx, c.session, c.server, ref.ResultID)
		if err != nil {
			return err
		}
		return c.workspaceValues(value, path, false, add)
	}
	literal = func(value *dagql.ResultCallLiteral, path workspaceInputPath) error {
		if value == nil {
			return nil
		}
		if err := ref(value.ResultRef, path); err != nil {
			return err
		}
		for i, item := range value.ListItems {
			if err := literal(item, workspacePathAppend(path, "index:"+strconv.Itoa(i+1))); err != nil {
				return err
			}
		}
		for _, field := range value.ObjectFields {
			if field != nil {
				if err := literal(field.Value, workspacePathAppend(path, "field:"+field.Name)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := ref(frame.Receiver, workspaceInputPath{"receiver"}); err != nil {
		return nil, err
	}
	for _, arg := range frame.Args {
		if arg != nil {
			if err := literal(arg.Value, workspaceInputPath{"argument:" + arg.Name}); err != nil {
				return nil, err
			}
		}
	}
	for _, input := range frame.ImplicitInputs {
		if input != nil {
			if err := literal(input.Value, workspaceInputPath{"implicit:" + input.Name}); err != nil {
				return nil, err
			}
		}
	}
	return bindings, nil
}

// Native views preserve the root's coordinate system. Cwd and overlay values
// remain part of normal execution identity; no view is replayed here.
func (c *workspaceInputCollector) workspaceBindingRoot(ref *dagql.ResultCallRef) uint64 {
	seen := map[uint64]bool{}
walk:
	for ref != nil && ref.ResultID != 0 && !seen[ref.ResultID] {
		seen[ref.ResultID] = true
		frame, err := c.cache.ResultCallForRef(ref)
		if err != nil || frame == nil {
			return ref.ResultID
		}
		if frame.Module != nil {
			// A returned Workspace can retain a proven input binding even
			// when the producing call is a module helper.
			facts, _, err := c.cache.ResultFacts(context.WithoutCancel(c.ctx), c.session, ref, workspaceInputsNamespace)
			if err == nil {
				for _, record := range facts {
					var fact workspaceInputFact
					if json.Unmarshal(record.Payload, &fact) != nil || !fact.CapabilitiesComplete || fact.Operation != workspaceInputOperation(c.ctx, frame) {
						continue
					}
					for _, retained := range fact.Retained {
						if len(retained.Output) == 0 && len(retained.Input) != 0 {
							bindings, err := c.inputBindings(frame)
							if err == nil {
								if binding, err := workspaceFindBinding(bindings, retained.Input); err == nil {
									ref = binding.producer
									continue walk
								}
							}
						}
					}
				}
			}
			return ref.ResultID
		}
		if frame.Receiver == nil {
			return ref.ResultID
		}
		receiver, err := c.cache.ResultCallForRef(frame.Receiver)
		if err != nil || receiver == nil || receiver.Type == nil || receiver.Type.NamedType != "Workspace" {
			return ref.ResultID
		}
		if !workspaceInputView(&dagql.CallRequest{ResultCall: frame, ReceiverTypeName: "Workspace"}) {
			return ref.ResultID
		}
		ref = frame.Receiver
	}
	return 0
}

func (c *workspaceInputCollector) matchingBindings(ref *dagql.ResultCallRef, bindings []workspaceInputBinding) []workspaceInputBinding {
	root := c.workspaceBindingRoot(ref)
	var matched []workspaceInputBinding
	if root == 0 {
		return nil
	}
	for _, binding := range bindings {
		if c.workspaceBindingRoot(binding.producer) == root {
			matched = append(matched, binding)
		}
	}
	return matched
}

func workspaceFindBinding(bindings []workspaceInputBinding, path workspaceInputPath) (workspaceInputBinding, error) {
	for _, binding := range bindings {
		if slices.Equal(binding.path, path) {
			return binding, nil
		}
	}
	return workspaceInputBinding{}, fmt.Errorf("Workspace input binding %s is unavailable", workspacePathKey(path))
}

func (c *workspaceInputCollector) materializeReads(fact workspaceInputFact, frame *dagql.ResultCall) workspaceInputSummary {
	summary := fact.Prepared
	summary.Sources = slices.Clone(summary.Sources)
	summary.FindUps = slices.Clone(summary.FindUps)
	summary.Issues = slices.Clone(summary.Issues)
	// Fixed context origins are ordinary input origins during inspection.
	// Only call completion promotes them to events on the enclosing call.
	summary.contextInputs = nil
	summary.merge(fixedWorkspaceContextInputs(fact.ContextInputs))
	summary.incomplete = summary.incomplete || !fact.Complete
	summary.FindUps = append(summary.FindUps, fact.FindUps...)
	summary.reads = nil
	summary.findUps = nil
	summary.Issues = append(summary.Issues, fact.Issues...)
	if len(fact.Reads) == 0 {
		return summary
	}
	bindings, err := c.inputBindings(frame)
	if err != nil {
		summary.Issues = append(summary.Issues, "cannot inspect Workspace input bindings: "+err.Error())
		return summary
	}
	for _, read := range fact.Reads {
		binding, err := workspaceFindBinding(bindings, read.Input)
		if err != nil {
			summary.Issues = append(summary.Issues, err.Error())
			continue
		}
		source := read.Source
		source.Workspace = binding.workspace
		source.producer = binding.producer
		summary.Sources = append(summary.Sources, source)
		summary.reads = append(summary.reads, source)
	}
	return summary
}
