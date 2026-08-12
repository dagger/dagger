package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// PublishedSDKDependency is the immutable public crate source selected while the
// SDK content is built. Fields which do not belong to the selected source stay empty.
type PublishedSDKDependency struct {
	Source       string `json:"source"`
	Registry     string `json:"registry,omitempty"`
	Package      string `json:"package"`
	ExactVersion string `json:"exact_version,omitempty"`
	URL          string `json:"url,omitempty"`
	Revision     string `json:"revision,omitempty"`
}

// EngineSource is the exact packaged engine identity forwarded into Rust requests.
type EngineSource struct {
	FormatVersion               uint32                 `json:"format_version"`
	Repository                  string                 `json:"repository"`
	DaggerRevision              string                 `json:"dagger_revision"`
	EngineVersion               string                 `json:"engine_version"`
	RustSDKVersion              string                 `json:"rust_sdk_version"`
	RustToolchain               string                 `json:"rust_toolchain"`
	SDKDependency               PublishedSDKDependency `json:"sdk_dependency"`
	CoreSchemaDigest            string                 `json:"core_schema_digest"`
	PackagedAssetManifestDigest string                 `json:"packaged_asset_manifest_digest"`
}

// RuntimePolicy is the immutable image, target, and filesystem contract packaged
// beside the private Rust executable.
type RuntimePolicy struct {
	FormatVersion         uint32 `json:"format_version"`
	BuildImage            string `json:"build_image"`
	RuntimeBaseImage      string `json:"runtime_base_image"`
	RuntimeBaseDigest     string `json:"runtime_base_digest"`
	LinuxAMD64Target      string `json:"linux_amd64_target"`
	LinuxARM64Target      string `json:"linux_arm64_target"`
	CargoTargetDir        string `json:"cargo_target_dir"`
	RuntimeBinaryPath     string `json:"runtime_binary_path"`
	RuntimeInstallPath    string `json:"runtime_install_path"`
	ProvenanceInstallPath string `json:"provenance_install_path"`
}

// ExecutionResult is the closed projection returned by an operation execution.
type ExecutionResult struct {
	FormatVersion     uint32         `json:"format_version"`
	Kind              string         `json:"kind"`
	OutputRoot        string         `json:"output_root"`
	TouchedPaths      []string       `json:"touched_paths,omitempty"`
	OperationManifest *string        `json:"operation_manifest"`
	VCSGenerated      []string       `json:"vcs_generated"`
	VCSIgnored        []string       `json:"vcs_ignored"`
	ClientPlan        *ClientSetPlan `json:"client_plan,omitempty"`
}

// PlannedClient is one credential-free Rust-owned workspace selection result.
type PlannedClient struct {
	RecordIndex     uint32  `json:"record_index"`
	Path            string  `json:"path"`
	ModuleRefDigest string  `json:"module_ref_digest"`
	StoredPin       *string `json:"stored_pin,omitempty"`
}

// ClientSetPlan is the canonical path-ordered result of the Rust preflight.
type ClientSetPlan struct {
	FormatVersion uint32          `json:"format_version"`
	Cwd           string          `json:"cwd"`
	Clients       []PlannedClient `json:"clients"`
}

// EngineDiagnostic is the bounded, engine-authored error projection emitted by
// the private Rust executable. It never carries child-process output or source bytes.
type EngineDiagnostic struct {
	Code       string             `json:"code"`
	Coordinate string             `json:"coordinate,omitempty"`
	Message    string             `json:"message"`
	Causes     []EngineDiagnostic `json:"causes"`
}

// RuntimeBuildPlan retains only the adapter fields needed to execute the closed
// Cargo plan. Rust strictly revalidates the complete document during finalization.
type RuntimeBuildPlan struct {
	FormatVersion      uint32   `json:"format_version"`
	CargoArgs          []string `json:"cargo_args"`
	BinaryRelativePath string   `json:"binary_relative_path"`
}

// DecodeEngineSource verifies the canonical descriptor before Go projects any of
// its data into another Rust-owned control document.
func DecodeEngineSource(data []byte) (EngineSource, error) {
	var value EngineSource
	if err := decodeCanonical(data, &value); err != nil {
		return EngineSource{}, fmt.Errorf("decode engine source: %w", err)
	}
	if value.FormatVersion != 1 || value.SDKDependency.Package != "dagger-sdk" {
		return EngineSource{}, fmt.Errorf("engine source has an unsupported format or package")
	}
	return value, nil
}

// DecodeRuntimePolicy validates the closed adapter paths and immutable image pins.
func DecodeRuntimePolicy(data []byte) (RuntimePolicy, error) {
	var value RuntimePolicy
	if err := decodeCanonical(data, &value); err != nil {
		return RuntimePolicy{}, fmt.Errorf("decode runtime policy: %w", err)
	}
	if value.FormatVersion != 1 ||
		!strings.Contains(value.BuildImage, "@sha256:") ||
		!strings.Contains(value.RuntimeBaseImage, "@sha256:") ||
		value.CargoTargetDir != "/var/lib/dagger/rust/target" ||
		value.RuntimeBinaryPath != "/var/lib/dagger/rust/target/release/dagger-module" ||
		value.RuntimeInstallPath != "/usr/local/bin/dagger-module" ||
		value.ProvenanceInstallPath != "/usr/local/share/dagger/rust/runtime-provenance.json" {
		return RuntimePolicy{}, fmt.Errorf("runtime policy differs from the closed adapter contract")
	}
	return value, nil
}

// DecodeExecutionResult rejects an alternate result class or any unconfined path.
func DecodeExecutionResult(data []byte, expectedKind string) (ExecutionResult, error) {
	var value ExecutionResult
	if err := decodeCanonical(data, &value); err != nil {
		return ExecutionResult{}, fmt.Errorf("decode execution result: %w", err)
	}
	if value.FormatVersion != 1 || value.Kind != expectedKind || !isNormalizedRelativePath(value.OutputRoot) {
		return ExecutionResult{}, fmt.Errorf("execution result differs from the requested operation")
	}
	paths := append(append(append([]string{}, value.TouchedPaths...), value.VCSGenerated...), value.VCSIgnored...)
	for _, candidate := range paths {
		if !isNormalizedRelativePath(candidate) {
			return ExecutionResult{}, fmt.Errorf("execution result path %q is not confined", candidate)
		}
	}
	if expectedKind == "client-plan" {
		if value.ClientPlan == nil || value.OperationManifest != nil || len(value.TouchedPaths) != 0 ||
			value.ClientPlan.FormatVersion != 1 || value.ClientPlan.Cwd != value.OutputRoot {
			return ExecutionResult{}, fmt.Errorf("client plan result differs from the requested operation")
		}
		previous := ""
		indices := map[uint32]struct{}{}
		for _, client := range value.ClientPlan.Clients {
			if !isNormalizedRelativePath(client.Path) || !validSHA256(client.ModuleRefDigest) ||
				(previous != "" && previous >= client.Path) {
				return ExecutionResult{}, fmt.Errorf("client plan is not canonical and confined")
			}
			if _, exists := indices[client.RecordIndex]; exists {
				return ExecutionResult{}, fmt.Errorf("client plan contains duplicate record identity")
			}
			indices[client.RecordIndex] = struct{}{}
			previous = client.Path
		}
	} else if value.ClientPlan != nil {
		return ExecutionResult{}, fmt.Errorf("non-planning result contains a client plan")
	}
	return value, nil
}

// DecodeEngineDiagnostic accepts only the private engine's canonical bounded shape.
func DecodeEngineDiagnostic(data []byte) (EngineDiagnostic, error) {
	const maxDiagnosticBytes = 256 * 1024
	if len(data) == 0 || len(data) > maxDiagnosticBytes {
		return EngineDiagnostic{}, fmt.Errorf("Rust engine diagnostic is absent or exceeds its bound")
	}
	var diagnostic EngineDiagnostic
	if err := decodeCanonical(data, &diagnostic); err != nil {
		return EngineDiagnostic{}, fmt.Errorf("decode canonical Rust engine diagnostic: %w", err)
	}
	if err := validateEngineDiagnostic(diagnostic, 0); err != nil {
		return EngineDiagnostic{}, err
	}
	return diagnostic, nil
}

func validateEngineDiagnostic(diagnostic EngineDiagnostic, depth int) error {
	if depth > 8 || diagnostic.Code == "" || len(diagnostic.Code) > 64 ||
		diagnostic.Message == "" || len(diagnostic.Message) > 1024 ||
		len(diagnostic.Coordinate) > 1024 || len(diagnostic.Causes) > 32 {
		return fmt.Errorf("Rust engine diagnostic has an invalid bounded shape")
	}
	for _, character := range diagnostic.Code {
		if (character < 'A' || character > 'Z') && character != '_' && (character < '0' || character > '9') {
			return fmt.Errorf("Rust engine diagnostic code is not canonical")
		}
	}
	for _, marker := range []string{"https://", "http://", "Authorization:", "Bearer ", "token="} {
		if strings.Contains(diagnostic.Coordinate, marker) || strings.Contains(diagnostic.Message, marker) {
			return fmt.Errorf("Rust engine diagnostic contains a forbidden credential marker")
		}
	}
	for _, cause := range diagnostic.Causes {
		if err := validateEngineDiagnostic(cause, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// DecodeRuntimeBuildPlan admits only the Cargo vector authored by the Rust verifier.
func DecodeRuntimeBuildPlan(data []byte) (RuntimeBuildPlan, error) {
	var complete map[string]any
	if err := decodeCanonical(data, &complete); err != nil {
		return RuntimeBuildPlan{}, fmt.Errorf("decode runtime plan: %w", err)
	}
	var value RuntimeBuildPlan
	if err := json.Unmarshal(data, &value); err != nil {
		return RuntimeBuildPlan{}, fmt.Errorf("project runtime plan: %w", err)
	}
	if value.FormatVersion != 1 || value.BinaryRelativePath != "release/dagger-module" || !validCargoArgs(value.CargoArgs) {
		return RuntimeBuildPlan{}, fmt.Errorf("runtime plan differs from the closed Cargo contract")
	}
	return value, nil
}

// CanonicalJSON produces the one byte spelling accepted by the Rust control boundary.
func CanonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var tree any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	canonical, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(canonical, '\n'), nil
}

// DigestBytes returns the scalar spelling used for exact mounted schema bytes.
func DigestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

// DigestModuleSource binds an opaque engine source digest to its semantic role.
func DigestModuleSource(value string) string {
	digest := sha256.New()
	digest.Write([]byte("dagger-rust-module-source-v1\x00"))
	digest.Write([]byte(value))
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

// DigestModuleReference keeps raw user-facing refs out of Rust control documents.
func DigestModuleReference(value string) string {
	digest := sha256.New()
	digest.Write([]byte("dagger-rust-module-reference-v1\x00"))
	digest.Write([]byte(value))
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

// ModuleSourceFile is one normalized caller-owned leaf in semantic source identity.
type ModuleSourceFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// DigestModuleSourceFiles makes source enumeration order irrelevant while retaining
// every normalized file path and its metadata-free content digest.
func DigestModuleSourceFiles(files []ModuleSourceFile) (string, error) {
	normalized := append([]ModuleSourceFile(nil), files...)
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].Path < normalized[right].Path
	})
	for index, file := range normalized {
		if file.Path == "." || strings.HasSuffix(file.Path, "/") ||
			!isNormalizedRelativePath(file.Path) || file.Digest == "" || len(file.Digest) > 256 {
			return "", fmt.Errorf("semantic module source contains an invalid file record")
		}
		if index > 0 && normalized[index-1].Path == file.Path {
			return "", fmt.Errorf("semantic module source contains duplicate path %q", file.Path)
		}
	}
	encoded, err := CanonicalJSON(struct {
		FormatVersion uint32             `json:"format_version"`
		Files         []ModuleSourceFile `json:"files"`
	}{FormatVersion: 1, Files: normalized})
	if err != nil {
		return "", fmt.Errorf("encode semantic module source: %w", err)
	}
	return DigestModuleSource(string(encoded)), nil
}

// RebaseOperationPath gives the private Rust capability root one fixed first
// component while preserving the caller's normalized relative spelling.
func RebaseOperationPath(candidate string) (string, error) {
	candidate = strings.TrimPrefix(candidate, "./")
	if candidate == "" || candidate == "." {
		return "workspace", nil
	}
	if path.IsAbs(candidate) || path.Clean(candidate) != candidate || candidate == ".." ||
		strings.HasPrefix(candidate, "../") || strings.Contains(candidate, "\\") {
		return "", fmt.Errorf("path must be normalized and relative")
	}
	return path.Join("workspace", candidate), nil
}

// StripOperationRoot converts private operation paths back into caller-context paths.
func StripOperationRoot(paths []string) ([]string, error) {
	stripped := make([]string, 0, len(paths))
	for _, candidate := range paths {
		if candidate == "workspace" {
			return nil, fmt.Errorf("operation returned the workspace root as a VCS path")
		}
		value, ok := strings.CutPrefix(candidate, "workspace/")
		if !ok || value == "" {
			return nil, fmt.Errorf("operation returned an unscoped VCS path")
		}
		stripped = append(stripped, value)
	}
	return stripped, nil
}

func decodeCanonical(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	canonical, err := CanonicalJSON(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, data) {
		return fmt.Errorf("document is not canonical JSON")
	}
	return nil
}

func validCargoArgs(args []string) bool {
	if len(args) != 13 {
		return false
	}
	return args[0] == "build" && args[1] == "--manifest-path" && isNormalizedRelativePath(args[2]) &&
		args[3] == "--package" && args[4] != "" && args[5] == "--bin" && args[6] == "dagger-module" &&
		args[7] == "--release" && args[8] == "--locked" && args[9] == "--target" && args[10] != "" &&
		args[11] == "--target-dir" && args[12] == "/var/lib/dagger/rust/target"
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func normalizedOrDot(candidate string) bool {
	return candidate == "." || isNormalizedRelativePath(candidate)
}

func confinedJoin(root, suffix string) (string, bool) {
	joined := path.Join(root, suffix)
	return joined, normalizedOrDot(joined) && joined != ".." && !strings.HasPrefix(joined, "../")
}
