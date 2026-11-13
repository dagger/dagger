package dagql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
)

// A bundle directory holds a value bundle as BundleFile and, under
// blobs/<algorithm>/<encoded> (ImageBlobPath), the blob of every layer its
// offers name. Its offers address each blob in the directory
// (ImageBlobAddress), never by a path: the form an engine image ships its
// entries in, which the engine merges at start (MergeValues from
// imageCacheID).

// BundleFile is the bundle's file in a bundle directory, beside its blobs.
const BundleFile = "bundle.json"

// BundleWriter receives a bundle as ExportBundle makes it: the bundle, then
// each blob its offers name, once each, by digest.
type BundleWriter interface {
	WriteBundle(context.Context, ValueBundle) error
	WriteBlob(ctx context.Context, d digest.Digest, r content.ReaderAt) error
}

// ExportBundle exports results as a bundle directory's content: each result,
// with its record and its closure's, and the complete parts of the result,
// whatever its type. A result with no parts of its own carries those of the
// results its value holds instead (its child references), and so on down
// through any that have none either. A container carries its filesystem,
// image config and exec metadata, which are its own parts, and a Git module
// source, which has none, carries its checkout's. What a result with parts
// was made from travels as records only: a lookup of the result reads its own
// parts, not those of what made it. Every result the export reads parts from is decoded, with
// resolver, and evaluated first, with every part it declares, and a part that
// still isn't complete fails the export. Every result must be captured: a
// result left out fails the export.
func (c *Cache) ExportBundle(ctx context.Context, resolver TypeResolver, results []AnyResult, cfg config.RefConfig, w BundleWriter) (rerr error) {
	if len(results) == 0 {
		return errors.New("export bundle: no results")
	}
	held, release, err := c.holdBundleRows(ctx, resolver, results)
	if err != nil {
		return err
	}
	defer func() { rerr = errors.Join(rerr, release(context.WithoutCancel(ctx))) }()

	var outputs []SelectedValueOutput
	for _, res := range held {
		if err := c.completeDeclaredParts(ctx, res); err != nil {
			return err
		}
		for _, key := range c.completePartKeys(ctx, res.cacheSharedResult()) {
			var address PersistedPartAddress
			if err := json.Unmarshal([]byte(key), &address); err != nil {
				return fmt.Errorf("export bundle: part %s: %w", key, err)
			}
			outputs = append(outputs, SelectedValueOutput{Result: res, Address: address})
		}
	}
	// Each given result is one root; what their values hold travels in
	// their closures.
	var roots []AnyResult
	seen := map[*sharedResult]bool{}
	for _, res := range results {
		shared := res.cacheSharedResult()
		if shared == nil || seen[shared] {
			continue
		}
		seen[shared] = true
		roots = append(roots, res)
	}
	selection := ValueSelection{Roots: roots, Outputs: outputs}
	return c.WithExportedValues(ctx, selection, cfg, func(ctx context.Context, values *ExportedValues) error {
		if len(values.LeftOut) > 0 {
			left := values.LeftOut[0]
			return fmt.Errorf("export bundle: %d of %d results left out, the first (row %d): %w", len(values.LeftOut), len(roots), left.Row, left.Err)
		}
		bundle := values.Bundle
		address := func(addresses map[digest.Digest]BlobAddress, layers []snapshots.ExportLayer) map[digest.Digest]BlobAddress {
			if addresses == nil {
				addresses = map[digest.Digest]BlobAddress{}
			}
			for _, layer := range layers {
				addresses[layer.Descriptor.Digest] = ImageBlobAddress(layer.Descriptor.Digest)
			}
			return addresses
		}
		if len(bundle.Layers) > 0 {
			bundle.Addresses = address(bundle.Addresses, bundle.Layers)
		}
		for i := range bundle.Outputs {
			if chain := bundle.Outputs[i].Chain; chain != nil && len(chain.Layers) > 0 {
				chain.Addresses = address(chain.Addresses, chain.Layers)
			}
		}
		if err := w.WriteBundle(ctx, bundle); err != nil {
			return err
		}
		written := map[digest.Digest]bool{}
		for _, entry := range values.Chains.Entries {
			for _, layer := range entry.Layers {
				d := layer.Descriptor.Digest
				if written[d] {
					continue
				}
				written[d] = true
				if err := d.Validate(); err != nil {
					return err
				}
				r, err := entry.Provider.ReaderAt(ctx, layer.Descriptor)
				if err != nil {
					return fmt.Errorf("export bundle: blob %s: %w", d, err)
				}
				err = w.WriteBlob(ctx, d, r)
				if closeErr := r.Close(); err == nil {
					err = closeErr
				}
				if err != nil {
					return fmt.Errorf("export bundle: blob %s: %w", d, err)
				}
			}
		}
		return nil
	})
}

// holdBundleRows evaluates results and, for each one with no parts of its
// own, the results its value holds, followed down through those that have
// none either, holding each one it reaches, and returns them: results first,
// then each held child once. A result is evaluated before its parts and
// references are read, since evaluating a value can complete what it holds.
func (c *Cache) holdBundleRows(ctx context.Context, resolver TypeResolver, results []AnyResult) (_ []AnyResult, _ func(context.Context) error, rerr error) {
	var releases []func(context.Context) error
	release := func(ctx context.Context) error {
		var err error
		for _, release := range releases {
			err = errors.Join(err, release(ctx))
		}
		return err
	}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, release(context.WithoutCancel(ctx)))
		}
	}()
	seen := map[sharedResultID]bool{}
	var held []AnyResult
	level := results
	for len(level) > 0 {
		if err := c.Evaluate(ctx, level...); err != nil {
			return nil, nil, fmt.Errorf("export bundle: evaluate: %w", err)
		}
		var next []AnyResult
		for _, res := range level {
			shared := res.cacheSharedResult()
			if shared == nil {
				return nil, nil, errors.New("export bundle: detached result")
			}
			if seen[shared.id] {
				continue
			}
			seen[shared.id] = true
			held = append(held, res)
			children, err := c.partHolders(ctx, shared)
			if err != nil {
				return nil, nil, fmt.Errorf("export bundle: result %d: %w", shared.id, err)
			}
			for _, id := range children {
				if seen[sharedResultID(id)] {
					continue
				}
				child, release, err := c.HoldEntry(ctx, id)
				if err != nil {
					return nil, nil, fmt.Errorf("export bundle: result %d's child %d: %w", shared.id, id, err)
				}
				releases = append(releases, release)
				// A held entry is undecoded, and an undecoded value has
				// nothing to evaluate its parts with.
				decoded, err := c.ensurePersistedHitValueLoaded(ctx, resolver, child)
				if err != nil {
					return nil, nil, fmt.Errorf("export bundle: result %d's child %d: %w", shared.id, id, err)
				}
				next = append(next, decoded)
			}
		}
		level = next
	}
	return held, release, nil
}

// completeDeclaredParts evaluates the parts res's record declares and doesn't
// have complete yet, such as a container's filesystem when only its image
// config was read, so the export carries every part of the value. It fails
// when one is still not complete after.
func (c *Cache) completeDeclaredParts(ctx context.Context, res AnyResult) error {
	pending, err := c.pendingDeclaredParts(ctx, res)
	if err != nil || len(pending) == 0 {
		return err
	}
	if err := c.EvaluateParts(ctx, res, pending...); err != nil {
		return fmt.Errorf("export bundle: result %d: evaluate parts %v: %w", res.cacheSharedResult().id, pending, err)
	}
	if pending, err = c.pendingDeclaredParts(ctx, res); err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("export bundle: result %d: parts %v are not complete after evaluating them", res.cacheSharedResult().id, pending)
	}
	return nil
}

// pendingDeclaredParts returns the parts at the root of res's value that its
// record declares and doesn't have complete.
func (c *Cache) pendingDeclaredParts(ctx context.Context, res AnyResult) ([]PartKey, error) {
	record, err := c.captureRowRecord(ctx, res.cacheSharedResult())
	if err != nil {
		return nil, fmt.Errorf("export bundle: %w", err)
	}
	probes, err := describePartRecord(record)
	if err != nil {
		return nil, fmt.Errorf("export bundle: %w", err)
	}
	var pending []PartKey
	for _, probe := range probes {
		if !probe.LocalComplete && len(probe.Descriptor.Address.OutputPath) == 0 && !slices.Contains(pending, probe.Descriptor.Address.Part) {
			pending = append(pending, probe.Descriptor.Address.Part)
		}
	}
	return pending, nil
}

// partHolders returns, for a row whose record declares no parts of its own,
// the results its record holds as its children (PersistedRefChild): the
// results its value holds, as opposed to those its call only took as inputs.
// A row with parts of its own returns none.
func (c *Cache) partHolders(ctx context.Context, row *sharedResult) ([]uint64, error) {
	record, err := c.captureRowRecord(ctx, row)
	if err != nil {
		return nil, err
	}
	probes, err := describePartRecord(record)
	if err != nil {
		return nil, err
	}
	if len(probes) > 0 {
		return nil, nil
	}
	// An offer's owner references what made the offer, not what the value
	// holds.
	record.Envelope.PendingOffers = nil
	var ids []uint64
	if _, err := VisitEncodedReferences(record, func(ref *PersistedRef) error {
		if ref.Kind == PersistedRefChild && ref.RecipeID == nil && ref.ResultID != 0 && ref.ResultID != uint64(row.id) {
			ids = append(ids, ref.ResultID)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return ids, nil
}
