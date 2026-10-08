package dagui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// SpanAttribute is one OTel attribute of a span, as the DB can still report
// it after ingestion.
//
// The DB keeps no copy of a span's raw attribute set: ProcessAttribute parses
// the attributes dagui understands into typed SpanSnapshot fields, and keeps
// every other one verbatim (JSON-encoded) in ExtraAttributes. Attributes()
// reads both back out, so inspecting a span costs nothing at ingest time and
// no memory beyond what rendering already holds — which matters for an
// imported trace of hundreds of thousands of spans.
type SpanAttribute struct {
	Key string
	// Value is JSON-encoded: strings quoted, lists as arrays. A parsed
	// attribute too large to be worth printing (the call payload) is
	// summarized instead.
	Value string
	// Parsed marks an attribute rebuilt from the typed field dagui parsed it
	// into, rather than kept verbatim. Its value is what dagui holds, which
	// for a few flags (canceled, left_running, passthrough, encapsulated) may
	// have been set by dagui itself rather than by the attribute.
	Parsed bool
}

// Text is the attribute's value as plain text: a string value unquoted, any
// other value as its JSON encoding.
func (attr SpanAttribute) Text() string {
	var s string
	if err := json.Unmarshal([]byte(attr.Value), &s); err == nil {
		return s
	}
	return attr.Value
}

// Attributes lists the span's attributes, sorted by key: the ones dagui
// parsed (Parsed set; zero values are indistinguishable from absent and
// left out) and every other attribute verbatim.
func (snapshot *SpanSnapshot) Attributes() []SpanAttribute {
	attrs := snapshot.parsedAttributes("")
	for key, val := range snapshot.ExtraAttributes {
		attrs = append(attrs, SpanAttribute{Key: key, Value: displayJSON(val)})
	}
	slices.SortFunc(attrs, func(a, b SpanAttribute) int {
		return strings.Compare(a.Key, b.Key)
	})
	return attrs
}

// Attribute looks up one attribute by key, as Attributes reports it, without
// building the whole list: it is what filtering every loaded span by an
// attribute runs per span.
func (snapshot *SpanSnapshot) Attribute(key string) (SpanAttribute, bool) {
	if raw, ok := snapshot.ExtraAttributes[key]; ok {
		return SpanAttribute{Key: key, Value: displayJSON(raw)}, true
	}
	if attrs := snapshot.parsedAttributes(key); len(attrs) > 0 {
		return attrs[0], true
	}
	return SpanAttribute{}, false
}

// encodeJSON encodes val without encoding/json's HTML escaping, which would
// print <, > and & as \u003c, \u003e and \u0026.
func encodeJSON(val any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(val); err != nil {
		return strconv.Quote(fmt.Sprint(val))
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// displayJSON undoes the HTML escaping ProcessAttribute's json.Marshal
// applied to a verbatim attribute, when there is any.
func displayJSON(raw json.RawMessage) string {
	if !bytes.Contains(raw, []byte(`\u00`)) {
		return string(raw)
	}
	var val any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep big integers exact
	if err := dec.Decode(&val); err != nil {
		return string(raw)
	}
	return encodeJSON(val)
}

// parsedAttributes rebuilds the attributes dagui parsed into typed fields;
// with only set, just that one (and nothing is encoded for the others).
func (snapshot *SpanSnapshot) parsedAttributes(only string) []SpanAttribute { //nolint: gocyclo
	var attrs []SpanAttribute
	parsed := func(key string, val any) {
		if only != "" && key != only {
			return
		}
		attrs = append(attrs, SpanAttribute{Key: key, Value: encodeJSON(val), Parsed: true})
	}
	str := func(key, val string) {
		if val != "" {
			parsed(key, val)
		}
	}
	flag := func(key string, val bool) {
		if val {
			parsed(key, true)
		}
	}
	strs := func(key string, val []string) {
		if len(val) > 0 {
			parsed(key, val)
		}
	}

	str(telemetry.DagDigestAttr, snapshot.CallDigest)
	if snapshot.CallPayload != "" && (only == "" || only == telemetry.DagCallAttr) {
		// Base64 protobuf: noise at any truncation. The console's /call
		// endpoint (and the call tools) decode it in full.
		parsed(telemetry.DagCallAttr, fmt.Sprintf(
			"<%d bytes of base64 call payload; decode it via the call digest>",
			len(snapshot.CallPayload)))
	}
	str(telemetry.DagCallScopeAttr, snapshot.CallScope)
	strs(telemetry.DagInputsAttr, snapshot.Inputs)
	str(telemetry.DagOutputAttr, snapshot.Output)
	str(telemetryattrs.UIResumeOutputAttr, snapshot.ResumeOutput)

	flag(telemetry.CachedAttr, snapshot.Cached)
	flag(telemetry.CanceledAttr, snapshot.Canceled)
	flag(telemetry.PendingAttr, snapshot.Pending)
	flag(telemetryattrs.DagBlockedAttr, snapshot.Blocked)
	flag(telemetryattrs.DagLeftRunningAttr, snapshot.LeftRunning)
	flag(telemetryattrs.DagPartialAttr, snapshot.Partial)

	flag(telemetry.UIEncapsulateAttr, snapshot.Encapsulate)
	flag(telemetry.UIEncapsulatedAttr, snapshot.Encapsulated)
	flag(telemetry.UIRevealAttr, snapshot.Reveal)
	flag(telemetry.UIBoundaryAttr, snapshot.Boundary)
	flag(telemetry.UIInternalAttr, snapshot.Internal)
	flag(telemetry.UIPassthroughAttr, snapshot.Passthrough)
	flag(telemetryattrs.UIImportedRootAttr, snapshot.ImportedRoot)
	flag(telemetry.UIRollUpLogsAttr, snapshot.RollUpLogs)
	flag(telemetry.UIRollUpSpansAttr, snapshot.RollUpSpans)
	str(telemetry.UIActorEmojiAttr, snapshot.ActorEmoji)
	str(telemetry.UIMessageAttr, snapshot.Message)
	str(telemetry.ContentTypeAttr, snapshot.ContentType)

	str(telemetry.CheckNameAttr, snapshot.CheckName)
	flag(telemetry.CheckPassedAttr, snapshot.CheckPassed)
	str(telemetry.GeneratorNameAttr, snapshot.GeneratorName)
	flag(telemetryattrs.GenerateSkippedAttr, snapshot.GenerateSkipped)
	flag(telemetryattrs.GenerateRegeneratedAttr, snapshot.GenerateRegenerated)

	flag(telemetryattrs.ServiceAttr, snapshot.Service)
	str(telemetryattrs.ServiceNameAttr, snapshot.ServiceName)
	strs(telemetryattrs.ServiceURLsAttr, snapshot.ServiceURLs)

	flag(telemetryattrs.AgentAttr, snapshot.Agent)
	str(telemetryattrs.AgentIDAttr, snapshot.AgentID)
	str(telemetryattrs.AgentNameAttr, snapshot.AgentName)
	str(telemetryattrs.AgentCallDigestAttr, snapshot.AgentCallDigest)
	str(telemetryattrs.AgentRewindFromDigestAttr, snapshot.AgentRewindFrom)
	str(telemetryattrs.AgentRewindToDigestAttr, snapshot.AgentRewindTo)

	str(telemetry.LLMRoleAttr, snapshot.LLMRole)
	flag("llm.thinking", snapshot.LLMThinking)
	str(telemetry.LLMToolAttr, snapshot.LLMTool)
	str(telemetry.LLMToolServerAttr, snapshot.LLMToolServer)
	strs(telemetry.LLMToolArgNamesAttr, snapshot.LLMToolArgNames)
	strs(telemetry.LLMToolArgValuesAttr, snapshot.LLMToolArgValues)
	str(telemetryattrs.LLMCallDigestAttr, snapshot.LLMCallDigest)
	str(telemetryattrs.LLMMessageOriginKindAttr, snapshot.LLMOriginKind)
	str(telemetryattrs.LLMMessageOriginAgentNameAttr, snapshot.LLMOriginAgentName)
	str(telemetryattrs.LLMMessageOriginRefAttr, snapshot.LLMOriginRef)
	str(telemetryattrs.LLMMessageOriginReplyToAttr, snapshot.LLMOriginReplyTo)
	if snapshot.LLMToolResultTokens != 0 {
		parsed(telemetryattrs.LLMToolResultTokensAttr, snapshot.LLMToolResultTokens)
	}

	str(string(semconv.TestCaseNameKey), snapshot.TestCaseName)
	str(string(semconv.TestSuiteNameKey), snapshot.TestSuiteName)
	if snapshot.TestStatus != "" {
		// Both status keys merge into one field; report it under the key
		// that matches the span's role.
		key := string(semconv.TestCaseResultStatusKey)
		if snapshot.TestCaseName == "" && snapshot.TestSuiteName != "" {
			key = string(semconv.TestSuiteRunStatusKey)
		}
		parsed(key, string(snapshot.TestStatus))
	}
	return attrs
}
