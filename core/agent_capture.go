package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/log"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
)

// validateAgentRecipe checks the complete captured dependency DAG, including
// lazy references. DagQL's ordinary replay classifier intentionally skips lazy
// arguments: that is correct for loading, but not proof of durability for an
// agent that may dispatch those tools in a later session. This is structural
// dependency validation, not a proof that arbitrary module code is side-effect
// free or that external services and credentials will remain available.
func validateAgentRecipe(srv *dagql.Server, id *call.ID) error {
	if id == nil || id.IsHandle() {
		return fmt.Errorf("agent capture has an engine-local handle instead of a recipe")
	}
	pb, err := id.ToProto()
	if err != nil {
		return fmt.Errorf("agent capture recipe: %w", err)
	}
	return validateAgentRecipeDAG(srv, pb.GetRecipe())
}

func validateAgentRecipeDAG(srv *dagql.Server, recipe *callpbv1.RecipeDAG) error {
	if recipe == nil {
		return fmt.Errorf("agent capture has no recipe DAG")
	}
	for _, digest := range slices.Sorted(maps.Keys(recipe.CallsByDigest)) {
		frame := recipe.CallsByDigest[digest]
		parent := "Query"
		if frame.ReceiverDigest != "" {
			receiver := recipe.CallsByDigest[frame.ReceiverDigest]
			if receiver == nil || receiver.Type == nil {
				return fmt.Errorf("agent capture missing receiver %s", frame.ReceiverDigest)
			}
			parent = receiver.Type.NamedType
		}
		// Host environment and local process access is never reconstructed from
		// the destination client. Even lazy bindings must use immutable captured
		// content or an explicitly portable service recipe instead.
		if parent == "Host" && frame.Field != "__typename" {
			return fmt.Errorf("agent capture depends on originating client via Host.%s (%s)", frame.Field, digest)
		}
		if typ, ok := srv.ObjectType(parent); ok {
			if field, ok := typ.FieldSpec(frame.Field, call.View(frame.View)); ok && field.NotReplayable != "" {
				return fmt.Errorf("agent capture depends on %s.%s (%s): %s", parent, frame.Field, digest, field.NotReplayable)
			}
		}
	}
	return nil
}

// emitAgentCapturePayloads deliberately bypasses the producer's ordinary
// span-reported claims. Those claims are not proof of persistence: selected
// anchors and their entire closure must traverse the protected payload lane.
// The session exporter deduplicates only durably delivered target writes.
// Emission itself is not a persistence acknowledgment; archive sealing verifies
// both successful provider drain and this closure in persisted storage.
func emitAgentCapturePayloads(ctx context.Context, id *call.ID, persisted func(string) bool) error {
	pb, err := id.ToProto()
	if err != nil {
		return err
	}
	recipe := pb.GetRecipe()
	if recipe == nil {
		return fmt.Errorf("agent capture requires recipe payloads")
	}
	logger := telemetry.Logger(ctx, InstrumentationLibrary)
	for _, digest := range slices.Sorted(maps.Keys(recipe.CallsByDigest)) {
		// Check every frame independently: a persisted root is not evidence
		// that all of its dependencies reached the protected storage lane.
		if persisted != nil && persisted(digest) {
			continue
		}
		payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(recipe.CallsByDigest[digest])
		if err != nil {
			return fmt.Errorf("encode capture payload %s: %w", digest, err)
		}
		var record log.Record
		record.SetTimestamp(time.Now())
		record.SetBody(log.BytesValue(payload))
		record.AddAttributes(log.String(telemetry.ContentTypeAttr, telemetryattrs.CallPayloadContentType),
			log.String(telemetryattrs.CallPayloadDigestAttr, digest))
		logger.Emit(ctx, record)
	}
	return nil
}

// Validate effective bindings before rebuilding the flat conversation, because
// some bindings (Workspace, skills, MCP services) are still eager. Construction
// of the final captured recipe cannot be allowed to re-read a live fallback.
func (llm *LLM) validateAgentBindings(ctx context.Context, srv *dagql.Server) error {
	validate := func(label string, value dagql.AnyObjectResult) error {
		id, err := value.RecipeID(ctx)
		if err != nil {
			return fmt.Errorf("capture %s: %w", label, err)
		}
		if err := validateAgentRecipe(srv, id); err != nil {
			return fmt.Errorf("capture %s: %w", label, err)
		}
		return nil
	}
	if llm.mcp.workspace.Self() != nil {
		if err := validate("workspace", llm.mcp.workspace); err != nil {
			return err
		}
	}
	for _, dir := range llm.mcp.skillDirs {
		// Ownership controls recomposition, not recipe identity. Validate the
		// underlying directory without changing the contribution's owner.
		if err := validate("skills", dir.Directory); err != nil {
			return err
		}
	}
	for name, server := range llm.mcp.mcpServers {
		if err := validate("MCP server "+name, server.Service); err != nil {
			return err
		}
	}
	bindings, err := llm.mcp.BoundToolBindings()
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		id := binding.ID
		if id.IsHandle() {
			// Resolving a handle addresses its existing value, not an argument
			// recipe. Derive its durable dependencies without executing a lazy
			// object constructor.
			value, err := srv.Load(ctx, id)
			if err != nil {
				return fmt.Errorf("capture tool handle: %w", err)
			}
			id, err = value.RecipeID(ctx)
			if err != nil {
				return fmt.Errorf("capture tool recipe: %w", err)
			}
		}
		if err := validateAgentRecipe(srv, id); err != nil {
			return fmt.Errorf("capture tools: %w", err)
		}
	}
	return nil
}
