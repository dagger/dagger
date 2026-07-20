package core

import (
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/stretchr/testify/require"
)

// profileSkip is the static, debug-independent, receiver-type profiling-skip
// predicate (separate from the introspectionInfo UI decision). It is a pure
// function of (immediate receiver type name, field) — both in the call digest — so
// a call and any caller that singleflights it agree by construction.
func TestProfileSkipClassifier(t *testing.T) {
	t.Parallel()

	// Skipped: every field on a reflection type (accessors, builders, internal),
	// including the residual accessor class the receiver-type rule must catch that a
	// named-field list missed (Function.returnType, FunctionArg.typeDef,
	// ObjectTypeDef.fields/functions/constructor, FieldTypeDef.typeDef,
	// ListTypeDef.elementTypeDef, EnumTypeDef.members).
	skipped := []struct{ recv, field string }{
		{"Function", "args"}, {"Function", "returnType"}, {"Function", "withArg"},
		{"Function", "__withReturnType"}, {"Function", "sourceMap"},
		{"FunctionArg", "typeDef"}, {"FunctionArg", "__withTypeDef"},
		{"TypeDef", "asObject"}, {"TypeDef", "asList"}, {"TypeDef", "asInterface"},
		{"TypeDef", "asInput"}, {"TypeDef", "asScalar"}, {"TypeDef", "asEnum"},
		{"TypeDef", "withObject"}, {"TypeDef", "__withObjectTypeDef"},
		{"ObjectTypeDef", "fields"}, {"ObjectTypeDef", "functions"},
		{"ObjectTypeDef", "constructor"}, {"ObjectTypeDef", "__withFunction"},
		{"InterfaceTypeDef", "functions"}, {"InterfaceTypeDef", "__withFunction"},
		{"InputTypeDef", "fields"},
		{"FieldTypeDef", "typeDef"}, {"FieldTypeDef", "__withTypeDef"},
		{"ListTypeDef", "elementTypeDef"},
		{"ScalarTypeDef", "__withName"},
		{"EnumTypeDef", "members"}, {"EnumTypeDef", "values"},
		{"EnumMemberTypeDef", "__withName"},
		// The enum member type's LIVE schema name is the legacy "EnumValueTypeDef"
		// (its Go type is EnumMemberTypeDef); the call-site stamp carries the schema
		// name, so the predicate must skip it (found as a residual: the live schema
		// name and the Go type name diverge here).
		{"EnumValueTypeDef", "__withSourceMap"}, {"EnumValueTypeDef", "__withName"},
		// Query-level introspection root fields (the root set, shared with
		// introspectionInfo).
		{"Query", "__schema"}, {"Query", "sourceMap"}, {"Query", "currentTypeDefs"},
		{"Query", "function"}, {"Query", "typeDef"}, {"Query", "__objectTypeDef"},
		{"Query", "currentFunctionCall"}, {"Query", "currentModule"},
		// A receiver-less synthetic root frame still classifies by the root set.
		{"", "__schema"}, {"", "currentTypeDefs"},
	}
	for _, tc := range skipped {
		require.Truef(t, profileSkip(tc.recv, tc.field), "%s.%s should be skipped", tc.recv, tc.field)
	}

	// Profiled (NOT skipped): real user/engine work and the name traps. The slow
	// part of module load lives on these non-reflection receivers.
	profiled := []struct{ recv, field string }{
		{"Container", "withExec"}, {"Container", "from"}, {"Container", "stdout"},
		{"Query", "moduleSource"}, {"Query", "container"}, {"Query", "git"},
		{"Query", "version"}, // non-root field on Query
		{"ModuleSource", "asModule"}, {"Module", "serve"},
		{"Directory", "entries"}, {"File", "contents"},
		// NAME TRAP: FunctionCall is the active-call context with real DoNotCache
		// work (returnValue/returnError) — it is NOT a reflection type and must stay
		// profiled, even though its name starts with "Function".
		{"FunctionCall", "returnValue"}, {"FunctionCall", "returnError"},
		// SourceMap / FunctionCallArgValue are metadata-ish but deliberately NOT in
		// the reflection set, so their own fields stay profiled (avoids the
		// FunctionCall name-trap confusion; negligible residual).
		{"SourceMap", "line"}, {"SourceMap", "filename"},
		{"FunctionCallArgValue", "value"}, {"FunctionCallArgValue", "name"},
		// "" receiver with a non-root field is not skipped.
		{"", "container"},
	}
	for _, tc := range profiled {
		require.Falsef(t, profileSkip(tc.recv, tc.field), "%s.%s should be profiled", tc.recv, tc.field)
	}
}

// profileSkip takes no context, so it is structurally debug-independent: debug
// baggage cannot change the decision (unlike introspectionInfo's debug-gated
// receiver-type cases). This is what keeps the volume amplifier from returning in
// the very mode used to capture a trace for debugging.
func TestProfileSkipIsDebugIndependentPureFunction(t *testing.T) {
	t.Parallel()
	require.Equal(t, profileSkip("Function", "args"), profileSkip("Function", "args"))
	require.True(t, profileSkip("ObjectTypeDef", "functions"))
	require.False(t, profileSkip("Container", "withExec"))
}

// The root set extracted for profileSkip must exactly match the names
// introspectionInfo classifies as roots, so the UI and profiler never diverge.
func TestIntrospectionRootFieldsMatchProfileSkip(t *testing.T) {
	t.Parallel()
	for field := range introspectionRootFields {
		require.Truef(t, profileSkip("Query", field), "root %q must be profile-skipped on Query", field)
		require.Truef(t, profileSkip("", field), "root %q must be profile-skipped on a receiver-less root", field)
	}
}

// AroundFunc must stamp ProfileSkip onto the call frame BEFORE the IsSkipped early
// return, so an inherited-skip descendant (under a typedef-loading hideCtx) is
// classified by its OWN recipe: reflection-receiver work is skipped, real shared
// work (clone / dep-load, non-reflection receiver) stays profiled. This is the
// load-bearing property for not coarsening real work hidden under introspection.
func TestAroundFuncStampsProfileSkipBeforeInheritedSkip(t *testing.T) {
	t.Parallel()

	// Reflection-receiver call under an inherited hideCtx → skipped, stamped despite
	// the early IsSkipped return.
	refReq := &dagql.CallRequest{
		ResultCall:       testResultCall("args", dagql.String(""), nil),
		ReceiverTypeName: "Function",
	}
	hideCtx := dagql.WithSkip(t.Context())
	AroundFunc(hideCtx, refReq)
	require.True(t, refReq.ResultCall.ProfileSkip, "reflection work under hideCtx must be profile-skipped")

	// Real shared work under the SAME inherited hideCtx → NOT skipped (stays
	// profiled); this is the over-cut guard.
	realReq := &dagql.CallRequest{
		ResultCall:       testResultCall("withExec", dagql.String(""), nil),
		ReceiverTypeName: "Container",
	}
	AroundFunc(hideCtx, realReq)
	require.False(t, realReq.ResultCall.ProfileSkip, "real work under hideCtx must stay profiled")

	// And without any inherited skip, a normal user call stays profiled.
	plainReq := &dagql.CallRequest{
		ResultCall:       testResultCall("withExec", dagql.String(""), nil),
		ReceiverTypeName: "Container",
	}
	AroundFunc(t.Context(), plainReq)
	require.False(t, plainReq.ResultCall.ProfileSkip)
}
