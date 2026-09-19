package schema

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/fixturetransport"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
	"github.com/opencontainers/go-digest"
)

// The gated fixture's control operations. They add no GraphQL field or
// argument: each new operation reads one typed JSON request record, named by
// `path`, from the fixture root's control/ directory, under the same os.Root
// containment and strict decoding as bundles.

// fixtureOperationArgs says which of the field's arguments an operation
// accepts. An argument that is irrelevant to the operation is an error.
type fixtureOperationArgs struct {
	ids     bool // accepts exact handles
	needIDs bool // requires at least one
	path    bool // requires a contained relative path
}

var fixtureOperations = map[string]fixtureOperationArgs{
	"export":            {ids: true, needIDs: true, path: true},
	"import":            {path: true},
	"report":            {ids: true},
	"recordBody":        {},
	"exportSelected":    {ids: true, needIDs: true, path: true},
	"offer":             {ids: true, needIDs: true, path: true},
	"takeRenewal":       {path: true},
	"replyRenewal":      {path: true},
	"armRenewalReply":   {path: true},
	"barrierArm":        {path: true},
	"barrierWait":       {path: true},
	"barrierRelease":    {path: true},
	"hold":              {ids: true, needIDs: true},
	"releaseHold":       {path: true},
	"dropRetainedRoots": {ids: true, needIDs: true},
	"gc":                {},
	"transport":         {path: true},
	"observe":           {path: true},
}

func validateFixtureOperation(args remoteCacheFixtureArgs) error {
	rule, ok := fixtureOperations[args.Operation]
	if !ok {
		return fmt.Errorf("unknown fixture operation %q", args.Operation)
	}
	if args.Operation != "export" && len(args.OutputIDs) != 0 {
		return fmt.Errorf("selected outputs require export")
	}
	switch {
	case !rule.ids && len(args.IDs) != 0:
		return fmt.Errorf("%s does not accept IDs", args.Operation)
	case rule.needIDs && len(args.IDs) == 0:
		return fmt.Errorf("%s requires handles", args.Operation)
	case !rule.path && args.Path != "":
		return fmt.Errorf("%s does not accept a path", args.Operation)
	case rule.path:
		return fixtureBundlePath(args.Path)
	}
	return nil
}

type fixtureExportSelectedRequest struct {
	Bundle  string                        `json:"bundle"`
	Outputs []fixtureExportSelectedOutput `json:"outputs"`
}
type fixtureExportSelectedOutput struct {
	Handle  string                     `json:"handle"`
	Address dagql.PersistedPartAddress `json:"address"`
}
type fixtureExportedLayer struct {
	Digest digest.Digest `json:"digest"`
	Size   int64         `json:"size"`
	// CopiedBytes is zero for a layer an earlier selection already copied.
	CopiedBytes int64 `json:"copiedBytes"`
}
type fixtureExportedOutput struct {
	Ordinal dagql.TransferOrdinal      `json:"ordinal"`
	Address dagql.PersistedPartAddress `json:"address"`
	Layers  []fixtureExportedLayer     `json:"layers"`
}
type fixtureExportSelectedResult struct {
	Roots   []remoteCacheFixtureMapping `json:"roots"`
	Outputs []fixtureExportedOutput     `json:"outputs"`
}

type fixtureOfferRequest struct {
	Offers []dagql.PersistedPartOffer `json:"offers"`
}
type fixtureOfferDisposition struct {
	Address  dagql.PersistedPartAddress `json:"address"`
	Outcome  string                     `json:"outcome"`
	Replaced bool                       `json:"replaced"`
	OfferRev uint64                     `json:"offerRev"`
	Error    string                     `json:"error,omitempty"`
}

var fixtureOfferOutcomes = map[dagql.OfferOutcome]string{
	dagql.OfferUnavailable:      "unavailable",
	dagql.OfferAccepted:         "accepted",
	dagql.OfferAlreadyComplete:  "alreadyComplete",
	dagql.OfferExecutionStarted: "executionStarted",
	dagql.OfferInvalid:          "invalid",
}

type fixtureBarrierToken struct {
	Key        string `json:"key"`
	Generation uint64 `json:"generation"`
}
type fixtureHoldToken struct {
	Token string `json:"token"`
}
type fixtureObserveRequest struct {
	Cap int `json:"cap"`
}

func readFixtureControl(path, name string, value any) error {
	root, err := fixtureRoot(path, "control")
	if err != nil {
		return err
	}
	defer root.Close()
	return readFixtureJSON(root, name, value)
}

func fixtureControls(q *core.Query) (core.RemoteCacheFixtureControls, error) {
	controls, ok := q.Server.(core.RemoteCacheFixtureControls)
	if !ok {
		return nil, fmt.Errorf("this engine has no fixture server controls")
	}
	return controls, nil
}

// runFixtureControl handles every operation batch 7 added. ids are the
// already-decoded handles of the field's ids argument.
//
//nolint:gocyclo // one case per fixture control; splitting the dispatcher would hurt clarity
func runFixtureControl(ctx context.Context, q *core.Query, cache *dagql.Cache, sessionID, path string, args remoteCacheFixtureArgs, ids []*call.ID) (any, error) {
	switch args.Operation {
	case "exportSelected":
		return fixtureExportSelected(ctx, cache, sessionID, path, args.Path, ids)
	case "offer":
		var req fixtureOfferRequest
		if err := readFixtureControl(path, args.Path, &req); err != nil {
			return nil, err
		}
		if len(ids) != 1 {
			return nil, fmt.Errorf("offer takes exactly one already-created receiver handle")
		}
		controls, err := fixtureControls(q)
		if err != nil {
			return nil, err
		}
		var out []fixtureOfferDisposition
		// The exact receiver is held for the real adapter call. It is never
		// created or demanded here.
		err = cache.WithTransferFixtureRoots(ctx, sessionID, ids, func(roots []dagql.AnyResult) error {
			dispositions, err := controls.RemoteCacheFixtureOffer(ctx, roots[0], req.Offers)
			// Earlier accepted entries stay reported beside a later failure.
			for _, d := range dispositions {
				entry := fixtureOfferDisposition{Address: d.Address, Outcome: fixtureOfferOutcomes[d.Outcome], Replaced: d.Replaced, OfferRev: d.OfferRev}
				if d.Err != nil {
					entry.Error = d.Err.Error()
				}
				out = append(out, entry)
			}
			return err
		})
		if err != nil && len(out) == 0 {
			return nil, err
		}
		return struct {
			Dispositions []fixtureOfferDisposition `json:"dispositions"`
			Error        string                    `json:"error,omitempty"`
		}{out, errorText(err)}, nil
	case "takeRenewal":
		controls, err := fixtureControls(q)
		if err != nil {
			return nil, err
		}
		renewal, err := controls.RemoteCacheFixtureTakeRenewal(ctx)
		if err != nil {
			return nil, err
		}
		root, err := fixtureRoot(path, "control")
		if err != nil {
			return nil, err
		}
		defer root.Close()
		// The record is written atomically under its unique exchange name.
		if err := writeFixtureJSON(ctx, root, args.Path, renewal); err != nil {
			return nil, err
		}
		return renewal, nil
	case "replyRenewal", "armRenewalReply":
		var reply core.RemoteCacheFixtureRenewalReply
		if err := readFixtureControl(path, args.Path, &reply); err != nil {
			return nil, err
		}
		controls, err := fixtureControls(q)
		if err != nil {
			return nil, err
		}
		if args.Operation == "armRenewalReply" {
			return struct {
				Armed digest.Digest `json:"armed"`
			}{reply.Chain}, controls.RemoteCacheFixtureArmRenewalReply(reply)
		}
		disposition, err := controls.RemoteCacheFixtureReplyRenewal(reply)
		return struct {
			Disposition string `json:"disposition"`
		}{disposition}, err
	case "barrierArm":
		var req dagql.FixtureBarrierRequest
		if err := readFixtureControl(path, args.Path, &req); err != nil {
			return nil, err
		}
		return cache.ArmTransferFixtureBarrier(req)
	case "barrierWait", "barrierRelease":
		var token fixtureBarrierToken
		if err := readFixtureControl(path, args.Path, &token); err != nil {
			return nil, err
		}
		if args.Operation == "barrierWait" {
			return cache.WaitTransferFixtureBarrier(ctx, token.Key, token.Generation)
		}
		return token, cache.ReleaseTransferFixtureBarrier(token.Key, token.Generation)
	case "hold":
		return cache.HoldTransferFixtureRoots(ctx, sessionID, ids)
	case "releaseHold":
		var token fixtureHoldToken
		if err := readFixtureControl(path, args.Path, &token); err != nil {
			return nil, err
		}
		return cache.ReleaseTransferFixtureHold(ctx, token.Token)
	case "dropRetainedRoots":
		return cache.DropTransferFixtureRetainedRoots(ctx, sessionID, ids)
	case "gc":
		controls, err := fixtureControls(q)
		if err != nil {
			return nil, err
		}
		return controls.RemoteCacheFixtureGC(ctx)
	case "transport":
		// A copied response script for the fixture's own hosts, swapped in as
		// one immutable generation. It sets statuses, headers, body files and
		// named read faults; it cannot alter a URL, an identity or a cache
		// output.
		var script fixturetransport.Script
		if err := readFixtureControl(path, args.Path, &script); err != nil {
			return nil, err
		}
		dispatcher := fixturetransport.Current()
		if dispatcher == nil {
			return nil, fmt.Errorf("this engine has no fixture transport")
		}
		generation, err := dispatcher.SetScript(script)
		return struct {
			Generation uint64 `json:"generation"`
		}{generation}, err
	case "observe":
		// The per-scenario observation bound. Overflow fails the report
		// instead of dropping events.
		var req fixtureObserveRequest
		if err := readFixtureControl(path, args.Path, &req); err != nil {
			return nil, err
		}
		if req.Cap <= 0 {
			return nil, fmt.Errorf("observe requires a positive cap")
		}
		cache.SetTransferFixtureEventCap(req.Cap)
		if dispatcher := fixturetransport.Current(); dispatcher != nil {
			dispatcher.SetObservationCap(req.Cap)
		}
		if controls, err := fixtureControls(q); err == nil {
			if err := controls.RemoteCacheFixtureObserve(req.Cap); err != nil {
				return nil, err
			}
		}
		return req, nil
	}
	return nil, fmt.Errorf("unknown fixture operation %q", args.Operation)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// fixtureExportSelected exports the held roots with exactly the requested
// full addresses. Every unique layer is copied from its actual ReaderAt into
// a temporary blob while its size and digest are checked, then renamed; the
// bundle is published last, after every requested blob succeeded.
func fixtureExportSelected(ctx context.Context, cache *dagql.Cache, sessionID, path, name string, ids []*call.ID) (any, error) {
	var req fixtureExportSelectedRequest
	if err := readFixtureControl(path, name, &req); err != nil {
		return nil, err
	}
	if err := fixtureBundlePath(req.Bundle); err != nil {
		return nil, err
	}
	outputIDs := make([]*call.ID, len(req.Outputs))
	for i, output := range req.Outputs {
		id := new(call.ID)
		if err := id.Decode(output.Handle); err != nil {
			return nil, fmt.Errorf("selected output %d: %w", i, err)
		}
		outputIDs[i] = id
	}
	var result fixtureExportSelectedResult
	err := cache.WithTransferFixtureRoots(ctx, sessionID, ids, func(roots []dagql.AnyResult) error {
		return cache.WithTransferFixtureRoots(ctx, sessionID, outputIDs, func(outputs []dagql.AnyResult) error {
			selection := dagql.ValueSelection{Roots: roots}
			for i, output := range outputs {
				selection.Outputs = append(selection.Outputs, dagql.SelectedValueOutput{Result: output, Address: req.Outputs[i].Address})
			}
			// WithExportedValues refuses a selected row outside the held root
			// closure, and releases chain handles before closure holds.
			return cache.WithExportedValues(ctx, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(ctx context.Context, values *dagql.ExportedValues) error {
				copied, err := copyFixtureChains(ctx, path, values.Chains)
				if err != nil {
					return err
				}
				result.Outputs = copied
				result.Roots, err = fixtureMappings(values.Bundle, values.Sources)
				if err != nil {
					return err
				}
				root, err := fixtureRoot(path, "bundles")
				if err != nil {
					return err
				}
				defer root.Close()
				return writeFixtureJSON(ctx, root, req.Bundle, values.Bundle)
			})
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func copyFixtureChains(ctx context.Context, path string, chains *dagql.SelectedChains) ([]fixtureExportedOutput, error) {
	root, err := fixtureRoot(path, "blobs")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	done := map[digest.Digest]bool{}
	var out []fixtureExportedOutput
	for _, entry := range chains.Entries {
		exported := fixtureExportedOutput{Ordinal: entry.Ordinal, Address: entry.Address}
		for _, layer := range entry.Layers {
			desc := layer.Descriptor
			if err := desc.Digest.Validate(); err != nil {
				return nil, err
			}
			report := fixtureExportedLayer{Digest: desc.Digest, Size: desc.Size}
			if !done[desc.Digest] {
				reader, err := entry.Provider.ReaderAt(ctx, desc)
				if err != nil {
					return nil, err
				}
				report.CopiedBytes, err = copyFixtureBlob(ctx, root, reader, desc.Digest, desc.Size)
				if err != nil {
					return nil, err
				}
				done[desc.Digest] = true
			}
			exported.Layers = append(exported.Layers, report)
		}
		out = append(out, exported)
	}
	return out, nil
}

// copyFixtureBlob closes the reader and the temporary file on every path. An
// orphaned temporary file is not a successful export.
func copyFixtureBlob(ctx context.Context, root *os.Root, reader content.ReaderAt, want digest.Digest, size int64) (_ int64, rerr error) {
	defer func() {
		if err := reader.Close(); err != nil && rerr == nil {
			rerr = err
		}
	}()
	if reader.Size() != size {
		return 0, fmt.Errorf("fixture blob %s: reader has %d bytes, descriptor %d", want, reader.Size(), size)
	}
	dir := want.Algorithm().String()
	if err := root.MkdirAll(dir, 0700); err != nil {
		return 0, err
	}
	tmp := filepath.Join(dir, ".tmp-"+identity.NewID())
	file, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, err
	}
	digester := want.Algorithm().Digester()
	n, copyErr := io.Copy(io.MultiWriter(file, digester.Hash()), io.NewSectionReader(reader, 0, size))
	closeErr := file.Close()
	switch {
	case copyErr != nil:
		err = copyErr
	case closeErr != nil:
		err = closeErr
	case context.Cause(ctx) != nil:
		err = context.Cause(ctx)
	case n != size || digester.Digest() != want:
		err = fmt.Errorf("fixture blob %s: copied %d bytes with digest %s", want, n, digester.Digest())
	}
	if err != nil {
		_ = root.Remove(tmp)
		return 0, err
	}
	return n, root.Rename(tmp, filepath.Join(dir, want.Encoded()))
}
