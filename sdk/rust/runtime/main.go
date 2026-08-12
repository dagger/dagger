// Package main exposes the module-backed ABI adapter for the built-in Rust SDK.
package main

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"rust-sdk/internal/dagger"
	"rust-sdk/internal/metadata"
)

const (
	sdkRuntimePath          = "runtime"
	engineToolPath          = "dist/dagger-rust-engine"
	formatterToolPath       = "dist/rustfmt"
	engineDescriptorPath    = "dist/engine-source.json"
	clientGenerationPath    = "dist/client-generation.json"
	runtimePolicyPath       = "dist/runtime-policy.json"
	operationRequestPath    = "/var/lib/dagger/rust/request.json"
	operationSchemaPath     = "/var/lib/dagger/rust/schema.json"
	operationProjectPath    = "/var/lib/dagger/rust/project"
	operationResultPath     = "/var/lib/dagger/rust/result.json"
	runtimePolicyMountPath  = "/usr/local/share/dagger/rust/runtime-policy.json"
	runtimePlanPath         = "/var/lib/dagger/rust/runtime-plan.json"
	runtimeProvenancePath   = "/var/lib/dagger/rust/runtime-provenance.json"
	runtimeDiagnosticPath   = "/var/lib/dagger/rust/runtime-diagnostic.json"
	operationDescriptorPath = "/usr/local/share/dagger/rust/engine-source.json"
	operationToolPath       = "/usr/local/bin/dagger-rust-engine"
	operationWorkspaceRoot  = "workspace"
)

// RustSDK is immutable packaged state. Operation methods build fresh Dagger object
// graphs from this source instead of retaining mutable container or project handles.
type RustSDK struct {
	SDKSourceDir *dagger.Directory // +private
}

type managedClientPlan struct {
	RecordIndex uint32
	Path        string
	ModuleRef   string
	StoredPin   string
	Source      *dagger.ModuleSource
}

// New constructs the built-in adapter from the complete packaged SDK content root.
func New(
	// Complete engine-packaged Rust SDK content, including runtime and dist metadata.
	// +optional
	// +defaultPath="/"
	sdkSourceDir *dagger.Directory,
) (*RustSDK, error) {
	if sdkSourceDir == nil {
		return nil, fmt.Errorf("sdk source directory not provided")
	}
	return &RustSDK{SDKSourceDir: sdkSourceDir}, nil
}

// GenerateModules regenerates every managed Rust module at or below the
// workspace's current location. Each module sees its freshly generated local
// dependencies, but only its own changes survive in the returned changeset.
//
// +generate
func (sdk *RustSDK) GenerateModules(ctx context.Context, ws *dagger.Workspace) (*dagger.Changeset, error) {
	cwd, err := workspaceCwd(ctx, ws)
	if err != nil {
		return nil, err
	}
	modules, err := dag.CurrentModule().AsSDK(dagger.CurrentModuleAsSDKOpts{Workspace: ws}).Modules(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover Rust modules: %w", err)
	}

	generated := make([]*dagger.Changeset, 0, len(modules))
	for _, module := range modules {
		modulePath, err := module.Path(ctx)
		if err != nil {
			return nil, fmt.Errorf("read managed Rust module path: %w", err)
		}
		_, visible := relativeToWorkspaceCwd(cwd, modulePath)
		if !visible {
			continue
		}

		absolute := "/" + normalizeWorkspacePath(modulePath)
		unstaged := ws.ModuleSource(absolute)
		staged := ws.WithChanges(unstaged.GenerateLocalDependencies(ws))
		changes := staged.ModuleSource(absolute).GeneratedContextChangeset()
		// GeneratedContextChangeset is context-rooted, while generator changes are
		// exported from the workspace cwd. Strip that prefix before returning or a
		// generator invoked inside a nested module duplicates the module path.
		generated = append(generated, scopedWorkspaceChanges(cwd, changes.Before(), changes.After()))
	}

	return dag.Changeset().WithChangesets(generated), nil
}

// GenerateClients regenerates every managed Rust client at or below the
// workspace's current location. Client schemas remain bound to their resolved
// module sources while output is confined to the registered workspace path.
//
// +generate
func (sdk *RustSDK) GenerateClients(ctx context.Context, ws *dagger.Workspace) (*dagger.Changeset, error) {
	cwd, err := workspaceCwd(ctx, ws)
	if err != nil {
		return nil, err
	}
	clients, err := dag.CurrentModule().AsSDK(dagger.CurrentModuleAsSDKOpts{Workspace: ws}).Clients(ctx)
	if err != nil {
		return nil, fmt.Errorf("discover Rust clients: %w", err)
	}

	workspaceBefore := ws.Directory("/")
	records := make([]managedClientPlan, 0, len(clients))
	for index, client := range clients {
		clientPath, err := client.Path(ctx)
		if err != nil {
			return nil, fmt.Errorf("read managed Rust client path: %w", err)
		}
		if !isConfinedWorkspacePath(clientPath) {
			return nil, fmt.Errorf("managed Rust client path is not confined")
		}
		moduleRef, err := client.Module(ctx)
		if err != nil {
			return nil, fmt.Errorf("read managed Rust client module reference: %w", err)
		}
		if err := validateModuleReference(moduleRef); err != nil {
			return nil, err
		}
		storedPin, err := client.Pin(ctx)
		if err != nil {
			return nil, fmt.Errorf("read managed Rust client pin: %w", err)
		}
		if err := validateOptionalRevision(storedPin); err != nil {
			return nil, fmt.Errorf("managed Rust client pin is invalid")
		}
		records = append(records, managedClientPlan{
			RecordIndex: uint32(index),
			Path:        normalizeWorkspacePath(clientPath),
			ModuleRef:   moduleRef,
			StoredPin:   storedPin,
			Source:      client.ModuleSource(),
		})
	}

	selected, err := sdk.planClientSet(ctx, cwd, records, workspaceBefore)
	if err != nil {
		return nil, err
	}
	generated := make([]*dagger.Changeset, 0, len(selected))
	for _, selectedClient := range selected {
		if int(selectedClient.RecordIndex) >= len(records) {
			return nil, fmt.Errorf("Rust client preflight returned an unknown record")
		}
		record := records[selectedClient.RecordIndex]
		rebasedRecordPath, err := rebasePath(record.Path)
		if err != nil {
			return nil, fmt.Errorf("managed Rust client path: %w", err)
		}
		if rebasedRecordPath != selectedClient.Path || metadata.DigestModuleReference(record.ModuleRef) != selectedClient.ModuleRefDigest || optionalString(selectedClient.StoredPin) != record.StoredPin {
			return nil, fmt.Errorf("Rust client preflight identity differs from the workspace record")
		}
		identity, err := moduleIdentity(ctx, record.Source, "current")
		if err != nil {
			return nil, fmt.Errorf("resolve client module source: %w", err)
		}
		if record.StoredPin != identity.ResolvedPin {
			return nil, fmt.Errorf("stored client pin differs from the resolved module revision")
		}
		schema := record.Source.ClientSchemaIntrospectionJSON()
		outputRoot, err := rebasePath(record.Path)
		if err != nil {
			return nil, fmt.Errorf("client output directory: %w", err)
		}
		request, _, err := sdk.generationRequest(ctx, "generate-client", identity, schema, outputRoot)
		if err != nil {
			return nil, err
		}
		execution, err := sdk.executeOperation(ctx, request, schema, workspaceBefore, "generation")
		if err != nil {
			return nil, err
		}

		workspaceAfter := execution.project.Directory(operationWorkspaceRoot)
		generated = append(generated, scopedWorkspaceChanges(cwd, workspaceBefore, workspaceAfter))
	}

	return dag.Changeset().WithChangesets(generated), nil
}

func (sdk *RustSDK) planClientSet(
	ctx context.Context,
	cwd string,
	records []managedClientPlan,
	workspaceBefore *dagger.Directory,
) ([]metadata.PlannedClient, error) {
	rebasedCwd, err := rebasePath(cwd)
	if err != nil {
		return nil, fmt.Errorf("workspace cwd: %w", err)
	}
	clients := make([]map[string]any, 0, len(records))
	for _, record := range records {
		rebasedPath, err := rebasePath(record.Path)
		if err != nil {
			return nil, fmt.Errorf("managed Rust client path: %w", err)
		}
		client := map[string]any{
			"record_index":      record.RecordIndex,
			"path":              rebasedPath,
			"module_ref_digest": metadata.DigestModuleReference(record.ModuleRef),
		}
		if record.StoredPin != "" {
			client["stored_pin"] = record.StoredPin
		}
		clients = append(clients, client)
	}
	request := map[string]any{
		"request_kind": "plan-client-set",
		"request": map[string]any{
			"format_version": 1,
			"cwd":            rebasedCwd,
			"clients":        clients,
		},
	}
	execution, err := sdk.executeOperation(ctx, request, nil, workspaceBefore, "client-plan")
	if err != nil {
		return nil, err
	}
	if execution.result.ClientPlan == nil {
		return nil, fmt.Errorf("Rust client preflight returned no plan")
	}
	return execution.result.ClientPlan.Clients, nil
}

func workspaceCwd(ctx context.Context, ws *dagger.Workspace) (string, error) {
	if ws == nil {
		return "", fmt.Errorf("workspace not provided")
	}
	cwd, err := ws.Cwd(ctx)
	if err != nil {
		return "", fmt.Errorf("read workspace cwd: %w", err)
	}
	return normalizeWorkspacePath(cwd), nil
}

func normalizeWorkspacePath(candidate string) string {
	trimmed := strings.Trim(candidate, "/")
	if trimmed == "" {
		return "."
	}
	return path.Clean(trimmed)
}

// relativeToWorkspaceCwd rejects siblings and ancestors because the engine
// re-roots generator output beneath the current workspace location.
func relativeToWorkspaceCwd(cwd string, target string) (string, bool) {
	if !isConfinedWorkspacePath(cwd) || !isConfinedWorkspacePath(target) {
		return "", false
	}
	cwd = normalizeWorkspacePath(cwd)
	target = normalizeWorkspacePath(target)
	if cwd == "." {
		return target, true
	}
	if target == cwd {
		return ".", true
	}
	if strings.HasPrefix(target, cwd+"/") {
		return strings.TrimPrefix(target, cwd+"/"), true
	}
	return "", false
}

func isConfinedWorkspacePath(candidate string) bool {
	if path.IsAbs(candidate) {
		return false
	}
	normalized := normalizeWorkspacePath(candidate)
	return normalized != ".." && !strings.HasPrefix(normalized, "../") && !strings.Contains(candidate, "\\")
}

func scopedWorkspaceChanges(cwd string, before *dagger.Directory, after *dagger.Directory) *dagger.Changeset {
	if cwd == "." {
		return after.Changes(before)
	}
	return after.Directory(cwd).Changes(before.Directory(cwd))
}

// engineDescriptor returns the opaque Rust-owned descriptor. Go forwards these bytes
// to the packaged tool and deliberately does not reinterpret dependency provenance.
func (sdk *RustSDK) engineDescriptor() *dagger.File {
	return sdk.SDKSourceDir.File(engineDescriptorPath)
}

func (sdk *RustSDK) engineTool() *dagger.File {
	return sdk.SDKSourceDir.File(engineToolPath)
}

func (sdk *RustSDK) clientGenerationMetadata(ctx context.Context) (metadata.ClientGeneration, error) {
	contents, err := sdk.SDKSourceDir.File(clientGenerationPath).Contents(ctx)
	if err != nil {
		return metadata.ClientGeneration{}, fmt.Errorf("read packaged client-generation metadata: %w", err)
	}
	return metadata.DecodeClientGeneration([]byte(contents))
}

// InitModule returns only SDK-owned project amendments. The engine independently
// authors module configuration and decides whether scoped generation follows.
func (sdk *RustSDK) InitModule(
	ctx context.Context,
	ws *dagger.Workspace,
	name string,
	path string,
) (*dagger.Changeset, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace not provided")
	}
	moduleRoot, err := rebasePath(path)
	if err != nil {
		return nil, fmt.Errorf("module path: %w", err)
	}
	descriptor, err := sdk.descriptorMetadata(ctx)
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"request_kind": "initialize-module",
		"request": map[string]any{
			"format_version": 1,
			"target":         targetIdentity(descriptor),
			"module": map[string]any{
				"name":           name,
				"original_name":  name,
				"source_subpath": moduleRoot,
				"config_format":  "current",
				"source_digest":  metadata.DigestModuleSource("initialization:" + path),
			},
			"package_name":   name,
			"sdk_dependency": descriptor.SDKDependency,
		},
	}
	before := ws.Directory("/")
	execution, err := sdk.executeOperation(ctx, request, nil, before, "initialization")
	if err != nil {
		return nil, err
	}
	return execution.project.Directory(operationWorkspaceRoot).Changes(before), nil
}

// InitClient validates the engine-owned record inputs and returns only SDK-owned
// scaffold changes. The module reference is never forwarded into Rust or generated
// content; the engine remains responsible for persisting and resolving that record.
func (sdk *RustSDK) InitClient(
	ctx context.Context,
	ws *dagger.Workspace,
	path string,
	module string,
) (*dagger.Changeset, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace not provided")
	}
	if err := validateModuleReference(module); err != nil {
		return nil, err
	}
	clientRoot, err := rebasePath(path)
	if err != nil {
		return nil, fmt.Errorf("client path: %w", err)
	}
	packageName, err := clientPackageName(path)
	if err != nil {
		return nil, err
	}
	descriptor, err := sdk.descriptorMetadata(ctx)
	if err != nil {
		return nil, err
	}
	request := clientInitializationRequestDocument(descriptor, clientRoot, packageName)
	before := ws.Directory("/")
	execution, err := sdk.executeOperation(ctx, request, nil, before, "client-initialization")
	if err != nil {
		return nil, err
	}
	return execution.project.Directory(operationWorkspaceRoot).Changes(before), nil
}

func clientInitializationRequestDocument(
	descriptor metadata.EngineSource,
	clientRoot string,
	packageName string,
) map[string]any {
	return map[string]any{
		"request_kind": "initialize-client",
		"request": map[string]any{
			"format_version": 1,
			"target":         targetIdentity(descriptor),
			"client_root":    clientRoot,
			"package_name":   packageName,
			"sdk_dependency": descriptor.SDKDependency,
		},
	}
}

// Codegen compiles the engine-visible schema into the scoped module context and
// returns the Rust-owned VCS policy emitted by the same operation plan.
func (sdk *RustSDK) Codegen(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
) (*dagger.GeneratedCode, error) {
	if introspectionJSON == nil {
		return nil, fmt.Errorf("introspection JSON not provided")
	}
	identity, err := moduleIdentity(ctx, modSource, "current")
	if err != nil {
		return nil, err
	}
	request, _, err := sdk.generationRequest(ctx, "generate-module", identity, introspectionJSON, identity.SourceSubpath)
	if err != nil {
		return nil, err
	}
	execution, err := sdk.executeOperation(ctx, request, introspectionJSON, modSource.ContextDirectory(), "generation")
	if err != nil {
		return nil, err
	}
	generated, err := stripWorkspacePaths(execution.result.VCSGenerated)
	if err != nil {
		return nil, err
	}
	ignored, err := stripWorkspacePaths(execution.result.VCSIgnored)
	if err != nil {
		return nil, err
	}
	return dag.GeneratedCode(execution.project.Directory(operationWorkspaceRoot)).
		WithVCSGeneratedPaths(generated).
		WithVCSIgnoredPaths(ignored), nil
}

// RequiredClientGenerationFiles returns the renderer-owned finite host input set.
func (sdk *RustSDK) RequiredClientGenerationFiles(ctx context.Context) ([]string, error) {
	metadata, err := sdk.clientGenerationMetadata(ctx)
	if err != nil {
		return nil, err
	}
	return metadata.RequiredHostFiles, nil
}

// GenerateClient renders the standalone client only within the requested output
// subtree of the scoped module context.
func (sdk *RustSDK) GenerateClient(
	ctx context.Context,
	modSource *dagger.ModuleSource,
	introspectionJSON *dagger.File,
	outputDir string,
) (*dagger.Directory, error) {
	if introspectionJSON == nil {
		return nil, fmt.Errorf("introspection JSON not provided")
	}
	identity, err := moduleIdentity(ctx, modSource, "current")
	if err != nil {
		return nil, err
	}
	outputRoot, err := rebasePath(outputDir)
	if err != nil {
		return nil, fmt.Errorf("client output directory: %w", err)
	}
	request, _, err := sdk.generationRequest(ctx, "generate-client", identity, introspectionJSON, outputRoot)
	if err != nil {
		return nil, err
	}
	execution, err := sdk.executeOperation(ctx, request, introspectionJSON, modSource.ContextDirectory(), "generation")
	if err != nil {
		return nil, err
	}
	return execution.project.Directory(operationWorkspaceRoot), nil
}

type scopedModuleIdentity struct {
	Name          string
	OriginalName  string
	SourceSubpath string
	SourceDigest  string
	ConfigFormat  string
	ResolvedPin   string
}

type operationExecution struct {
	project *dagger.Directory
	result  metadata.ExecutionResult
}

func (sdk *RustSDK) executeOperation(
	ctx context.Context,
	request any,
	schema *dagger.File,
	project *dagger.Directory,
	expectedKind string,
) (operationExecution, error) {
	requestBytes, err := metadata.CanonicalJSON(request)
	if err != nil {
		return operationExecution{}, fmt.Errorf("encode Rust operation request: %w", err)
	}
	policy, err := sdk.runtimePolicyMetadata(ctx)
	if err != nil {
		return operationExecution{}, err
	}
	descriptor, err := sdk.descriptorMetadata(ctx)
	if err != nil {
		return operationExecution{}, err
	}
	rustTarget, err := runtimeTarget(ctx, policy)
	if err != nil {
		return operationExecution{}, err
	}
	formatterInstallPath := path.Join(
		"/usr/local/rustup/toolchains",
		descriptor.RustToolchain+"-"+rustTarget,
		"bin/rustfmt",
	)
	container := dag.Container().From(policy.BuildImage).
		WithFile(operationToolPath, sdk.engineTool(), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		// rustfmt resolves private toolchain libraries relative to its executable.
		// Retaining the rustup layout keeps that lookup valid without an ambient proxy.
		WithFile(formatterInstallPath, sdk.SDKSourceDir.File(formatterToolPath), dagger.ContainerWithFileOpts{Permissions: 0o755}).
		WithFile(operationDescriptorPath, sdk.engineDescriptor()).
		WithDirectory(operationProjectPath, dag.Directory().WithDirectory(operationWorkspaceRoot, project)).
		WithNewFile(operationRequestPath, string(requestBytes))
	args := []string{
		operationToolPath, "execute",
		"--request", operationRequestPath,
		"--descriptor", operationDescriptorPath,
		"--project", operationProjectPath,
		"--result", operationResultPath,
	}
	if schema != nil {
		container = container.WithFile(operationSchemaPath, schema)
		args = append(args, "--schema", operationSchemaPath)
	}
	executed, err := container.WithExec(args).Sync(ctx)
	if err != nil {
		return operationExecution{}, fmt.Errorf("execute Rust SDK operation: %w", err)
	}
	resultBytes, err := executed.File(operationResultPath).Contents(ctx)
	if err != nil {
		return operationExecution{}, fmt.Errorf("read Rust SDK operation result: %w", err)
	}
	result, err := metadata.DecodeExecutionResult([]byte(resultBytes), expectedKind)
	if err != nil {
		return operationExecution{}, err
	}
	return operationExecution{project: executed.Directory(operationProjectPath), result: result}, nil
}

func (sdk *RustSDK) generationRequest(
	ctx context.Context,
	operation string,
	module scopedModuleIdentity,
	schema *dagger.File,
	outputRoot string,
) (map[string]any, []byte, error) {
	descriptor, err := sdk.descriptorMetadata(ctx)
	if err != nil {
		return nil, nil, err
	}
	contents, err := schema.Contents(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("read visible schema: %w", err)
	}
	schemaBytes := []byte(contents)
	return generationRequestDocument(descriptor, operation, module, schemaBytes, outputRoot), schemaBytes, nil
}

// generationRequestDocument is deliberately data-only: Rust owns source discovery,
// descriptor construction, registration, codecs, dispatch, and evidence semantics.
func generationRequestDocument(
	descriptor metadata.EngineSource,
	operation string,
	module scopedModuleIdentity,
	schemaBytes []byte,
	outputRoot string,
) map[string]any {
	moduleDocument := map[string]any{
		"name":           module.Name,
		"original_name":  module.OriginalName,
		"source_subpath": module.SourceSubpath,
		"config_format":  module.ConfigFormat,
		"source_digest":  module.SourceDigest,
	}
	if module.ResolvedPin != "" {
		moduleDocument["resolved_pin"] = module.ResolvedPin
	}
	return map[string]any{
		"request_kind": "generate",
		"request": map[string]any{
			"format_version": 1,
			"operation":      operation,
			"target":         targetIdentity(descriptor),
			"visible_schema": map[string]any{
				"path":   "schema.json",
				"digest": metadata.DigestBytes(schemaBytes),
			},
			"module":         moduleDocument,
			"sdk_dependency": descriptor.SDKDependency,
			"output_root":    outputRoot,
		},
	}
}

func (sdk *RustSDK) descriptorMetadata(ctx context.Context) (metadata.EngineSource, error) {
	contents, err := sdk.engineDescriptor().Contents(ctx)
	if err != nil {
		return metadata.EngineSource{}, fmt.Errorf("read packaged engine descriptor: %w", err)
	}
	return metadata.DecodeEngineSource([]byte(contents))
}

func (sdk *RustSDK) runtimePolicyMetadata(ctx context.Context) (metadata.RuntimePolicy, error) {
	contents, err := sdk.SDKSourceDir.File(runtimePolicyPath).Contents(ctx)
	if err != nil {
		return metadata.RuntimePolicy{}, fmt.Errorf("read packaged runtime policy: %w", err)
	}
	return metadata.DecodeRuntimePolicy([]byte(contents))
}

func targetIdentity(descriptor metadata.EngineSource) map[string]any {
	return map[string]any{
		"format_version":     descriptor.FormatVersion,
		"repository":         descriptor.Repository,
		"dagger_revision":    descriptor.DaggerRevision,
		"engine_version":     descriptor.EngineVersion,
		"rust_sdk_version":   descriptor.RustSDKVersion,
		"rust_toolchain":     descriptor.RustToolchain,
		"core_schema_digest": descriptor.CoreSchemaDigest,
	}
}

func moduleIdentity(ctx context.Context, source *dagger.ModuleSource, configFormat string) (scopedModuleIdentity, error) {
	if source == nil {
		return scopedModuleIdentity{}, fmt.Errorf("module source not provided")
	}
	name, err := source.ModuleName(ctx)
	if err != nil {
		return scopedModuleIdentity{}, fmt.Errorf("read module name: %w", err)
	}
	originalName, err := source.ModuleOriginalName(ctx)
	if err != nil {
		return scopedModuleIdentity{}, fmt.Errorf("read original module name: %w", err)
	}
	subpath, err := source.SourceSubpath(ctx)
	if err != nil {
		return scopedModuleIdentity{}, fmt.Errorf("read module source subpath: %w", err)
	}
	rebased, err := rebasePath(subpath)
	if err != nil {
		return scopedModuleIdentity{}, fmt.Errorf("module source subpath: %w", err)
	}
	// Dagger's directory digest retains empty parent directories created solely by
	// generated paths. Hashing a canonical leaf-file inventory keeps every authored
	// input while preventing that incidental scaffolding from making a fresh manifest
	// stale. The operation manifest separately authenticates the excluded products.
	digest, err := semanticModuleSourceDigest(ctx, source, subpath)
	if err != nil {
		return scopedModuleIdentity{}, fmt.Errorf("read semantic module source digest: %w", err)
	}
	kind, err := source.Kind(ctx)
	if err != nil {
		return scopedModuleIdentity{}, fmt.Errorf("read module source kind: %w", err)
	}
	resolvedPin := ""
	if kind == dagger.ModuleSourceKindGit {
		resolvedPin, err = source.Pin(ctx)
		if err != nil {
			return scopedModuleIdentity{}, fmt.Errorf("read resolved module pin: %w", err)
		}
		if err := validateOptionalRevision(resolvedPin); err != nil || resolvedPin == "" {
			return scopedModuleIdentity{}, fmt.Errorf("resolved remote module pin is invalid")
		}
	} else if kind != dagger.ModuleSourceKindLocal {
		return scopedModuleIdentity{}, fmt.Errorf("module source kind is unsupported")
	}
	return scopedModuleIdentity{
		Name: name, OriginalName: originalName, SourceSubpath: rebased,
		SourceDigest: digest, ConfigFormat: configFormat, ResolvedPin: resolvedPin,
	}, nil
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validateOptionalRevision(value string) error {
	if value == "" {
		return nil
	}
	if len(value) != 40 {
		return fmt.Errorf("revision must be a full lowercase commit")
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return fmt.Errorf("revision must be a full lowercase commit")
		}
	}
	return nil
}

func validateModuleReference(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("client module reference is empty or malformed")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("client module reference is empty or malformed")
		}
	}
	return nil
}

func clientPackageName(clientPath string) (string, error) {
	normalized := normalizeWorkspacePath(clientPath)
	if !isConfinedWorkspacePath(clientPath) {
		return "", fmt.Errorf("client path is not confined")
	}
	name := path.Base(normalized)
	if name == "." {
		name = "dagger-client"
	}
	name = strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	if name == "" || name[0] == '-' || name[len(name)-1] == '-' {
		return "", fmt.Errorf("client path does not yield a valid Cargo package name")
	}
	for _, character := range name {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return "", fmt.Errorf("client path does not yield a valid Cargo package name")
		}
	}
	return name, nil
}

func semanticModuleSourceDigest(
	ctx context.Context,
	source *dagger.ModuleSource,
	subpath string,
) (string, error) {
	// The engine writes its canonical config representation after codegen. Overlaying
	// that representation before enumeration keeps config semantics in the identity
	// without coupling the manifest to pre- versus post-write serialization.
	normalizedContext := source.ContextDirectory().WithDirectory("/", source.UpdatedConfigDirectory())
	semantic := normalizedContext.Directory(subpath).Filter(dagger.DirectoryFilterOpts{
		Exclude: []string{
			".dagger/rust", ".dagger/rust/**",
			"src/dagger_generated", "src/dagger_generated/**",
			"src/bin/dagger-module.rs",
			".gitattributes", ".gitignore",
		},
	})
	paths, err := semantic.Glob(ctx, "**")
	if err != nil {
		return "", fmt.Errorf("enumerate semantic module source: %w", err)
	}
	sort.Strings(paths)
	files := make([]metadata.ModuleSourceFile, 0, len(paths))
	for _, candidate := range paths {
		if strings.HasSuffix(candidate, "/") {
			continue
		}
		digest, err := semantic.File(candidate).Digest(ctx, dagger.FileDigestOpts{ExcludeMetadata: true})
		if err != nil {
			return "", fmt.Errorf("digest semantic module source path %s: %w", candidate, err)
		}
		files = append(files, metadata.ModuleSourceFile{Path: candidate, Digest: digest})
	}
	return metadata.DigestModuleSourceFiles(files)
}

// The private operation root always has a named first component because Rust rejects
// an empty capability path. Rebasing does not change caller-visible result paths.
func rebasePath(candidate string) (string, error) {
	return metadata.RebaseOperationPath(candidate)
}

func stripWorkspacePaths(paths []string) ([]string, error) {
	return metadata.StripOperationRoot(paths)
}
