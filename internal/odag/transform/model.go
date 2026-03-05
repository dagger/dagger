package transform

type TraceProjection struct {
	TraceID       string          `json:"traceID"`
	StartUnixNano int64           `json:"startUnixNano"`
	EndUnixNano   int64           `json:"endUnixNano"`
	Objects       []ObjectNode    `json:"objects"`
	Edges         []ObjectEdge    `json:"edges"`
	Events        []MutationEvent `json:"events"`
	Warnings      []string        `json:"warnings,omitempty"`
}

type ObjectNode struct {
	ID                string        `json:"id"`
	TypeName          string        `json:"typeName"`
	Alias             string        `json:"alias"`
	FirstSeenUnixNano int64         `json:"firstSeenUnixNano"`
	LastSeenUnixNano  int64         `json:"lastSeenUnixNano"`
	ReferencedByTop   bool          `json:"referencedByTop"`
	MissingState      bool          `json:"missingState"`
	StateHistory      []ObjectState `json:"stateHistory"`
}

type ObjectState struct {
	StateDigest     string         `json:"stateDigest"`
	CallDigest      string         `json:"callDigest"`
	SpanID          string         `json:"spanID"`
	StartUnixNano   int64          `json:"startUnixNano"`
	EndUnixNano     int64          `json:"endUnixNano"`
	StatusCode      string         `json:"statusCode"`
	ReceiverState   string         `json:"receiverState,omitempty"`
	OutputStateJSON map[string]any `json:"outputState,omitempty"`
}

type ObjectEdge struct {
	FromObjectID  string `json:"fromObjectID"`
	ToObjectID    string `json:"toObjectID"`
	Kind          string `json:"kind"`
	Label         string `json:"label"`
	EvidenceCount int    `json:"evidenceCount"`
}

type MutationEvent struct {
	Index               int        `json:"index"`
	TraceID             string     `json:"traceID"`
	SpanID              string     `json:"spanID"`
	ParentSpanID        string     `json:"parentSpanID,omitempty"`
	StartUnixNano       int64      `json:"startUnixNano"`
	EndUnixNano         int64      `json:"endUnixNano"`
	StatusCode          string     `json:"statusCode"`
	StatusMessage       string     `json:"statusMessage,omitempty"`
	Name                string     `json:"name"`
	CallDigest          string     `json:"callDigest,omitempty"`
	ReceiverStateDigest string     `json:"receiverStateDigest,omitempty"`
	OutputStateDigest   string     `json:"outputStateDigest,omitempty"`
	ReturnType          string     `json:"returnType,omitempty"`
	TopLevel            bool       `json:"topLevel"`
	Internal            bool       `json:"internal,omitempty"`
	Kind                string     `json:"kind"` // create, mutate, call
	ObjectID            string     `json:"objectID,omitempty"`
	MissingOutputState  bool       `json:"missingOutputState"`
	Inputs              []InputRef `json:"inputs,omitempty"`
}

type InputRef struct {
	Name        string `json:"name"`
	Path        string `json:"path,omitempty"`
	StateDigest string `json:"stateDigest"`
}

type Snapshot struct {
	TraceID        string          `json:"traceID"`
	UnixNano       int64           `json:"unixNano"`
	Objects        []ObjectNode    `json:"objects"`
	Edges          []ObjectEdge    `json:"edges"`
	Events         []MutationEvent `json:"events"`
	ActiveEventIDs []string        `json:"activeEventIDs,omitempty"`
}
