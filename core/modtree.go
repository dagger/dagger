package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"dagger.io/dagger/querybuilder"
	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"

	"github.com/dagger/dagger/util/parallel"
	"github.com/iancoleman/strcase"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type ModTreeNode struct {
	Parent      *ModTreeNode
	Name        string
	Description string
	DagqlServer *dagql.Server
	// This module is the same across all ModTreeNode, this is the root module.
	Module *Module
	// This original module is the one in which the node has been defined.
	OriginalModule *Module
	Type           *TypeDef
	IsCheck        bool
	IsGenerator    bool
	IsUp           bool
}

func (node *ModTreeNode) Path() ModTreePath {
	if node.Parent == nil {
		return nil
	}
	var path ModTreePath
	path = append(path, node.Parent.Path()...)
	path = append(path, node.Name)
	return path
}

func NewModTree(ctx context.Context, mod dagql.ObjectResult[*Module]) (*ModTreeNode, error) {
	main := mod.Self()
	mainObj, ok := main.MainObject()
	if !ok {
		return nil, fmt.Errorf("%q: no main object", main.Name())
	}
	srv, err := dagqlServerForModule(ctx, mod)
	if err != nil {
		return nil, err
	}
	mainObjRes, err := dagql.NewObjectResultForCurrentCall(ctx, srv, mainObj)
	if err != nil {
		return nil, fmt.Errorf("wrap main object typedef %q: %w", mainObj.Name, err)
	}
	return &ModTreeNode{
		DagqlServer:    srv,
		Module:         main,
		OriginalModule: main,
		Type: (&TypeDef{
			Kind:     TypeDefKindObject,
			AsObject: dagql.NonNull(mainObjRes),
		}).syncName(),
		Description: main.Description,
	}, nil
}

func (node *ModTreeNode) Run(
	ctx context.Context,
	// should return true if that's a leaf we need to execute
	// for instance if we want to run a check, return true if IsCheck is true
	isLeaf func(*ModTreeNode) bool,
	// run the right function on the leaf. For instance run as a check, or run as a generator
	// clientMetadata is used to know if we want to try to scale out
	// this callback is used to keep this function generic and allow to return different values
	runLeaf func(context.Context, *ModTreeNode, *engine.ClientMetadata) error,
	include, exclude []string,
) (rerr error) {
	clientMD, _ := engine.ClientMetadataFromContext(ctx)

	if isLeaf(node) {
		return runLeaf(ctx, node, clientMD)
	}

	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	jobs := parallel.New().WithTracing(false)
	for _, child := range children {
		// FIXME: filtering uses `node` instead of `child` - should match against the child being iterated
		if len(include) > 0 {
			if match, err := node.Match(ctx, include); err != nil {
				return err
			} else if !match {
				continue
			}
		}
		if len(exclude) > 0 {
			if match, err := node.Match(ctx, exclude); err != nil {
				return err
			} else if match {
				continue
			}
		}
		jobs = jobs.WithJob(child.Name, func(ctx context.Context) error {
			return child.Run(ctx, isLeaf, runLeaf, nil, nil)
		})
	}
	return jobs.Run(ctx) // don't suppress the error. That can be handled by the top-level caller if necessary
}

func (node *ModTreeNode) RunCheck(ctx context.Context, include, exclude []string) error {
	return node.runAsCheck(ctx,
		func(n *ModTreeNode) bool { return n.IsCheck },
		func(n *ModTreeNode, ctx context.Context) (bool, error) {
			return node.tryRunCheckScaleOut(ctx)
		},
		func(n *ModTreeNode, ctx context.Context) error {
			return n.runCheckLocally(ctx)
		},
		include, exclude)
}

func (node *ModTreeNode) RunGeneratorAsCheck(ctx context.Context, include, exclude []string) error {
	return node.runAsCheck(ctx,
		func(n *ModTreeNode) bool { return n.IsGenerator },
		func(n *ModTreeNode, ctx context.Context) (bool, error) {
			return n.tryRunGeneratorAsCheckScaleOut(ctx)
		},
		func(n *ModTreeNode, ctx context.Context) error {
			return n.runGeneratorAsCheckLocally(ctx)
		},
		include, exclude)
}

// runAsCheck runs a leaf node as a check, with telemetry span and optional scale-out.
func (node *ModTreeNode) runAsCheck(
	ctx context.Context,
	isLeaf func(*ModTreeNode) bool,
	tryScaleOut func(*ModTreeNode, context.Context) (bool, error),
	runLocally func(*ModTreeNode, context.Context) error,
	include, exclude []string,
) error {
	return node.Run(ctx,
		isLeaf,
		func(ctx context.Context, n *ModTreeNode, clientMD *engine.ClientMetadata) (rerr error) {
			// Try scale-out if enabled (will be false for scaled-out sessions)
			if clientMD != nil && clientMD.EnableCloudScaleOut {
				if ok, err := tryScaleOut(n, ctx); ok {
					return err
				}
			}
			ctx, span := Tracer(ctx).Start(ctx, n.PathString(),
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.Bool(telemetry.UIRollUpLogsAttr, true),
					attribute.Bool(telemetry.UIRollUpSpansAttr, true),
					attribute.String(telemetry.CheckNameAttr, n.PathString()),
				),
			)
			defer func() {
				span.SetAttributes(attribute.Bool(telemetry.CheckPassedAttr, rerr == nil))
				telemetry.EndWithCause(span, &rerr)
			}()
			return runLocally(n, ctx)
		},
		include, exclude)
}

func (node *ModTreeNode) runGeneratorAsCheckLocally(ctx context.Context) error {
	cs, err := node.runGeneratorLocally(ctx)
	if err != nil {
		return err
	}
	if cs == nil {
		return nil
	}
	empty, err := cs.IsEmpty(ctx)
	if err != nil {
		return err
	}
	if !empty {
		return fmt.Errorf("generate function %s produced changes; run 'dagger generate %s' to apply",
			node.PathString(), node.PathString())
	}
	return nil
}

func (node *ModTreeNode) tryRunGeneratorAsCheckScaleOut(ctx context.Context) (_ bool, rerr error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, err
	}

	cloudClient, useCloud, err := q.CloudEngineClient(ctx,
		node.RootAddress(),
		node.PathString(),
		nil,
	)
	if err != nil {
		return true, fmt.Errorf("engine-to-engine connect: %w", err)
	}
	if !useCloud {
		return false, nil
	}
	defer func() {
		rerr = errors.Join(rerr, cloudClient.Close())
	}()

	query, err := node.buildScaleOutModuleQuery(cloudClient.Dagger().QueryBuilder())
	if err != nil {
		return true, err
	}

	query = query.Select("generator").Arg("name", node.PathString())
	query = query.Select("run")
	query = query.Select("isEmpty")

	var empty bool
	if err := query.Bind(&empty).Execute(ctx); err != nil {
		return true, err
	}

	if !empty {
		return true, fmt.Errorf("generate function %s produced changes; run 'dagger generate %s' to apply",
			node.PathString(), node.PathString())
	}

	return true, nil
}

func (node *ModTreeNode) runCheckLocally(ctx context.Context) error {
	var status dagql.AnyResult
	if err := node.DagqlValue(ctx, &status); err != nil {
		return err
	}
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](status); ok {
		// If the check returns a syncable type, sync it
		srv := node.DagqlServer
		if syncField, has := obj.ObjectType().FieldSpec("sync", srv.View); has {
			if !syncField.Args.HasRequired(srv.View) {
				if err := srv.Select(
					dagql.WithNonInternalTelemetry(ctx),
					obj,
					&status,
					dagql.Selector{Field: "sync"},
				); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (node *ModTreeNode) tryRunCheckScaleOut(ctx context.Context) (_ bool, rerr error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, err
	}

	cloudClient, useCloud, err := q.CloudEngineClient(ctx,
		node.RootAddress(),
		node.PathString(),
		nil,
	)
	if err != nil {
		return true, fmt.Errorf("engine-to-engine connect: %w", err)
	}
	if !useCloud {
		return false, nil
	}
	defer func() {
		rerr = errors.Join(rerr, cloudClient.Close())
	}()

	query, err := node.buildScaleOutModuleQuery(cloudClient.Dagger().QueryBuilder())
	if err != nil {
		return true, err
	}

	query = query.Select("check").Arg("name", node.PathString())
	query = query.Select("run")
	query = query.Select("error")
	query = query.Select("id")

	var errID string
	if err := query.Bind(&errID).Execute(ctx); err != nil {
		return true, err
	}

	if errID != "" {
		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return true, err
		}
		var idp call.ID
		if err := idp.Decode(errID); err != nil {
			return true, err
		}
		errObj, err := dagql.NewID[*Error](&idp).Load(ctx, srv)
		if err != nil {
			return true, err
		}
		return true, errObj.Self()
	}

	return true, nil
}

// ServiceNameAttr is the telemetry attribute key for the service name.
// Defined locally because the canonical constant lives in the external
// github.com/dagger/otel-go package which we cannot modify.
const ServiceNameAttr = "dagger.io/service.name"

func portScheme(port int) string {
	if port == 443 {
		return "https"
	}
	return "http"
}

// RunUp starts the service and returns a result that must be cleaned up.
// It does NOT block — the caller (UpGroup.Run) handles the blocking wait.
func (node *ModTreeNode) RunUp(ctx context.Context, include, exclude []string, portMappings []PortForward) (*runUpStartResult, error) {
	var result *runUpStartResult
	err := node.Run(ctx,
		func(n *ModTreeNode) bool { return n.IsUp },
		func(ctx context.Context, n *ModTreeNode, clientMD *engine.ClientMetadata) (rerr error) {
			ctx, span := Tracer(ctx).Start(ctx, n.PathString(),
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.Bool(telemetry.UIRollUpLogsAttr, true),
					attribute.String(ServiceNameAttr, n.PathString()),
				),
			)
			defer func() {
				telemetry.EndWithCause(span, &rerr)
			}()
			var err error
			result, err = n.runUpLocally(ctx, span, portMappings)
			return err
		},
		include, exclude)
	return result, err
}

// runUpStartResult is the result of starting a single service in runUpLocally.
// It contains everything needed to display status and clean up after ctx cancellation.
type runUpStartResult struct {
	ReadySpan trace.Span
}

// runUpLocally evaluates the +up function, creates a host tunnel, starts the
// service, and returns immediately. It does NOT block — the caller is
// responsible for blocking on ctx.Done() after all services have started.
// This two-phase design ensures that if one service fails to start, sibling
// goroutines are not left hanging on <-ctx.Done() forever.
func (node *ModTreeNode) runUpLocally(ctx context.Context, parentSpan trace.Span, portMappings []PortForward) (*runUpStartResult, error) {
	// Evaluate the +up function to get the Service
	var svcResult dagql.ObjectResult[*Service]
	if err := node.DagqlValue(ctx, &svcResult); err != nil {
		return nil, err
	}

	// Update parent span name with port info.
	var portStrs []string
	if len(portMappings) > 0 {
		portStrs = make([]string, 0, len(portMappings))
		for _, pf := range portMappings {
			frontend := pf.FrontendOrBackendPort()
			portStrs = append(portStrs, fmt.Sprintf("%s://localhost:%d→%d", portScheme(frontend), frontend, pf.Backend))
		}
	} else if svc := svcResult.Self(); svc != nil && svc.Container.Self() != nil {
		portStrs = make([]string, 0, len(svc.Container.Self().Ports))
		for _, p := range svc.Container.Self().Ports {
			portStrs = append(portStrs, fmt.Sprintf("%s://localhost:%d", portScheme(p.Port), p.Port))
		}
	}
	if len(portStrs) > 0 {
		parentSpan.SetName(fmt.Sprintf("%s %s", node.PathString(), strings.Join(portStrs, ", ")))
	}

	// Set up the host tunnel
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var hostSvc dagql.Result[*Service]
	svcID, err := svcResult.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get service ID: %w", err)
	}
	tunnelArgs := []dagql.NamedInput{
		{Name: "service", Value: dagql.NewID[*Service](svcID)},
	}
	if len(portMappings) > 0 {
		// Use explicit port mappings instead of native 1:1 tunneling.
		portInputs := make([]dagql.InputObject[PortForward], len(portMappings))
		for i, pf := range portMappings {
			inputMap := map[string]any{
				"backend": pf.Backend,
			}
			if pf.Frontend != nil {
				inputMap["frontend"] = *pf.Frontend
			}
			if pf.Protocol != "" {
				inputMap["protocol"] = string(pf.Protocol)
			}
			portInputAny, err := (dagql.InputObject[PortForward]{}).Decoder().DecodeInput(inputMap)
			if err != nil {
				return nil, fmt.Errorf("decode host tunnel port forward input: %w", err)
			}
			portInput, ok := portInputAny.(dagql.InputObject[PortForward])
			if !ok {
				return nil, fmt.Errorf("decode host tunnel port forward input: unexpected input %T", portInputAny)
			}
			portInputs[i] = portInput
		}
		tunnelArgs = append(tunnelArgs, dagql.NamedInput{
			Name:  "ports",
			Value: dagql.ArrayInput[dagql.InputObject[PortForward]](portInputs),
		})
	} else {
		tunnelArgs = append(tunnelArgs, dagql.NamedInput{Name: "native", Value: dagql.Boolean(true)})
	}
	err = srv.Select(ctx, srv.Root(), &hostSvc,
		dagql.Selector{Field: "host"},
		dagql.Selector{
			Field: "tunnel",
			Args:  tunnelArgs,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create host tunnel: %w", err)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}
	hostSvcDig, err := hostSvc.ContentPreferredDigest(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get host service digest: %w", err)
	}
	runningSvc, err := svcs.Start(ctx, hostSvcDig, hostSvc.Self(), true)
	if err != nil {
		return nil, fmt.Errorf("failed to start service: %w", err)
	}

	// Build URL list from the running service's actual ports.
	var urls []string
	for _, port := range runningSvc.Ports {
		scheme := "http"
		if port.Port == 443 {
			scheme = "https"
		}
		urls = append(urls, fmt.Sprintf("%s://localhost:%d", scheme, port.Port))
	}

	// Create a "ready" child span visible in the TUI.
	readyName := "ready"
	if len(urls) > 0 {
		readyName = "ready " + strings.Join(urls, " ")
	}
	_, readySpan := Tracer(ctx).Start(ctx, readyName,
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.StringSlice("service.urls", urls),
		),
	)

	return &runUpStartResult{ReadySpan: readySpan}, nil
}

func (node *ModTreeNode) RunGenerator(ctx context.Context, include, exclude []string) (*Changeset, error) {
	var cs *Changeset
	err := node.Run(ctx,
		func(n *ModTreeNode) bool { return n.IsGenerator },
		func(ctx context.Context, n *ModTreeNode, clientMD *engine.ClientMetadata) (rerr error) {
			// Try scale-out if enabled (will be false for scaled-out sessions)
			if clientMD != nil && clientMD.EnableCloudScaleOut {
				if ok, changes, err := node.tryRunGeneratorScaleOut(ctx); ok {
					cs = changes
					return err
				}
			}
			ctx, span := Tracer(ctx).Start(ctx, node.PathString(),
				telemetry.Reveal(),
				trace.WithAttributes(
					attribute.Bool(telemetry.UIRollUpLogsAttr, true),
					attribute.Bool(telemetry.UIRollUpSpansAttr, true),
					attribute.String(telemetry.GeneratorNameAttr, node.PathString()),
				),
			)
			defer telemetry.EndWithCause(span, &rerr)
			changes, err := n.runGeneratorLocally(ctx)
			cs = changes
			return err
		},
		include, exclude)
	return cs, err
}

func (node *ModTreeNode) runGeneratorLocally(ctx context.Context) (*Changeset, error) {
	var changes dagql.ObjectResult[*Changeset]
	if err := node.DagqlValue(ctx, &changes); err != nil {
		return nil, err
	}
	return changes.Self(), nil
}

func (node *ModTreeNode) tryRunGeneratorScaleOut(ctx context.Context) (_ bool, _ *Changeset, rerr error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, nil, err
	}

	cloudClient, useCloud, err := q.CloudEngineClient(ctx,
		node.RootAddress(),
		node.PathString(),
		nil,
	)
	if err != nil {
		return true, nil, fmt.Errorf("engine-to-engine connect: %w", err)
	}
	if !useCloud {
		return false, nil, nil
	}
	defer func() {
		rerr = errors.Join(rerr, cloudClient.Close())
	}()

	query, err := node.buildScaleOutModuleQuery(cloudClient.Dagger().QueryBuilder())
	if err != nil {
		return true, nil, err
	}

	query = query.Select("generator").Arg("name", node.PathString())
	query = query.Select("run")
	query = query.Select("changes")

	var cs Changeset
	if err := query.Bind(&cs).Execute(ctx); err != nil {
		return true, nil, err
	}

	// ResolveRefs to load Directory objects from IDs
	if err := cs.ResolveRefs(ctx, node.DagqlServer); err != nil {
		return true, nil, fmt.Errorf("resolve changeset refs: %w", err)
	}

	return true, &cs, nil
}

// buildScaleOutModuleQuery builds a query to load a module for scale-out execution.
// It handles all module source kinds (Local, Git, Dir) and returns a query
// positioned at the "asModule" selection, ready for check/generator-specific queries.
func (node *ModTreeNode) buildScaleOutModuleQuery(query *querybuilder.Selection) (*querybuilder.Selection, error) {
	modSrc := node.Module.Source.Value.Self()
	switch modSrc.Kind {
	case ModuleSourceKindLocal:
		query = query.Select("moduleSource").
			Arg("refString", filepath.Join(
				modSrc.Local.ContextDirectoryPath,
				modSrc.SourceRootSubpath,
			))
	case ModuleSourceKindGit:
		query = query.Select("moduleSource").
			Arg("refString", modSrc.AsString()).
			Arg("refPin", modSrc.Git.Commit).
			Arg("requireKind", modSrc.Kind)
	case ModuleSourceKindDir:
		dirID, err := modSrc.DirSrc.OriginalContextDir.ID()
		if err != nil {
			return nil, fmt.Errorf("get dir ID: %w", err)
		}
		dirIDEnc, err := dirID.Encode()
		if err != nil {
			return nil, fmt.Errorf("encode dir ID: %w", err)
		}
		query = query.Select("loadDirectoryFromID").Arg("id", dirIDEnc)
		query = query.Select("asModuleSource").
			Arg("sourceRootPath", modSrc.DirSrc.OriginalSourceRootSubpath)
	}
	return query.Select("asModule"), nil
}

// Initialize a standalone dagql server for querying the given module
func dagqlServerForModule(ctx context.Context, mod dagql.ObjectResult[*Module]) (*dagql.Server, error) {
	main := mod.Self()
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	srv, err := dagql.NewServer(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("create module dagql server: %w", err)
	}
	srv.Around(AroundFunc)
	// Install default "dependencies" (ie the core)
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("%q: load core schema: %w", main.Name(), err)
	}
	// Install dependencies
	for _, defaultDep := range defaultDeps.Mods() {
		if err := defaultDep.Install(ctx, srv); err != nil {
			return nil, fmt.Errorf("%q: serve core schema: %w", main.Name(), err)
		}
	}
	// Install the main module
	if err := NewUserMod(mod).Install(ctx, srv); err != nil {
		return nil, fmt.Errorf("%q: serve module: %w", main.Name(), err)
	}
	return srv, nil
}

// The address of the dagger module that is the root of the tree
// If the node is a "file", the root address is the URL of the filesystem root
func (node *ModTreeNode) RootAddress() string {
	mod := node.Module
	if mod == nil {
		return ""
	}
	modSrc := mod.Source.Value.Self()
	if modSrc == nil {
		return ""
	}
	return modSrc.AsString()
}

func (node *ModTreeNode) Clone() *ModTreeNode {
	cp := *node
	return &cp
}

func (node *ModTreeNode) DagqlValue(ctx context.Context, dest any) error {
	// We can't direct-select the dagql path, because Select() doesn't support traversing
	// lists
	// FIXME: as an optimization, one-shot when possible?
	srv := node.DagqlServer
	// 1. Are we the root? Select the module's main object from Query root.
	// A node is also treated as root if its parent is a synthetic naming-only
	// node (e.g. injected by workspace checks reparenting, which sets
	// Parent to an empty ModTreeNode with nil Module).
	if node.Parent == nil || node.Parent.Module == nil {
		return srv.Select(ctx, srv.Root(), dest, dagql.Selector{Field: gqlFieldName(node.Module.Name())})
	}
	// 2. Is parent an object?
	if parentObjType := node.Parent.ObjectType(); parentObjType != nil {
		var parentObjValue dagql.AnyObjectResult
		if err := node.Parent.DagqlValue(ctx, &parentObjValue); err != nil {
			return err
		}
		return srv.Select(dagql.WithNonInternalTelemetry(ctx), parentObjValue, dest, dagql.Selector{Field: node.Name})
	}
	return fmt.Errorf("%q: get value: parent is not an object", node.PathString())
}

func debugTrace(ctx context.Context, msg string, args ...any) {
	_ = parallel.
		New().
		WithContextualTracer(true).
		WithJob(fmt.Sprintf(msg, args...), nil).
		Run(ctx)
}

// Walk the tree and return all matching nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupNodes(ctx context.Context, matches func(*ModTreeNode) bool, include []string, exclude []string) ([]*ModTreeNode, error) {
	var res []*ModTreeNode
	err := node.Walk(ctx, func(ctx context.Context, n *ModTreeNode) (bool, error) {
		// FIXME: prune the search tree more aggressively, for efficiency
		// BUT be careful to not break matching!
		if matches(n) {
			if len(include) > 0 {
				if match, err := n.Match(ctx, include); err != nil {
					return false, err
				} else if !match {
					debugTrace(ctx, "%q: does not match %v. Skipping", n.PathString(), include)
					return false, nil
				}
			}
			if len(exclude) > 0 {
				if match, err := n.Match(ctx, exclude); err != nil {
					return false, err
				} else if match {
					return false, nil
				}
			}
			res = append(res, n)
			return false, nil // always looking for leaves - no point in trying to walk
		}
		return true, nil
	})
	slices.SortStableFunc(res, func(a, b *ModTreeNode) int {
		return strings.Compare(a.PathString(), b.PathString())
	})
	// Deduplicate by path — a function and its object subtree can both
	// produce the same leaf (e.g. toolchain services appear in both).
	res = slices.CompactFunc(res, func(a, b *ModTreeNode) bool {
		return a.PathString() == b.PathString()
	})
	return res, err
}

// Walk the tree and return all check nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupChecks(ctx context.Context, include []string, exclude []string) ([]*ModTreeNode, error) {
	return node.RollupNodes(ctx, func(n *ModTreeNode) bool {
		return n.IsCheck
	}, include, exclude)
}

// Walk the tree and return all generator nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupGenerator(ctx context.Context, include []string, exclude []string) ([]*ModTreeNode, error) {
	return node.RollupNodes(ctx, func(n *ModTreeNode) bool {
		return n.IsGenerator
	}, include, exclude)
}

// Walk the tree and return all up (service) nodes, with include and exclude filters applied.
func (node *ModTreeNode) RollupUp(ctx context.Context, include []string, exclude []string) ([]*ModTreeNode, error) {
	return node.RollupNodes(ctx, func(n *ModTreeNode) bool {
		return n.IsUp
	}, include, exclude)
}

type ModTreePath []string

func NewModTreePath(s string) ModTreePath {
	return ModTreePath(strings.Split(s, ":"))
}

func (p ModTreePath) CliCase() []string {
	cliCase := make([]string, len(p))
	for i := range p {
		cliCase[i] = strcase.ToKebab(p[i])
	}
	return cliCase
}

func (p ModTreePath) APICase() []string {
	apiCase := make([]string, len(p))
	for i := range p {
		apiCase[i] = gqlFieldName(p[i])
	}
	return apiCase
}

func (p ModTreePath) Contains(ctx context.Context, target ModTreePath) (result bool) {
	defer func() {
		debugTrace(ctx, "%v.Contains(%v) -> %v", p, target, result)
	}()
	if len(target) < len(p) {
		// if the target is shorter, it can't be a sub-path
		return false
	}
	targetParent := target[:len(p)]
	return p.Equals(ctx, targetParent)
}

func (p ModTreePath) Equals(ctx context.Context, other ModTreePath) (result bool) {
	defer func() {
		debugTrace(ctx, "%v.Equals(%v) -> %v", p, other, result)
	}()
	if len(p) != len(other) {
		return false
	}
	for i := range p {
		if gqlFieldName(p[i]) != gqlFieldName(other[i]) {
			debugTrace(ctx, "%v.Equals(%v): %q != %q -> NOT EQUAL", p, other, gqlFieldName(p[i]), gqlFieldName(other[i]))
			return false
		}
	}
	return true
}

func (p ModTreePath) Glob(ctx context.Context, pattern string) (bool, error) {
	// Normalize both pattern and path to CLI case (kebab-case) for consistent matching
	slashPattern := strings.Join(NewModTreePath(pattern).CliCase(), "/")
	slashPath := strings.Join(p.CliCase(), "/")
	if match, err := doublestar.PathMatch(slashPattern, slashPath); err != nil {
		return false, err
	} else if match {
		debugTrace(ctx, "%q.Glob(%q) -> MATCH", slashPath, slashPattern)
		return true, nil
	}
	debugTrace(ctx, "%q.Glob(%q) -> no match", slashPath, slashPattern)
	return false, nil
}

func (node *ModTreeNode) Match(ctx context.Context, patterns []string) (bool, error) {
	if node.Parent == nil {
		// The root node matches everything
		return true, nil
	}
	if len(patterns) == 0 {
		return true, nil
	}
	for _, pattern := range patterns {
		if match, err := node.Path().Glob(ctx, pattern); err != nil {
			return false, err
		} else if match {
			return true, nil
		}
		patternAsPath := NewModTreePath(pattern)
		if patternAsPath.Contains(ctx, node.Path()) {
			return true, nil
		}
	}
	return false, nil
}

func (node *ModTreeNode) PathString() string {
	return strings.Join(node.Path().CliCase(), ":")
}

type WalkFunc func(context.Context, *ModTreeNode) (bool, error)

func (node *ModTreeNode) Walk(ctx context.Context, fn WalkFunc) error {
	return node.walk(ctx, fn, make(map[string]bool))
}

func (node *ModTreeNode) walk(ctx context.Context, fn WalkFunc, visiting map[string]bool) error {
	// The callback is always called so that leaves (checks, services, etc.)
	// are always discovered regardless of cycle state.
	enter, err := fn(ctx, node)
	if err != nil {
		return err
	}
	if !enter {
		return nil
	}

	// Cycle detection: if this node's object type has already been seen
	// along the current path, don't descend into its children.
	// This prevents infinite recursion when e.g. Service.start() returns
	// Service, which has start(), which returns Service, etc.
	var typeName string
	if obj := node.ObjectType(); obj != nil {
		typeName = obj.Name
	}
	if typeName != "" {
		if visiting[typeName] {
			return nil
		}
		visiting[typeName] = true
		defer delete(visiting, typeName)
	}

	children, err := node.Children(ctx)
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := child.walk(ctx, fn, visiting); err != nil {
			return err
		}
	}
	return nil
}

// Children returns child nodes for tree walking.
// NOTE: When a function returns a module-defined object, both a function leaf
// (preserving IsCheck/IsGenerator/IsUp) and an object subtree child are
// added with the same Name. This is intentional so that leaf flags are preserved
// while the object's nested functions are still discoverable via the subtree.
// Callers that need unique results should deduplicate by path (see RollupNodes).
func (node *ModTreeNode) Children(ctx context.Context) ([]*ModTreeNode, error) {
	var children []*ModTreeNode
	if objType := node.ObjectType(); objType != nil {
		nodeType := objType.Name
		for _, fnRes := range objType.Functions {
			fn := fnRes.Self()
			if functionRequiresArgs(fn) {
				continue
			}
			returnType := fn.ReturnType.Self().ToType().Name()
			children = append(children, &ModTreeNode{
				Parent:         node,
				Name:           fn.Name,
				DagqlServer:    node.DagqlServer,
				Module:         node.Module,
				OriginalModule: node.OriginalModule,
				Type:           fn.ReturnType.Self(),
				IsCheck:        fn.IsCheck,
				IsGenerator:    fn.IsGenerator,
				IsUp:           fn.IsUp,
				Description:    fn.Description,
			})
			// if the type returned by the function is an object, also add the object subtree
			if returnsObject := fn.ReturnType.Self().AsObject.Valid; returnsObject &&
				// avoid cycles (X.withFoo: X)
				returnType != nodeType {
				if subObj, ok := node.OriginalModule.ObjectByName(fn.ReturnType.Self().ToType().Name()); ok {
					subObjRes, err := dagql.NewObjectResultForCurrentCall(ctx, node.DagqlServer, subObj)
					if err != nil {
						return nil, fmt.Errorf("wrap child object typedef %q: %w", subObj.Name, err)
					}
					children = append(children, &ModTreeNode{
						Parent:         node,
						Name:           fn.Name,
						DagqlServer:    node.DagqlServer,
						Module:         node.Module,
						OriginalModule: node.OriginalModule,
						Type: (&TypeDef{
							Kind:     TypeDefKindObject,
							AsObject: dagql.NonNull(subObjRes),
						}).syncName(),
						IsCheck:     false,
						IsGenerator: false,
						IsUp:        false,
						Description: subObj.Description,
					})
				}
			}
		}
		for _, fieldRes := range objType.Fields {
			field := fieldRes.Self()
			children = append(children, &ModTreeNode{
				Parent:         node,
				Name:           field.Name,
				DagqlServer:    node.DagqlServer,
				Module:         node.Module,
				OriginalModule: node.OriginalModule,
				Type:           field.TypeDef.Self(),
				IsCheck:        false,
				IsGenerator:    false,
				IsUp:           false,
				Description:    field.Description,
			})
		}
	}
	return children, nil
}

func (node *ModTreeNode) ChildrenNames(ctx context.Context) ([]string, error) {
	children, err := node.Children(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(children))
	for i := range children {
		names[i] = children[i].Name
	}
	return names, nil
}

func (node *ModTreeNode) Child(ctx context.Context, name string) (*ModTreeNode, error) {
	children, err := node.Children(ctx)
	if err != nil {
		return nil, err
	}
	for _, child := range children {
		if child.Name == name {
			return child, nil
		}
	}
	return nil, nil
}

func (node *ModTreeNode) ObjectType() *ObjectTypeDef {
	return node.Type.AsObject.Value.Self()
}
