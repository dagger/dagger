package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/dagger/dagger/dagql/call"
)

// SubscriptionTypeName is the name of the GraphQL Subscription root type.
// It exists in the schema only while at least one subscription field is
// installed (and visible in the view).
const SubscriptionTypeName = "Subscription"

// SubscriptionFunc runs one subscription: it calls emit once per value, in
// order, and returns when the stream ends (nil), fails (an error), or ctx is
// canceled. Emission is synchronous — emit returns only once the value has
// been resolved and handed to the transport — so a resolver cannot run ahead
// of its reader, and the source it reads from (a log, not a Go buffer) holds
// whatever has not been read yet.
//
// emit returns an error when the value could not be delivered: the reader is
// gone (ctx canceled) or resolving its sub-selection failed. The resolver
// must stop and return; the error it returns (or emit's, if it swallows it)
// ends the stream.
//
// Values of object type must be results that carry an ID (typically obtained
// with Server.Select, so that the ID is an honest, re-loadable chain); a bare
// Go value of an object type is refused. Leaf values may be plain Typed.
type SubscriptionFunc func(ctx context.Context, args map[string]Input, view call.View, emit func(Typed) error) error

// SubscriptionField is one field of the Subscription root.
type SubscriptionField struct {
	Spec *FieldSpec
	Func SubscriptionFunc
}

// Subscribe declares a subscription field. Arguments are derived from A
// exactly as for Func; the field's type is R (one value of the stream).
//
// fn blocks for the life of the subscription, calling emit for each value.
// Subscription fields are never cached: there is no receiver and no single
// result to cache. Caching applies to the emitted values, which are ordinary
// results.
func Subscribe[A any, R Typed](name string, fn func(ctx context.Context, args A, emit func(R) error) error) SubscriptionField {
	var zeroArgs A
	inputs, argsErr := InputSpecsForType(zeroArgs, true)
	if argsErr != nil {
		slog.Error("failed to parse subscription args", "field", name, "error", argsErr)
	}
	spec := &FieldSpec{
		Name: name,
		Args: inputs,
	}
	var zeroRet R
	if res, ok := any(zeroRet).(AnyResult); ok {
		spec.Type = res.Unwrap()
	} else {
		spec.Type = zeroRet
	}
	return SubscriptionField{
		Spec: spec,
		Func: func(ctx context.Context, argVals map[string]Input, view call.View, emit func(Typed) error) error {
			if argsErr != nil {
				return argsErr
			}
			var args A
			if err := spec.Args.Decode(argVals, &args, view); err != nil {
				return err
			}
			return fn(ctx, args, func(v R) error {
				return emit(v)
			})
		},
	}
}

// Doc sets the description of the field.
func (field SubscriptionField) Doc(paras ...string) SubscriptionField {
	field.Spec.Description = FormatDescription(paras...)
	return field
}

// View sets a view for this field.
func (field SubscriptionField) View(view ViewFilter) SubscriptionField {
	field.Spec.ViewFilter = view
	return field
}

// Experimental marks the field as experimental.
func (field SubscriptionField) Experimental(paras ...string) SubscriptionField {
	field.Spec.ExperimentalReason = FormatDescription(paras...)
	return field
}

// Args patches the argument specs derived from the args struct, as
// Field.Args does.
func (field SubscriptionField) Args(args ...Argument) SubscriptionField {
	patched := Field[Typed]{Spec: field.Spec}.Args(args...)
	field.Spec = patched.Spec
	return field
}

// Subscriptions is a set of subscription fields to install together.
type Subscriptions []SubscriptionField

// Install installs the fields on the server's Subscription root.
func (fields Subscriptions) Install(srv *Server) {
	srv.InstallSubscription(fields...)
}

// InstallSubscription installs fields on the Subscription root. Like class
// fields, a later install of the same name takes precedence in the views it
// is visible in.
func (s *Server) InstallSubscription(fields ...SubscriptionField) {
	s.installLock.Lock()
	for _, field := range fields {
		if field.Spec == nil || field.Spec.Name == "" {
			panic("subscription field must have a name")
		}
		if field.Spec.Type == nil {
			panic(fmt.Sprintf("subscription field %q has no type", field.Spec.Name))
		}
		if _, ok := s.subscriptions[field.Spec.Name]; !ok {
			s.subscriptionOrder = append(s.subscriptionOrder, field.Spec.Name)
		}
		s.subscriptions[field.Spec.Name] = append(s.subscriptions[field.Spec.Name], field)
	}
	s.installLock.Unlock()
	s.invalidateSchemaCache()
}

// subscriptionFieldLocked returns the field visible in view. Must be called
// with installLock held (read or write).
func (s *Server) subscriptionFieldLocked(name string, view call.View) (SubscriptionField, bool) {
	fields := s.subscriptions[name]
	for i := len(fields) - 1; i >= 0; i-- {
		field := fields[i]
		if field.Spec.ViewFilter == nil || field.Spec.ViewFilter.Contains(view) {
			return field, true
		}
	}
	return SubscriptionField{}, false
}

// SubscriptionField returns the subscription field visible in view.
func (s *Server) SubscriptionField(name string, view call.View) (SubscriptionField, bool) {
	s.installLock.RLock()
	defer s.installLock.RUnlock()
	return s.subscriptionFieldLocked(name, view)
}

// subscriptionDefinitionLocked builds the Subscription root definition for
// view, or nil when no subscription field is visible. Must be called with
// installLock held.
func (s *Server) subscriptionDefinitionLocked(view call.View) *ast.Definition {
	var fields ast.FieldList
	for _, name := range s.subscriptionOrder {
		field, ok := s.subscriptionFieldLocked(name, view)
		if !ok {
			continue
		}
		fields = append(fields, field.Spec.FieldDefinition(view))
	}
	if len(fields) == 0 {
		return nil
	}
	return &ast.Definition{
		Kind:        ast.Object,
		Name:        SubscriptionTypeName,
		Description: "The root of every subscription operation: each field is a stream of values, pushed over a graphql-sse (text/event-stream) response.",
		Fields:      fields,
	}
}

type streamingTransportKey struct{}

// WithStreamingTransport marks ctx as served by a transport that can deliver
// more than one response per operation (dagql.SSE). Subscriptions are refused
// on any other transport, rather than delivering the first value and
// silently dropping the rest.
func WithStreamingTransport(ctx context.Context) context.Context {
	return context.WithValue(ctx, streamingTransportKey{}, true)
}

func isStreamingTransport(ctx context.Context) bool {
	v, _ := ctx.Value(streamingTransportKey{}).(bool)
	return v
}

// ErrSubscriptionNeedsStream is the refusal for a subscription operation sent
// over a non-streaming transport.
var ErrSubscriptionNeedsStream = errors.New(`subscription operations must be sent as a POST with "Accept: text/event-stream" (graphql-sse distinct connections mode); the response is a text/event-stream of "next" events ending with "complete"`)

// execSubscription returns the ResponseHandler for a subscription operation.
// The resolver starts on the first call and runs on its own goroutine; each
// call returns the next resolved value, and nil once the stream has ended.
func (s *Server) execSubscription(execCtx context.Context, gqlOp *graphql.OperationContext) graphql.ResponseHandler {
	if !isStreamingTransport(execCtx) {
		err := gqlerror.Errorf("%s", ErrSubscriptionNeedsStream)
		return graphql.OneShot(&graphql.Response{Errors: gqlerror.List{err}})
	}

	var (
		startOnce sync.Once
		responses = make(chan *graphql.Response)
		cancel    context.CancelCauseFunc
	)
	start := func(ctx context.Context) {
		var subCtx context.Context
		subCtx, cancel = context.WithCancelCause(ctx)
		go func() {
			defer close(responses)
			send := func(resp *graphql.Response) error {
				select {
				case responses <- resp:
					return nil
				case <-subCtx.Done():
					return context.Cause(subCtx)
				}
			}
			err := s.runSubscription(subCtx, gqlOp, send)
			if err != nil && subCtx.Err() == nil {
				_ = send(&graphql.Response{Errors: gqlErrs(err)})
			}
		}()
	}
	return func(ctx context.Context) *graphql.Response {
		startOnce.Do(func() { start(ctx) })
		select {
		case resp, ok := <-responses:
			if !ok {
				cancel(errors.New("subscription ended"))
				return nil
			}
			return resp
		case <-ctx.Done():
			cancel(context.Cause(ctx))
			return nil
		}
	}
}

// runSubscription resolves a subscription operation's single root field,
// calling send with one response per emitted value.
func (s *Server) runSubscription(ctx context.Context, gqlOp *graphql.OperationContext, send func(*graphql.Response) error) error {
	ctx = srvToContext(ctx, s)

	op := gqlOp.Operation
	if op == nil {
		op = gqlOp.Doc.Operations.ForName(gqlOp.OperationName)
	}
	if op == nil || op.Operation != ast.Subscription {
		return fmt.Errorf("not a subscription operation")
	}
	roots, err := collectRootASTFields(op.SelectionSet, gqlOp.Doc.Fragments, map[string]bool{})
	if err != nil {
		return err
	}
	if len(roots) != 1 {
		return fmt.Errorf("a subscription operation must select exactly one root field, got %d", len(roots))
	}
	astField := roots[0]
	if astField.Name == "__typename" {
		return fmt.Errorf("a subscription operation must select a subscription field, not __typename")
	}

	view := s.View
	field, ok := s.SubscriptionField(astField.Name, view)
	if !ok {
		return fmt.Errorf("%s has no such field: %q", SubscriptionTypeName, astField.Name)
	}

	inputArgs, err := subscriptionInputs(field.Spec, view, astField, gqlOp.Variables)
	if err != nil {
		return fmt.Errorf("%s.%s: %w", SubscriptionTypeName, field.Spec.Name, err)
	}

	var subsels []Selection
	if len(astField.SelectionSet) > 0 {
		subsels, err = s.parseASTSelections(ctx, gqlOp, field.Spec.Type.Type(), astField.SelectionSet)
		if err != nil {
			return fmt.Errorf("parse selections: %w", err)
		}
	}
	responseKey := astField.Alias
	if responseKey == "" {
		responseKey = astField.Name
	}

	// One operation lease for the life of the subscription: values are
	// selected and resolved under it, as one long-running query would be.
	leaseCtx, release, err := withOperationLease(ctx)
	if err != nil {
		return fmt.Errorf("acquire operation lease: %w", err)
	}
	ctx = leaseCtx
	defer func() {
		if releaseErr := release(context.WithoutCancel(ctx)); releaseErr != nil {
			slog.Warn("release subscription operation lease", "error", releaseErr)
		}
	}()

	// emitErr is sticky: once a value fails to deliver, the stream is over
	// even if the resolver swallows the error.
	var emitErr error
	emit := func(v Typed) error {
		if emitErr != nil {
			return emitErr
		}
		res, err := s.resolveSubscriptionValue(ctx, v, astField.Name, len(subsels) > 0, subsels)
		if err != nil {
			emitErr = gqlErr(err, ast.Path{ast.PathName(responseKey)})
			return emitErr
		}
		data, err := json.Marshal(map[string]any{responseKey: res})
		if err != nil {
			emitErr = fmt.Errorf("marshal: %w", err)
			return emitErr
		}
		if err := send(&graphql.Response{Data: json.RawMessage(data)}); err != nil {
			emitErr = err
			return err
		}
		return nil
	}
	err = field.Func(ctx, inputArgs, view, emit)
	if emitErr != nil {
		return emitErr
	}
	if err != nil {
		return gqlErr(err, ast.Path{ast.PathName(responseKey)})
	}
	return nil
}

// resolveSubscriptionValue applies the operation's sub-selection to one
// emitted value through the ordinary selection path.
func (s *Server) resolveSubscriptionValue(ctx context.Context, v Typed, fieldName string, hasSubsels bool, subsels []Selection) (any, error) {
	if v == nil {
		return nil, nil
	}
	res, isResult := v.(AnyResult)
	if isResult {
		if res.Unwrap() == nil {
			return nil, nil
		}
	}
	if !hasSubsels {
		if _, ok := v.(AnyObjectResult); ok {
			return nil, fmt.Errorf("field %q of type %q must have a selection of subfields", fieldName, v.Type().Name())
		}
		if isResult {
			return res.Unwrap(), nil
		}
		return v, nil
	}
	if !isResult {
		return nil, fmt.Errorf("subscription %q emitted a bare %T: object values must be results with an ID (select them, e.g. with Server.Select)", fieldName, v)
	}
	node, err := s.toSelectable(ctx, res)
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}
	return s.Resolve(ctx, node, subsels...)
}

// subscriptionInputs decodes a root field's arguments, applying defaults and
// required checks as Class.ParseField and preselect do for object fields.
func subscriptionInputs(spec *FieldSpec, view call.View, astField *ast.Field, vars map[string]any) (map[string]Input, error) {
	given := make(map[string]Input, len(astField.Arguments))
	for _, arg := range astField.Arguments {
		argSpec, ok := spec.Args.Input(arg.Name, view)
		if !ok {
			return nil, fmt.Errorf("no such argument: %q", arg.Name)
		}
		if argSpec.Internal {
			return nil, fmt.Errorf("cannot use internal argument %q", arg.Name)
		}
		val, err := arg.Value.Value(vars)
		if err != nil {
			return nil, err
		}
		if val == nil {
			// An explicit null is an omitted nullable argument.
			continue
		}
		input, err := argSpec.Type.Decoder().DecodeInput(val)
		if err != nil {
			return nil, fmt.Errorf("init arg %q value as %T (%s) using %T: %w", arg.Name, argSpec.Type, argSpec.Type.Type(), argSpec.Type.Decoder(), err)
		}
		given[arg.Name] = input
	}
	inputs := make(map[string]Input, len(given))
	for _, argSpec := range spec.Args.Inputs(view) {
		switch {
		case given[argSpec.Name] != nil:
			inputs[argSpec.Name] = given[argSpec.Name]
		case argSpec.Default != nil:
			inputs[argSpec.Name] = argSpec.Default
		case argSpec.Type.Type().NonNull:
			return nil, fmt.Errorf("missing required argument: %q", argSpec.Name)
		}
	}
	return inputs, nil
}

// collectRootASTFields flattens a subscription's root selection set through
// fragments into its fields.
func collectRootASTFields(sels ast.SelectionSet, fragments ast.FragmentDefinitionList, stack map[string]bool) ([]*ast.Field, error) {
	var fields []*ast.Field
	for _, sel := range sels {
		switch x := sel.(type) {
		case *ast.Field:
			fields = append(fields, x)
		case *ast.InlineFragment:
			sub, err := collectRootASTFields(x.SelectionSet, fragments, stack)
			if err != nil {
				return nil, err
			}
			fields = append(fields, sub...)
		case *ast.FragmentSpread:
			if stack[x.Name] {
				return nil, fmt.Errorf("fragment cycle at %q", x.Name)
			}
			frag := fragments.ForName(x.Name)
			if frag == nil {
				return nil, fmt.Errorf("unknown fragment: %s", x.Name)
			}
			stack[x.Name] = true
			sub, err := collectRootASTFields(frag.SelectionSet, fragments, stack)
			delete(stack, x.Name)
			if err != nil {
				return nil, err
			}
			fields = append(fields, sub...)
		default:
			return nil, fmt.Errorf("unknown selection type: %T", x)
		}
	}
	return fields, nil
}
