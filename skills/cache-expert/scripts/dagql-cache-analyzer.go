package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

type resultNode struct {
	ID                    uint64   `json:"shared_result_id"`
	OutputEqClassIDs      []uint64 `json:"output_eq_class_ids"`
	RecordType            string   `json:"record_type"`
	Description           string   `json:"description"`
	TypeName              string   `json:"type_name"`
	RefCount              int64    `json:"ref_count"`
	HasValue              bool     `json:"has_value"`
	PayloadState          string   `json:"payload_state"`
	DepOfPersistedResult  bool     `json:"dep_of_persisted_result"`
	ExplicitDeps          []uint64 `json:"explicit_dep_ids"`
	HeldDependencyResults int      `json:"held_dependency_results_count"`
	SafeToPersistCache    bool     `json:"safe_to_persist_cache"`
	ExpiresAtUnix         int64    `json:"expires_at_unix"`
	SizeEstimateBytes     int64    `json:"size_estimate_bytes"`
	UsageIdentity         string   `json:"usage_identity"`

	Deps []uint64 `json:"-"`
}

type termNode struct {
	ID         uint64   `json:"term_id"`
	InputEqIDs []uint64 `json:"input_eq_ids"`
}

type resultTerm struct {
	ResultID         uint64                  `json:"shared_result_id"`
	TermID           uint64                  `json:"term_id"`
	InputProvenance  []resultInputProvenance `json:"input_provenance"`
	resultBackedSlot []int                   `json:"-"`
}

type resultInputProvenance struct {
	Kind string `json:"kind"`
}

type categoryKey struct {
	TypeName   string
	RecordType string
}

type graphSummary struct {
	NodeCount      int
	KnownBytes     int64
	KnownByteNodes int
	ByType         map[string]int
	ByRecord       map[string]int
}

type analyzer struct {
	path string

	results   map[uint64]*resultNode
	terms     map[uint64]*termNode
	resultOps map[uint64][]resultTerm

	resultsCount             int
	resultDigestIndexesCount int
	termsCount               int
	resultTermsCount         int
	digestsCount             int
	eqClassesCount           int

	edgeCountExplicit          int
	edgeCountStructural        int
	edgeCountStructuralMissing int
}

func main() {
	var (
		topN               = flag.Int("top", 15, "number of top categories to print")
		liveGroupLimit     = flag.Int("live-group-limit", 12, "number of live root groups to compute cumulative closures for")
		persistedRootLimit = flag.Int("persisted-root-limit", 20, "number of persisted roots to print by cumulative size")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: go run ./skills/cache-expert/scripts/dagql-cache-analyzer.go [flags] <snapshot.json>\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	start := time.Now()
	a := &analyzer{
		path:      flag.Arg(0),
		results:   make(map[uint64]*resultNode),
		terms:     make(map[uint64]*termNode),
		resultOps: make(map[uint64][]resultTerm),
	}
	if err := a.load(); err != nil {
		fmt.Fprintf(os.Stderr, "load snapshot: %v\n", err)
		os.Exit(1)
	}
	a.buildGraph()

	liveRoots, persistedRoots, orphanZeroRefRoots := a.classifyRoots()
	a.printOverview(start, liveRoots, persistedRoots, orphanZeroRefRoots)
	a.printResultCounters(*topN)
	a.printRootCounters("live roots", liveRoots, *topN)
	a.printRootCounters("persisted roots", persistedRoots, *topN)

	liveGroups := a.topRootGroups(liveRoots, *liveGroupLimit)
	a.printGroupedClosures("live root groups", liveGroups)

	persistedSummaries := a.rootClosures(persistedRoots)
	a.printTopRootClosures("persisted roots", persistedSummaries, *persistedRootLimit)
}

func (a *analyzer) load() error {
	f, err := os.Open(a.path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("expected top-level object")
	}

	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("expected object key, got %T", keyTok)
		}

		switch key {
		case "results":
			if err := a.decodeArray(dec, func() error {
				var r resultNode
				if err := dec.Decode(&r); err != nil {
					return err
				}
				a.results[r.ID] = &r
				a.resultsCount++
				return nil
			}); err != nil {
				return fmt.Errorf("decode results: %w", err)
			}
		case "terms":
			if err := a.decodeArray(dec, func() error {
				var t termNode
				if err := dec.Decode(&t); err != nil {
					return err
				}
				a.terms[t.ID] = &t
				a.termsCount++
				return nil
			}); err != nil {
				return fmt.Errorf("decode terms: %w", err)
			}
		case "result_terms":
			if err := a.decodeArray(dec, func() error {
				var rt resultTerm
				if err := dec.Decode(&rt); err != nil {
					return err
				}
				for i, prov := range rt.InputProvenance {
					if prov.Kind == "result" {
						rt.resultBackedSlot = append(rt.resultBackedSlot, i)
					}
				}
				a.resultOps[rt.ResultID] = append(a.resultOps[rt.ResultID], rt)
				a.resultTermsCount++
				return nil
			}); err != nil {
				return fmt.Errorf("decode result_terms: %w", err)
			}
		case "result_digest_indexes":
			if err := a.countArray(dec, &a.resultDigestIndexesCount); err != nil {
				return fmt.Errorf("decode result_digest_indexes: %w", err)
			}
		case "digests":
			if err := a.countArray(dec, &a.digestsCount); err != nil {
				return fmt.Errorf("decode digests: %w", err)
			}
		case "eq_classes":
			if err := a.countArray(dec, &a.eqClassesCount); err != nil {
				return fmt.Errorf("decode eq_classes: %w", err)
			}
		default:
			var discard json.RawMessage
			if err := dec.Decode(&discard); err != nil {
				return fmt.Errorf("decode %s: %w", key, err)
			}
		}
	}

	_, err = dec.Token()
	return err
}

func (a *analyzer) decodeArray(dec *json.Decoder, decodeElem func() error) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '[' {
		return fmt.Errorf("expected array")
	}
	for dec.More() {
		if err := decodeElem(); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

func (a *analyzer) countArray(dec *json.Decoder, dst *int) error {
	return a.decodeArray(dec, func() error {
		var discard json.RawMessage
		if err := dec.Decode(&discard); err != nil {
			return err
		}
		*dst++
		return nil
	})
}

func (a *analyzer) buildGraph() {
	minResultForEq := make(map[uint64]uint64, len(a.results))
	for id, res := range a.results {
		for _, eqID := range res.OutputEqClassIDs {
			prev := minResultForEq[eqID]
			if prev == 0 || id < prev {
				minResultForEq[eqID] = id
			}
		}
	}

	for _, res := range a.results {
		depSet := make(map[uint64]struct{}, len(res.ExplicitDeps)+4)
		for _, dep := range res.ExplicitDeps {
			if dep == 0 || dep == res.ID {
				continue
			}
			depSet[dep] = struct{}{}
		}
		a.edgeCountExplicit += len(depSet)

		for _, op := range a.resultOps[res.ID] {
			term := a.terms[op.TermID]
			if term == nil {
				continue
			}
			for _, slot := range op.resultBackedSlot {
				if slot >= len(term.InputEqIDs) {
					continue
				}
				dep := minResultForEq[term.InputEqIDs[slot]]
				if dep == 0 {
					a.edgeCountStructuralMissing++
					continue
				}
				if dep == res.ID {
					continue
				}
				if _, ok := depSet[dep]; !ok {
					a.edgeCountStructural++
					depSet[dep] = struct{}{}
				}
			}
		}

		if len(depSet) == 0 {
			continue
		}
		res.Deps = make([]uint64, 0, len(depSet))
		for dep := range depSet {
			res.Deps = append(res.Deps, dep)
		}
		sort.Slice(res.Deps, func(i, j int) bool { return res.Deps[i] < res.Deps[j] })
	}
}

func (a *analyzer) classifyRoots() (liveRoots, persistedRoots, orphanZeroRefRoots []uint64) {
	incoming := make(map[uint64]int, len(a.results))
	for _, res := range a.results {
		for _, dep := range res.Deps {
			incoming[dep]++
		}
	}

	for id, res := range a.results {
		if incoming[id] != 0 {
			continue
		}
		switch {
		case res.RefCount > 0 && res.DepOfPersistedResult:
			persistedRoots = append(persistedRoots, id)
		case res.RefCount > 0:
			liveRoots = append(liveRoots, id)
		case res.DepOfPersistedResult:
			persistedRoots = append(persistedRoots, id)
		default:
			orphanZeroRefRoots = append(orphanZeroRefRoots, id)
		}
	}

	sort.Slice(liveRoots, func(i, j int) bool { return liveRoots[i] < liveRoots[j] })
	sort.Slice(persistedRoots, func(i, j int) bool { return persistedRoots[i] < persistedRoots[j] })
	sort.Slice(orphanZeroRefRoots, func(i, j int) bool { return orphanZeroRefRoots[i] < orphanZeroRefRoots[j] })
	return
}

func (a *analyzer) printOverview(start time.Time, liveRoots, persistedRoots, orphanZeroRefRoots []uint64) {
	refBuckets := map[string]int{}
	refPositive := 0
	refPositiveNonPersisted := 0
	depOfPersisted := 0
	hasValue := 0
	knownSizeCount := 0
	var knownSizeTotal int64
	for _, res := range a.results {
		switch rc := res.RefCount; {
		case rc == 0:
			refBuckets["0"]++
		case rc == 1:
			refBuckets["1"]++
		case rc <= 5:
			refBuckets["2-5"]++
		case rc <= 20:
			refBuckets["6-20"]++
		default:
			refBuckets["21+"]++
		}
		if res.RefCount > 0 {
			refPositive++
			if !res.DepOfPersistedResult {
				refPositiveNonPersisted++
			}
		}
		if res.DepOfPersistedResult {
			depOfPersisted++
		}
		if res.HasValue {
			hasValue++
		}
		if res.SizeEstimateBytes > 0 {
			knownSizeCount++
			knownSizeTotal += res.SizeEstimateBytes
		}
	}

	fmt.Printf("Snapshot: %s\n", a.path)
	fmt.Printf("Loaded in: %s\n\n", time.Since(start).Round(time.Millisecond))

	fmt.Println("Counts")
	fmt.Printf("- results: %d\n", a.resultsCount)
	fmt.Printf("- terms: %d\n", a.termsCount)
	fmt.Printf("- result_terms: %d\n", a.resultTermsCount)
	fmt.Printf("- result_digest_indexes: %d\n", a.resultDigestIndexesCount)
	fmt.Printf("- digests: %d\n", a.digestsCount)
	fmt.Printf("- eq_classes: %d\n", a.eqClassesCount)
	fmt.Printf("- edges (explicit): %d\n", a.edgeCountExplicit)
	fmt.Printf("- edges (structural): %d\n", a.edgeCountStructural)
	fmt.Printf("- edges (structural missing representative): %d\n", a.edgeCountStructuralMissing)
	fmt.Println()

	fmt.Println("Retention Shape")
	fmt.Printf("- dep_of_persisted_result=true: %d\n", depOfPersisted)
	fmt.Printf("- ref_count>0: %d\n", refPositive)
	fmt.Printf("- ref_count>0 && !dep_of_persisted_result: %d\n", refPositiveNonPersisted)
	fmt.Printf("- has_value=true: %d\n", hasValue)
	fmt.Printf("- live roots (ref_count>0, no incoming deps): %d\n", len(liveRoots))
	fmt.Printf("- persisted roots (dep_of_persisted_result, no incoming deps): %d\n", len(persistedRoots))
	fmt.Printf("- orphan zero-ref non-persisted roots: %d\n", len(orphanZeroRefRoots))
	fmt.Printf("- known nonzero size estimates: %d results, %s total\n", knownSizeCount, humanBytes(knownSizeTotal))
	fmt.Printf("- refcount buckets: 0=%d 1=%d 2-5=%d 6-20=%d 21+=%d\n",
		refBuckets["0"], refBuckets["1"], refBuckets["2-5"], refBuckets["6-20"], refBuckets["21+"])
	fmt.Println()
}

func (a *analyzer) printResultCounters(topN int) {
	typeCounts := make(map[string]int)
	recordCounts := make(map[string]int)
	persistedTypeCounts := make(map[string]int)
	persistedRecordCounts := make(map[string]int)
	liveTypeCounts := make(map[string]int)
	liveRecordCounts := make(map[string]int)

	for _, res := range a.results {
		typeCounts[nz(res.TypeName)]++
		recordCounts[nz(res.RecordType)]++
		if res.DepOfPersistedResult {
			persistedTypeCounts[nz(res.TypeName)]++
			persistedRecordCounts[nz(res.RecordType)]++
		}
		if res.RefCount > 0 {
			liveTypeCounts[nz(res.TypeName)]++
			liveRecordCounts[nz(res.RecordType)]++
		}
	}

	fmt.Println("Top Types Overall")
	printCounter(typeCounts, topN)
	fmt.Println()

	fmt.Println("Top Record Types Overall")
	printCounter(recordCounts, topN)
	fmt.Println()

	fmt.Println("Top Types Among Live Results")
	printCounter(liveTypeCounts, topN)
	fmt.Println()

	fmt.Println("Top Record Types Among Live Results")
	printCounter(liveRecordCounts, topN)
	fmt.Println()

	fmt.Println("Top Types Among Persisted Closure Results")
	printCounter(persistedTypeCounts, topN)
	fmt.Println()

	fmt.Println("Top Record Types Among Persisted Closure Results")
	printCounter(persistedRecordCounts, topN)
	fmt.Println()
}

func (a *analyzer) printRootCounters(title string, rootIDs []uint64, topN int) {
	typeCounts := make(map[string]int)
	recordCounts := make(map[string]int)
	categoryCounts := make(map[categoryKey]int)
	for _, id := range rootIDs {
		res := a.results[id]
		if res == nil {
			continue
		}
		typeCounts[nz(res.TypeName)]++
		recordCounts[nz(res.RecordType)]++
		categoryCounts[categoryKey{TypeName: nz(res.TypeName), RecordType: nz(res.RecordType)}]++
	}

	fmt.Printf("Top %s by Type\n", strings.Title(title))
	printCounter(typeCounts, topN)
	fmt.Println()

	fmt.Printf("Top %s by Record Type\n", strings.Title(title))
	printCounter(recordCounts, topN)
	fmt.Println()

	fmt.Printf("Top %s by Type+Record\n", strings.Title(title))
	printCategoryCounter(categoryCounts, topN)
	fmt.Println()
}

func (a *analyzer) topRootGroups(rootIDs []uint64, limit int) []groupSummary {
	counts := make(map[categoryKey][]uint64)
	for _, id := range rootIDs {
		res := a.results[id]
		if res == nil {
			continue
		}
		key := categoryKey{TypeName: nz(res.TypeName), RecordType: nz(res.RecordType)}
		counts[key] = append(counts[key], id)
	}

	groups := make([]groupSummary, 0, len(counts))
	for key, ids := range counts {
		groups = append(groups, groupSummary{Key: key, RootIDs: ids})
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].RootIDs) != len(groups[j].RootIDs) {
			return len(groups[i].RootIDs) > len(groups[j].RootIDs)
		}
		if groups[i].Key.TypeName != groups[j].Key.TypeName {
			return groups[i].Key.TypeName < groups[j].Key.TypeName
		}
		return groups[i].Key.RecordType < groups[j].Key.RecordType
	})
	if limit > 0 && len(groups) > limit {
		groups = groups[:limit]
	}
	for i := range groups {
		groups[i].Summary = a.closure(groups[i].RootIDs)
	}
	return groups
}

type groupSummary struct {
	Key     categoryKey
	RootIDs []uint64
	Summary graphSummary
}

func (a *analyzer) printGroupedClosures(title string, groups []groupSummary) {
	fmt.Printf("Top %s by cumulative closure\n", title)
	for _, group := range groups {
		fmt.Printf("- %s / %s: roots=%d reachable=%d known_bytes=%s known_byte_nodes=%d top_types=%s\n",
			group.Key.TypeName,
			group.Key.RecordType,
			len(group.RootIDs),
			group.Summary.NodeCount,
			humanBytes(group.Summary.KnownBytes),
			group.Summary.KnownByteNodes,
			topCounterString(group.Summary.ByType, 5),
		)
	}
	fmt.Println()
}

type rootSummary struct {
	ID      uint64
	Summary graphSummary
}

func (a *analyzer) rootClosures(rootIDs []uint64) []rootSummary {
	summaries := make([]rootSummary, 0, len(rootIDs))
	for _, id := range rootIDs {
		summaries = append(summaries, rootSummary{
			ID:      id,
			Summary: a.closure([]uint64{id}),
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Summary.NodeCount != summaries[j].Summary.NodeCount {
			return summaries[i].Summary.NodeCount > summaries[j].Summary.NodeCount
		}
		if summaries[i].Summary.KnownBytes != summaries[j].Summary.KnownBytes {
			return summaries[i].Summary.KnownBytes > summaries[j].Summary.KnownBytes
		}
		return summaries[i].ID < summaries[j].ID
	})
	return summaries
}

func (a *analyzer) printTopRootClosures(title string, roots []rootSummary, limit int) {
	fmt.Printf("Top %s by cumulative closure\n", title)
	if limit > len(roots) {
		limit = len(roots)
	}
	for _, root := range roots[:limit] {
		res := a.results[root.ID]
		if res == nil {
			continue
		}
		fmt.Printf("- id=%d type=%s record=%s desc=%s refcount=%d reachable=%d known_bytes=%s known_byte_nodes=%d top_types=%s\n",
			root.ID,
			nz(res.TypeName),
			nz(res.RecordType),
			nz(res.Description),
			res.RefCount,
			root.Summary.NodeCount,
			humanBytes(root.Summary.KnownBytes),
			root.Summary.KnownByteNodes,
			topCounterString(root.Summary.ByType, 5),
		)
	}
	fmt.Println()
}

func (a *analyzer) closure(rootIDs []uint64) graphSummary {
	visited := make(map[uint64]struct{}, len(rootIDs)*4)
	stack := append([]uint64(nil), rootIDs...)
	summary := graphSummary{
		ByType:   make(map[string]int),
		ByRecord: make(map[string]int),
	}

	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := visited[id]; seen {
			continue
		}
		visited[id] = struct{}{}
		res := a.results[id]
		if res == nil {
			continue
		}

		summary.NodeCount++
		summary.ByType[nz(res.TypeName)]++
		summary.ByRecord[nz(res.RecordType)]++
		if res.SizeEstimateBytes > 0 {
			summary.KnownBytes += res.SizeEstimateBytes
			summary.KnownByteNodes++
		}

		for _, dep := range res.Deps {
			if _, seen := visited[dep]; !seen {
				stack = append(stack, dep)
			}
		}
	}

	return summary
}

func printCounter(counter map[string]int, topN int) {
	type item struct {
		Key   string
		Count int
	}
	items := make([]item, 0, len(counter))
	for key, count := range counter {
		items = append(items, item{Key: key, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Key < items[j].Key
	})
	if topN > len(items) {
		topN = len(items)
	}
	for _, item := range items[:topN] {
		fmt.Printf("- %s: %d\n", item.Key, item.Count)
	}
}

func printCategoryCounter(counter map[categoryKey]int, topN int) {
	type item struct {
		Key   categoryKey
		Count int
	}
	items := make([]item, 0, len(counter))
	for key, count := range counter {
		items = append(items, item{Key: key, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		if items[i].Key.TypeName != items[j].Key.TypeName {
			return items[i].Key.TypeName < items[j].Key.TypeName
		}
		return items[i].Key.RecordType < items[j].Key.RecordType
	})
	if topN > len(items) {
		topN = len(items)
	}
	for _, item := range items[:topN] {
		fmt.Printf("- %s / %s: %d\n", item.Key.TypeName, item.Key.RecordType, item.Count)
	}
}

func topCounterString(counter map[string]int, topN int) string {
	type item struct {
		Key   string
		Count int
	}
	items := make([]item, 0, len(counter))
	for key, count := range counter {
		items = append(items, item{Key: key, Count: count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count != items[j].Count {
			return items[i].Count > items[j].Count
		}
		return items[i].Key < items[j].Key
	})
	if topN > len(items) {
		topN = len(items)
	}
	parts := make([]string, 0, topN)
	for _, item := range items[:topN] {
		parts = append(parts, fmt.Sprintf("%s=%d", item.Key, item.Count))
	}
	return strings.Join(parts, ", ")
}

func nz(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func humanBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
