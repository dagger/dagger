package telemetryattrs

const (
	WorkspaceInputsAttr                     = "dagger.io/workspace.inputs"
	WorkspaceInputsIssuesAttr               = "dagger.io/workspace.inputs.issues"
	WorkspaceInputsCompleteAttr             = "dagger.io/workspace.inputs.complete"
	WorkspaceInputsCapabilitiesCompleteAttr = "dagger.io/workspace.inputs.capabilities_complete"
	WorkspaceInputsErrorAttr                = "dagger.io/workspace.inputs.error"
	WorkspaceInputsTruncatedAttr            = "dagger.io/workspace.inputs.truncated"
	WorkspaceInputsOmittedAttr              = "dagger.io/workspace.inputs.omitted"
)

// WorkspaceInputRecord is bounded per-call evidence, not a check-selection
// decision. Read includes nested queries; input carries prepared origins;
// retained describes future Workspace access, not a completed file read.
type WorkspaceInputRecord struct {
	Role         string   `json:"role"`
	Workspace    string   `json:"workspace"`
	Path         string   `json:"path,omitempty"`
	Include      []string `json:"include,omitempty"`
	Exclude      []string `json:"exclude,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	Origin       string   `json:"origin,omitempty"`
	Conservative bool     `json:"conservative,omitempty"`
	Input        []string `json:"input,omitempty"`
	Output       []string `json:"output,omitempty"`
}
