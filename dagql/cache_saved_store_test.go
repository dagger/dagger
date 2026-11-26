package dagql

// Measuring tools for a saved Cloud cache: a store a cache saved with its
// blob store (WithBlobStore), such as the remote cache service's. Each test
// restores the store alone, the way the service opens it, and reports:
//
//   - TestSavedStoreComposition: the restored cache's live heap, and what each
//     kind of thing its entries hold costs, by letting go of each in turn and
//     collecting (composition.txt);
//   - TestSavedStoreRecords: the records, their parts and the calls kept with
//     them, finer than the composition, how kept calls compress, and what
//     share of the records' bytes the most recently used entries hold
//     (records.txt);
//   - TestSavedStoreTimes: how long the restore takes, bundles of the first
//     4,000 entries that keep calls, and a save's read of the store (times.txt,
//     with a CPU profile of the bundles).
//
// They skip unless given a store and a report directory:
//
//	go test ./dagql -run '^TestSavedStoreComposition$' -count=1 -timeout 60m \
//	  -args -savedstore=/path/to/store.db -savedstore.out=/path/to/report
//
// The store is copied before it is opened, so it is left as it was.
//
// They read the cache's own fields, so a change to what the cache holds
// changes them too: keep each step's name, so two builds' reports compare
// line by line.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
	"unique"
	"unsafe"

	"github.com/klauspost/compress/zstd"
)

var (
	savedStorePath = flag.String("savedstore", "", "a saved Cloud cache (SQLite) to measure; it is copied before use")
	savedStoreOut  = flag.String("savedstore.out", "", "the directory the report is written to")
)

func savedStoreHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}

func savedStoreCopy(t *testing.T, from, to string) {
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSavedStoreComposition(t *testing.T) {
	if *savedStorePath == "" || *savedStoreOut == "" {
		t.Skip("no -savedstore or -savedstore.out")
	}
	debug.SetGCPercent(100)
	var report strings.Builder
	say := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		fmt.Println(line)
		report.WriteString(line + "\n")
	}
	defer func() {
		_ = os.WriteFile(filepath.Join(*savedStoreOut, "composition.txt"), []byte(report.String()), 0o644)
	}()

	dir := t.TempDir()
	path := filepath.Join(dir, "org.db")
	savedStoreCopy(t, *savedStorePath, path)

	base := savedStoreHeap()
	start := time.Now()
	c, err := NewCache(context.Background(), path, nil, nil, WithBlobStore())
	if err != nil {
		t.Fatal(err)
	}
	savedStoreRestored(t, c)
	opened := time.Since(start)
	// Let the body worker finish what the restore queued (taught content).
	time.Sleep(20 * time.Second)
	total := savedStoreHeap() - base
	say("opened in %s; entries=%d; live heap of the restored cache %.3f GB (%.0f B an entry)", opened.Round(time.Millisecond), len(c.resultsByID), savedStoreGB(total), float64(total)/float64(savedStoreAtLeast1(len(c.resultsByID))))
	say("sizeof sharedResult=%d egraphTerm=%d storedBody=%d keptCall=%d storedPart=%d partFacts=%d PersistedResultEnvelope=%d ResultCall=%d",
		unsafe.Sizeof(sharedResult{}), unsafe.Sizeof(egraphTerm{}), unsafe.Sizeof(storedBody{}), unsafe.Sizeof(keptCall{}), unsafe.Sizeof(storedPart{}), unsafe.Sizeof(partFacts{}), unsafe.Sizeof(PersistedResultEnvelope{}), unsafe.Sizeof(ResultCall{}))

	// Hold the graph lock from here to the end: nothing else may run on
	// what is let go. The cache is never used or closed again.
	c.egraphMu.Lock()

	savedStoreEntryCounts(c, say)
	savedStoreIndexCounts(c, say)

	savedStoreCompositionSteps(c, base, say)

	runtime.KeepAlive(c)
}

// savedStoreRestored fails the test unless the cache restored the store it
// was given: a cache that cannot restore a store (another schema, a failed
// import) wipes it and starts empty, and its figures would be an empty cache's.
func savedStoreRestored(t *testing.T, c *Cache) {
	t.Helper()
	if reason := c.PersistenceResetReason(); reason != CachePersistenceResetNone {
		t.Fatalf("the saved store was not restored: the cache started empty (%s)", reason)
	}
}

func savedStoreGB(n uint64) float64 { return float64(n) / 1e9 }

func savedStoreAtLeast1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

func TestSavedStoreRecords(t *testing.T) {
	if *savedStorePath == "" || *savedStoreOut == "" {
		t.Skip("no -savedstore or -savedstore.out")
	}
	var report strings.Builder
	say := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		fmt.Println(line)
		report.WriteString(line + "\n")
	}
	defer func() {
		_ = os.WriteFile(filepath.Join(*savedStoreOut, "records.txt"), []byte(report.String()), 0o644)
	}()
	dir := t.TempDir()
	path := filepath.Join(dir, "org.db")
	savedStoreCopy(t, *savedStorePath, path)
	base := savedStoreHeap()
	c, err := NewCache(context.Background(), path, nil, nil, WithBlobStore())
	if err != nil {
		t.Fatal(err)
	}
	savedStoreRestored(t, c)
	time.Sleep(20 * time.Second)
	total := savedStoreHeap() - base
	n := len(c.resultsByID)
	say("entries=%d live heap of the restored cache %.3f GB", n, savedStoreGB(total))

	c.egraphMu.Lock()

	savedStoreKeptCallCompression(t, c, say)

	// Stored parts.
	var parts, partEntries int
	for _, res := range c.resultsByID {
		if len(res.storedParts) > 0 {
			partEntries++
			parts += len(res.storedParts)
		}
	}
	say("stored parts %d on %d entries", parts, partEntries)

	savedStoreRecency(c, n, say)

	prev := savedStoreHeap()
	step := func(name string, drop func()) {
		drop()
		now := savedStoreHeap()
		freed := int64(prev) - int64(now)
		say("STEP %-62s freed %7.3f GB  %6.0f B an entry", name, float64(freed)/1e9, float64(freed)/float64(n))
		prev = now
	}
	// A body holds its envelope and frame in one allocation: the envelopes'
	// step lets go of both, and the frames' step of nothing. Both steps stay
	// so that reports compare line by line.
	step("records: compressed envelopes' bytes", func() {
		for _, res := range c.resultsByID {
			if b := res.stored; b != nil {
				b.bytes = nil
			}
		}
	})
	step("records: compressed frames' bytes", func() {})
	step("records: light envelopes", func() {
		for _, res := range c.resultsByID {
			res.persistedEnvelope = nil
		}
	})
	step("records: transfer copies", func() {
		for _, res := range c.resultsByID {
			res.storedTransfer.Store(nil)
		}
	})
	step("records: body structs and their refs", func() {
		for _, res := range c.resultsByID {
			res.stored = nil
		}
	})
	step("part facts", func() {
		for _, res := range c.resultsByID {
			res.partFacts = nil
			res.completeParts.Store(nil)
			res.unavailableParts = nil
		}
	})
	step("stored parts: each entry's parts", func() {
		for _, res := range c.resultsByID {
			res.storedParts = nil
		}
	})
	step("stored parts: the layer table and the pool's blob counts", func() {
		c.storedLayers = &storedLayers{}
		c.pool.blobs = nil
	})
	step("kept calls: encodings", func() {
		for _, res := range c.resultsByID {
			for _, k := range res.keptCallsLocked() {
				k.encoded = ""
			}
		}
	})
	step("kept calls: identities, their maps and the input index", func() {
		for _, res := range c.resultsByID {
			res.calls = nil
		}
		c.callInputEntries = nil
	})
	say("left: the bookkeeping and the rest, %.3f GB", savedStoreGB(prev-base))
}

// savedStoreEntryCounts reports what the restored cache's entries hold, as counts and byte sums.
func savedStoreEntryCounts(c *Cache, say func(format string, args ...any)) {
	// Counts first.
	var (
		withBody, withBodyFrame, withDecodedFrame, withCalls, calls, callBytes, callInputs int
		deps, depParents, withStoredParts, storedParts, withPartFacts                      int
		bodyEnvBytes, bodyFrameBytes, bodyEnvRaw, bodyFrameRaw, refs                       int64
		descBytes, recordTypeBytes, recipeKeys, taught, holders, withCallTermIndex         int
		withUsageMaps, offerParents, listItems                                             int
		rootsWithCalls                                                                     int
	)
	descSeen := map[unique.Handle[string]]bool{}
	recordTypeSeen := map[unique.Handle[string]]bool{}
	for _, res := range c.resultsByID {
		if b := res.stored; b != nil {
			withBody++
			bodyEnvBytes += int64(b.envelopeLen)
			bodyEnvRaw += int64(b.envelopeBytes)
			if b.holdsFrame() {
				withBodyFrame++
				bodyFrameBytes += int64(len(b.frame()))
				bodyFrameRaw += int64(b.frameBytes)
			}
			for range b.refs.all() {
				refs++
			}
		}
		if res.resultCall != nil {
			withDecodedFrame++
		}
		if kept := res.keptCallsLocked(); len(kept) > 0 {
			withCalls++
			rootsWithCalls++
			calls += len(kept)
			for _, k := range kept {
				callBytes += len(k.encoded)
				callInputs += len(k.id.inputs)
			}
		}
		if res.calls != nil && res.calls.termIndex != nil {
			withCallTermIndex++
		}
		deps += len(res.deps)
		depParents += len(res.depParents)
		if len(res.storedParts) > 0 {
			withStoredParts++
			storedParts += len(res.storedParts)
		}
		if res.partFacts != nil {
			withPartFacts++
		}
		// Names are interned handles: each distinct name is held once.
		if !descSeen[res.description] {
			descSeen[res.description] = true
			descBytes += len(handleString(res.description))
		}
		if !recordTypeSeen[res.recordType] {
			recordTypeSeen[res.recordType] = true
			recordTypeBytes += len(handleString(res.recordType))
		}
		recipeKeys += len(res.recipeKeys)
		if res.taughtContent != nil {
			taught += len(res.taughtContent.digests)
		}
		if res.holders != nil {
			holders++
		}
		if res.cacheUsageSizeByIdentity != nil || res.cacheUsageRecordTypeByID != nil {
			withUsageMaps++
		}
		if res.offers != nil {
			offerParents += len(res.offers.parents)
		}
		if res.list != nil {
			listItems += len(res.list.items)
		}
	}
	say("entries with a body %d (with its frame compressed %d); entries still holding a decoded frame %d", withBody, withBodyFrame, withDecodedFrame)
	say("bodies: envelope %.3f GB compressed (%.3f GB as JSON), frame %.3f GB compressed (%.3f GB as JSON), refs %d", savedStoreGB(uint64(bodyEnvBytes)), savedStoreGB(uint64(bodyEnvRaw)), savedStoreGB(uint64(bodyFrameBytes)), savedStoreGB(uint64(bodyFrameRaw)), refs)
	say("kept calls: %d on %d entries, %.3f GB encoded, %d inputs; entries with a term index %d", calls, withCalls, savedStoreGB(uint64(callBytes)), callInputs, withCallTermIndex)
	say("deps %d, dep parents %d, persisted edges %d", deps, depParents, len(c.persistedEdgesByResult))
	say("stored parts %d on %d entries; part facts on %d", storedParts, withStoredParts, withPartFacts)
	say("description bytes (distinct) %d, recordType bytes (distinct) %d, recipe keys %d (entriesByRecipe %d), taught %d, with holders %d, with usage maps %d, offer parents %d, list items %d",
		descBytes, recordTypeBytes, recipeKeys, len(c.entriesByRecipe), taught, holders, withUsageMaps, offerParents, listItems)
	_ = rootsWithCalls
}

// savedStoreIndexCounts reports the sizes of the cache's indexes.
func savedStoreIndexCounts(c *Cache, say func(format string, args ...any)) {
	inner := func(m map[eqClassID]*idSet[string]) (n int) {
		for _, v := range m {
			n += v.Size()
		}
		return
	}
	digestStrings := map[unsafe.Pointer]int{}
	noteString := func(s string) {
		if p := unsafe.StringData(s); p != nil {
			digestStrings[unsafe.Pointer(p)] = len(s)
		}
	}
	for d := range c.egraphDigestToClass {
		noteString(d)
	}
	for _, v := range c.eqClassToDigests {
		for d := range v.All() {
			noteString(d)
		}
	}
	for d := range c.egraphResultsByDigest.Digests() {
		noteString(d)
	}
	var digestStringBytes int
	for _, n := range digestStrings {
		digestStringBytes += n
	}
	var postingExact, postingImported, postingsWithTree int
	postingExact = len(c.egraphResultsByDigest.one)
	for _, p := range c.egraphResultsByDigest.rest {
		postingImported += p.imported.Size()
		if p.exact != nil {
			postingsWithTree++
			postingExact += p.exact.Size()
		}
	}
	var rocInner, ocrInner, trInner, rtInner, ridInner, termsByDigestInner, inTermsInner, outTermsInner, termInputs, rdgGroups, cieInner int
	for _, v := range c.resultOutputEqClasses {
		rocInner += v.Size()
	}
	for _, v := range c.outputEqClassResults {
		ocrInner += v.Size()
	}
	for k := range c.termResults.Keys() {
		trInner += c.termResults.Size(k)
	}
	for k := range c.resultTerms.Keys() {
		rtInner += c.resultTerms.Size(k)
	}
	for _, v := range c.resultIndexedDigests {
		ridInner += len(v)
	}
	for _, v := range c.egraphTermsByTermDigest {
		termsByDigestInner += v.Size()
	}
	for _, v := range c.inputEqClassToTerms {
		inTermsInner += v.Size()
	}
	for _, v := range c.outputEqClassToTerms {
		outTermsInner += v.Size()
	}
	for _, term := range c.egraphTerms {
		termInputs += len(term.inputEqIDs)
	}
	for _, v := range c.restoredDigestGroups {
		rdgGroups += len(v)
	}
	for _, v := range c.callInputEntries {
		cieInner += len(v)
	}
	say("classes: live %d, slots %d; digest->class %d; class->digests %d classes, %d digests; extra-digest classes %d; distinct digest strings %d, %.3f GB of bytes",
		c.liveEqClasses, len(c.egraphParents), len(c.egraphDigestToClass), len(c.eqClassToDigests), inner(c.eqClassToDigests), len(c.eqClassExtraDigests), len(digestStrings), savedStoreGB(uint64(digestStringBytes)))
	say("terms %d (inputs %d); by term digest %d keys, %d ids; input class->terms %d/%d; output class->terms %d/%d; input provenance %d",
		len(c.egraphTerms), termInputs, len(c.egraphTermsByTermDigest), termsByDigestInner, len(c.inputEqClassToTerms), inTermsInner, len(c.outputEqClassToTerms), outTermsInner, len(c.termInputProvenance))
	say("result->classes %d/%d; class->results %d/%d; term->results %d/%d; result->terms %d/%d", len(c.resultOutputEqClasses), rocInner, len(c.outputEqClassResults), ocrInner, c.termResults.Len(), trInner, c.resultTerms.Len(), rtInner)
	say("digest postings %d (with a tree %d: %d ids; imported ids %d); result->indexed digests %d/%d; broadly indexed %d; restored digest groups %d/%d; call input entries %d/%d",
		c.egraphResultsByDigest.Len(), postingsWithTree, postingExact, postingImported, len(c.resultIndexedDigests), ridInner, len(c.broadlyIndexedResults), len(c.restoredDigestGroups), rdgGroups, len(c.callInputEntries), cieInner)
}

// savedStoreCompositionSteps lets go of each kind of thing the entries hold in
// turn, and reports what each frees. The cache is unusable afterwards.
func savedStoreCompositionSteps(c *Cache, base uint64, say func(format string, args ...any)) {
	// Then what each kind costs, let go of in turn.
	prev := savedStoreHeap()
	count := len(c.resultsByID)
	step := func(name string, drop func()) {
		drop()
		now := savedStoreHeap()
		freed := int64(prev) - int64(now)
		say("STEP %-58s freed %7.3f GB  %6.0f B an entry", name, float64(freed)/1e9, float64(freed)/float64(count))
		prev = now
	}
	entries := len(c.resultsByID)

	// A body holds its envelope and frame in one allocation: the envelopes'
	// step lets go of both, and the frames' step of nothing. Both steps stay
	// so that reports compare line by line.
	step("bodies: compressed envelopes' bytes", func() {
		for _, res := range c.resultsByID {
			if b := res.stored; b != nil {
				b.bytes = nil
			}
		}
	})
	step("bodies: compressed frames' bytes", func() {})
	step("bodies: the rest (body structs, refs, light envelopes, transfer copies)", func() {
		for _, res := range c.resultsByID {
			res.stored = nil
			res.storedTransfer.Store(nil)
			res.persistedEnvelope = nil
		}
	})
	step("decoded frames still held", func() {
		for _, res := range c.resultsByID {
			res.resultCall = nil
			if res.taughtContent != nil {
				res.taughtContent.from = entryFrame{}
			}
		}
	})
	step("part facts (declared and complete part keys)", func() {
		for _, res := range c.resultsByID {
			res.partFacts = nil
			res.completeParts.Store(nil)
			res.unavailableParts = nil
		}
	})
	step("stored parts and their layers, the pool's blob counts", func() {
		for _, res := range c.resultsByID {
			res.storedParts = nil
		}
		c.storedLayers = &storedLayers{}
		c.pool.blobs = nil
	})
	step("kept calls' encodings", func() {
		for _, res := range c.resultsByID {
			for _, k := range res.keptCallsLocked() {
				k.encoded = ""
			}
		}
	})
	step("kept calls' identities, their maps and input index", func() {
		for _, res := range c.resultsByID {
			res.calls = nil
		}
		c.callInputEntries = nil
	})
	step("labels, stamps' maps, taught digests, holders, runs", func() {
		for _, res := range c.resultsByID {
			res.description = unique.Handle[string]{}
			res.recordType = unique.Handle[string]{}
			res.cacheUsageSizeByIdentity = nil
			res.cacheUsageRecordTypeByID = nil
			res.taughtContent = nil
			res.holders = nil
			res.run = nil
			res.offers = nil
			res.list = nil
		}
	})
	step("dependency edges: the reverse direction (depParents)", func() {
		for _, res := range c.resultsByID {
			res.depParents = nil
		}
	})
	step("dependency edges: the forward direction (deps), persisted edges", func() {
		for _, res := range c.resultsByID {
			res.deps = nil
		}
		c.persistedEdgesByResult = nil
	})
	step("recipe index (recipe keys, entriesByRecipe)", func() {
		for _, res := range c.resultsByID {
			res.recipeKeys = nil
		}
		c.entriesByRecipe = nil
	})
	step("digest postings (digest->entries) and their reverse", func() {
		c.egraphResultsByDigest = digestPostings{}
		c.resultIndexedDigests = nil
		c.broadlyIndexedResults = nil
		c.restoredDigestGroups = nil
	})
	step("entry<->class and entry<->term associations", func() {
		c.resultOutputEqClasses = nil
		c.outputEqClassResults = nil
		c.termResults = idSetIndex[egraphTermID, sharedResultID]{}
		c.resultTerms = idSetIndex[sharedResultID, egraphTermID]{}
	})
	step("terms and their indexes", func() {
		c.egraphTerms = nil
		c.egraphTermsByTermDigest = nil
		c.inputEqClassToTerms = nil
		c.outputEqClassToTerms = nil
		c.termInputProvenance = nil
	})
	step("classes: digest->class, class->digests, extras, union-find", func() {
		c.egraphDigestToClass = nil
		c.eqClassToDigests = nil
		c.eqClassExtraDigests = nil
		c.egraphParents = nil
		c.egraphRanks = nil
		c.egraphUses = nil
		c.freeEqClassIDs = nil
	})
	step("the entries' own structs", func() {
		c.resultsByID = nil
		c.restoredTaught = nil
	})
	// What is left of the cache beside its entries.
	left := prev - base
	say("left beside the entries (the cache's own state, test overhead): %.3f GB", savedStoreGB(left))
	say("entries %d", entries)
}

// savedStoreKeptCallCompression reports how kept calls' encodings compress,
// one at a time and an entry's together.
func savedStoreKeptCallCompression(t *testing.T, c *Cache, say func(format string, args ...any)) {
	// Kept calls: how their encodings compress, alone and an entry's together.
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(storedBodyLevel), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	var calls, callEntries, encBytes, zAlone, zEntry int
	var groups [][]byte
	prefix := ""
	for _, res := range c.resultsByID {
		ks := res.keptCallsLocked()
		if len(ks) == 0 {
			continue
		}
		callEntries++
		var group []byte
		for _, k := range ks {
			calls++
			encBytes += len(k.encoded)
			zAlone += len(enc.EncodeAll([]byte(k.encoded), nil))
			group = append(group, k.encoded...)
			if prefix == "" && len(k.encoded) > 24 {
				prefix = k.encoded[:24]
			}
		}
		zEntry += len(enc.EncodeAll(group, nil))
		groups = append(groups, group)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	var squeezed [][]byte
	cStart := time.Now()
	for _, g := range groups {
		squeezed = append(squeezed, enc.EncodeAll(g, nil))
	}
	cTook := time.Since(cStart)
	dStart := time.Now()
	for _, z := range squeezed {
		if _, err := dec.DecodeAll(z, nil); err != nil {
			t.Fatal(err)
		}
	}
	dTook := time.Since(dStart)
	say("an entry's kept calls together: compress %.1f us an entry, decompress %.1f us an entry, over %d entries, one goroutine", float64(cTook.Microseconds())/float64(max(1, int64(len(groups)))), float64(dTook.Microseconds())/float64(max(1, int64(len(groups)))), len(groups))
	say("kept calls: %d on %d entries; encodings %.3f GB; zstd each alone %.3f GB (%.2fx); an entry's together %.3f GB (%.2fx); an encoding starts %q",
		calls, callEntries, savedStoreGB(uint64(encBytes)), savedStoreGB(uint64(zAlone)), float64(encBytes)/float64(max(1, int64(zAlone))), savedStoreGB(uint64(zEntry)), float64(encBytes)/float64(max(1, int64(zEntry))), prefix)
}

// savedStoreRecency reports what share of the records' bytes the most
// recently used entries hold.
func savedStoreRecency(c *Cache, n int, say func(format string, args ...any)) {
	// The records' bytes by recency: entries ordered by their last use (or
	// storing), newest first, and the share of compressed record and kept-call
	// bytes the newest quarter holds.
	type rec struct {
		at    int64
		bytes int64
	}
	recs := make([]rec, 0, n)
	var all int64
	for _, res := range c.resultsByID {
		var b int64
		if s := res.stored; s != nil {
			b += int64(len(s.bytes))
		}
		for _, k := range res.keptCallsLocked() {
			b += int64(len(k.encoded))
		}
		at := res.lastUsedAtUnixNano
		if res.createdAtUnixNano > at {
			at = res.createdAtUnixNano
		}
		recs = append(recs, rec{at: at, bytes: b})
		all += b
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].at > recs[j].at })
	for _, f := range []float64{0.10, 0.25, 0.50} {
		k := int(float64(len(recs)) * f)
		var s int64
		for _, r := range recs[:k] {
			s += r.bytes
		}
		say("newest %.0f%% of entries by last use hold %.1f%% of the record and kept-call bytes (%.3f of %.3f GB)", f*100, 100*float64(s)/float64(max(1, all)), savedStoreGB(uint64(s)), savedStoreGB(uint64(all)))
	}
}

func TestSavedStoreTimes(t *testing.T) {
	if *savedStorePath == "" || *savedStoreOut == "" {
		t.Skip("no -savedstore or -savedstore.out")
	}
	var report strings.Builder
	say := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		fmt.Println(line)
		report.WriteString(line + "\n")
	}
	defer func() {
		_ = os.WriteFile(filepath.Join(*savedStoreOut, "times.txt"), []byte(report.String()), 0o644)
	}()
	dir := t.TempDir()
	path := filepath.Join(dir, "org.db")
	savedStoreCopy(t, *savedStorePath, path)
	ctx := context.Background()
	start := time.Now()
	c, err := NewCache(ctx, path, nil, nil, WithBlobStore())
	if err != nil {
		t.Fatal(err)
	}
	savedStoreRestored(t, c)
	say("opened in %s", time.Since(start).Round(time.Millisecond))
	time.Sleep(20 * time.Second)

	c.egraphMu.RLock()
	var ids []uint64
	for id, res := range c.resultsByID {
		if len(res.keptCallsLocked()) > 0 && res.stored != nil {
			ids = append(ids, uint64(id))
		}
	}
	c.egraphMu.RUnlock()
	slices.Sort(ids)
	ids = ids[:min(len(ids), 4000)]
	for rep := range 3 {
		calls, values := 0, 0
		if rep == 1 {
			f, err := os.Create(filepath.Join(*savedStoreOut, "bundles-cpu.pprof"))
			if err != nil {
				t.Fatal(err)
			}
			if err := pprof.StartCPUProfile(f); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		for _, id := range ids {
			built, err := c.StoredBundle(ctx, []uint64{id}, time.Now().Unix(), nil)
			if err != nil {
				continue
			}
			calls += len(built.Bundle.Calls)
			values += len(built.Bundle.Values)
		}
		took := time.Since(start)
		if rep == 1 {
			pprof.StopCPUProfile()
		}
		say("bundles of %d entries that keep calls, run %d: %s, %.1f us a bundle; %d calls and %d values sent", len(ids), rep, took.Round(time.Millisecond), float64(took.Microseconds())/float64(len(ids)), calls, values)
	}
	for rep := range 3 {
		start := time.Now()
		snap, err := c.snapshotPersistState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		took := time.Since(start)
		withCalls := 0
		for _, r := range snap.results {
			if r.write&persistWriteCalls != 0 {
				withCalls++
			}
		}
		say("a save's read and assembly, run %d: %s; %d entries to write, %d of them their calls", rep, took.Round(time.Millisecond), len(snap.results), withCalls)
	}
}
