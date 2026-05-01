package dagui

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
	"time"
)

// TestStatus is the normalized semantic status for a test span.
type TestStatus string

const (
	TestStatusUnset      TestStatus = ""
	TestStatusSuccess    TestStatus = "success"
	TestStatusFailure    TestStatus = "failure"
	TestStatusSkipped    TestStatus = "skipped"
	TestStatusAborted    TestStatus = "aborted"
	TestStatusTimedOut   TestStatus = "timed_out"
	TestStatusInProgress TestStatus = "in_progress"
)

func normalizeTestStatus(raw string) TestStatus {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "success", "successful", "pass", "passed", "ok":
		return TestStatusSuccess
	case "failure", "failed", "fail", "error":
		return TestStatusFailure
	case "skipped", "skip":
		return TestStatusSkipped
	case "aborted", "abort":
		return TestStatusAborted
	case "timed_out", "timed-out", "timeout", "timedout":
		return TestStatusTimedOut
	case "in_progress", "in-progress", "running":
		return TestStatusInProgress
	default:
		return TestStatusUnset
	}
}

func mergeTestStatus(current, next TestStatus) TestStatus {
	if next == TestStatusUnset {
		return current
	}
	if testStatusPriority(next) >= testStatusPriority(current) {
		return next
	}
	return current
}

func testStatusPriority(status TestStatus) int {
	switch status {
	case TestStatusFailure, TestStatusTimedOut, TestStatusAborted:
		return 4
	case TestStatusSkipped:
		return 3
	case TestStatusInProgress:
		return 2
	case TestStatusSuccess:
		return 1
	default:
		return 0
	}
}

func (s TestStatus) IsFailing() bool {
	switch s {
	case TestStatusFailure, TestStatusTimedOut, TestStatusAborted:
		return true
	default:
		return false
	}
}

func (s TestStatus) IsSkipped() bool {
	return s == TestStatusSkipped
}

func (s TestStatus) IsRunning() bool {
	return s == TestStatusInProgress
}

func (s TestStatus) IsSuccess() bool {
	return s == TestStatusSuccess
}

// TestCategory is the aggregate/render category for a test node.
type TestCategory uint8

const (
	TestCategoryFailing TestCategory = iota
	TestCategoryRunning
	TestCategorySkipped
	TestCategoryPassing
	TestCategoryMixed
)

func (c TestCategory) String() string {
	switch c {
	case TestCategoryFailing:
		return "failing"
	case TestCategoryRunning:
		return "running"
	case TestCategorySkipped:
		return "skipped"
	case TestCategoryPassing:
		return "passing"
	case TestCategoryMixed:
		return "mixed"
	default:
		return "unknown"
	}
}

// TestCounts tallies counted test cases by render category.
type TestCounts struct {
	Failing int
	Running int
	Passing int
	Skipped int
}

func (c TestCounts) Total() int {
	return c.Failing + c.Running + c.Passing + c.Skipped
}

func (c TestCounts) isZero() bool {
	return c == TestCounts{}
}

func (c TestCounts) add(other TestCounts) TestCounts {
	return TestCounts{
		Failing: c.Failing + other.Failing,
		Running: c.Running + other.Running,
		Passing: c.Passing + other.Passing,
		Skipped: c.Skipped + other.Skipped,
	}
}

func (c TestCounts) sub(other TestCounts) TestCounts {
	return TestCounts{
		Failing: c.Failing - other.Failing,
		Running: c.Running - other.Running,
		Passing: c.Passing - other.Passing,
		Skipped: c.Skipped - other.Skipped,
	}
}

func countForCategory(category TestCategory) TestCounts {
	switch category {
	case TestCategoryFailing:
		return TestCounts{Failing: 1}
	case TestCategoryRunning:
		return TestCounts{Running: 1}
	case TestCategorySkipped:
		return TestCounts{Skipped: 1}
	default:
		return TestCounts{Passing: 1}
	}
}

// TestNodeKind identifies whether a test node is a real test case, a real
// suite span, or a synthetic suite grouping.
type TestNodeKind uint8

const (
	TestNodeCase TestNodeKind = iota
	TestNodeSuite
	TestNodeVirtualSuite
)

type TestNodeID string

type TestNode struct {
	ID   TestNodeID
	Kind TestNodeKind

	// Name is the local display name for this node, usually the span name. For
	// test cases this avoids repeating a fully-qualified semantic test name at
	// every level of a rendered tree.
	Name string

	// FullName is the fully-qualified semantic name reported by test.case.name
	// or test.suite.name. Use this for stable lookups and URL compatibility.
	FullName string

	// Span is nil for virtual suites.
	Span *Span

	Parent   *TestNode
	Children []*TestNode

	// RepresentativeSpan is non-nil for virtual suites when children exist.
	RepresentativeSpan *Span

	SelfCategory TestCategory
	Category     TestCategory
	Counts       TestCounts
	MaxDuration  time.Duration

	suiteName    string
	selfDuration time.Duration
}

type TestView struct {
	Roots []*TestNode

	ByID         map[TestNodeID]*TestNode
	BySpan       map[SpanID]*TestNode
	CasesByName  map[string][]*TestNode
	SuitesByName map[string][]*TestNode

	Counts      TestCounts
	MaxDuration time.Duration
}

func (v *TestView) HasTests() bool {
	return v != nil && len(v.Roots) > 0
}

func (v *TestView) FindCaseByName(name string) *TestNode {
	if v == nil {
		return nil
	}
	matches := v.CasesByName[name]
	if len(matches) == 0 {
		return nil
	}
	return matches[0]
}

func (v *TestView) FindSuiteByName(name string) *TestNode {
	if v == nil {
		return nil
	}
	matches := v.SuitesByName[name]
	if len(matches) == 0 {
		return nil
	}
	return matches[0]
}

type TestPartition struct {
	Failing []*TestNode
	Running []*TestNode
	Suites  []*TestNode
	Mixed   []*TestNode
	Passing []*TestNode
	Skipped []*TestNode
}

func PartitionTests(nodes []*TestNode) TestPartition {
	var partition TestPartition
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.Kind == TestNodeSuite || node.Kind == TestNodeVirtualSuite {
			partition.Suites = append(partition.Suites, node)
			continue
		}
		switch node.Category {
		case TestCategoryFailing:
			partition.Failing = append(partition.Failing, node)
		case TestCategoryRunning:
			partition.Running = append(partition.Running, node)
		case TestCategorySkipped:
			partition.Skipped = append(partition.Skipped, node)
		case TestCategoryPassing:
			partition.Passing = append(partition.Passing, node)
		case TestCategoryMixed:
			partition.Mixed = append(partition.Mixed, node)
		}
	}
	return partition
}

func TestSpanCategory(span *Span) TestCategory {
	if span == nil {
		return TestCategoryPassing
	}
	if span.TestStatus != TestStatusUnset {
		switch {
		case span.TestStatus.IsFailing():
			return TestCategoryFailing
		case span.TestStatus.IsSkipped():
			return TestCategorySkipped
		case span.TestStatus.IsRunning():
			return TestCategoryRunning
		default:
			return TestCategoryPassing
		}
	}
	switch {
	case span.IsFailedOrCausedFailure():
		return TestCategoryFailing
	case span.IsRunningOrEffectsRunning():
		return TestCategoryRunning
	default:
		return TestCategoryPassing
	}
}

type TestIndex struct {
	db *DB

	initialBuilt   bool
	structureDirty bool
	aggregateDirty bool

	nodesBySpan map[SpanID]*TestNode
	dirtySpans  map[SpanID]struct{}

	knownTestSpans map[SpanID]*Span

	cachedView   *TestView
	version      uint64
	builtVersion uint64

	initialScanCount       int
	structuralRebuildCount int
}

func (db *DB) TestView() *TestView {
	if db.testIndex == nil {
		db.testIndex = &TestIndex{db: db}
	}
	return db.testIndex.View()
}

func (db *DB) HasTests() bool {
	return db.TestView().HasTests()
}

func (db *DB) noteTestSpanUpdated(span *Span) {
	if db == nil || db.testIndex == nil || span == nil {
		return
	}
	db.testIndex.spanUpdated(span)
}

func (idx *TestIndex) View() *TestView {
	if !idx.initialBuilt {
		idx.buildInitial()
	}
	if idx.structureDirty {
		idx.rebuildStructure()
	} else if idx.aggregateDirty {
		idx.applyAggregateUpdates()
	}
	return idx.cachedView
}

func (idx *TestIndex) buildInitial() {
	idx.initialScanCount++
	idx.knownTestSpans = make(map[SpanID]*Span)
	for _, span := range idx.db.Spans.Order {
		if testSpanHasNode(span) {
			idx.knownTestSpans[span.ID] = span
		}
	}
	idx.initialBuilt = true
	idx.structureDirty = true
	idx.rebuildStructure()
}

func (idx *TestIndex) spanUpdated(span *Span) {
	if !idx.initialBuilt {
		return
	}

	hasNodeMetadata := testSpanHasNode(span)
	if idx.knownTestSpans == nil {
		idx.knownTestSpans = make(map[SpanID]*Span)
	}

	node := idx.nodesBySpan[span.ID]
	_, known := idx.knownTestSpans[span.ID]

	if hasNodeMetadata {
		idx.knownTestSpans[span.ID] = span
	} else if known || node != nil {
		delete(idx.knownTestSpans, span.ID)
		idx.markStructureDirty()
		return
	} else {
		return
	}

	if idx.structureDirty {
		return
	}

	if node == nil {
		idx.markStructureDirty()
		return
	}

	kind, name, fullName, suiteName, ok := testNodeMetadata(span)
	if !ok || node.Kind != kind || node.Name != name || node.FullName != fullName || node.suiteName != suiteName {
		idx.markStructureDirty()
		return
	}

	nearest := nearestTestAncestor(span, idx.nodesBySpan)
	if node.Parent != nil && node.Parent.Kind == TestNodeVirtualSuite {
		if nearest != nil {
			idx.markStructureDirty()
			return
		}
	} else if nearest != node.Parent {
		idx.markStructureDirty()
		return
	}

	idx.markAggregateDirty(span.ID)
}

func (idx *TestIndex) markStructureDirty() {
	idx.structureDirty = true
	idx.version++
}

func (idx *TestIndex) markAggregateDirty(spanID SpanID) {
	if idx.dirtySpans == nil {
		idx.dirtySpans = make(map[SpanID]struct{})
	}
	idx.dirtySpans[spanID] = struct{}{}
	idx.aggregateDirty = true
	idx.version++
}

func (idx *TestIndex) rebuildStructure() {
	idx.structuralRebuildCount++

	nodesBySpan := make(map[SpanID]*TestNode, len(idx.knownTestSpans))
	for id, span := range idx.knownTestSpans {
		kind, name, fullName, suiteName, ok := testNodeMetadata(span)
		if !ok {
			delete(idx.knownTestSpans, id)
			continue
		}
		node := &TestNode{
			ID:        TestNodeID("span:" + span.ID.String()),
			Kind:      kind,
			Name:      name,
			FullName:  fullName,
			Span:      span,
			suiteName: suiteName,
		}
		nodesBySpan[id] = node
	}

	var roots []*TestNode
	for _, node := range nodesBySpan {
		if parent := nearestTestAncestor(node.Span, nodesBySpan); parent != nil {
			node.Parent = parent
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}

	sortTestNodes(roots)
	roots = groupVirtualSuites(roots, nodesBySpan)
	sortTestNodes(roots)

	idx.nodesBySpan = nodesBySpan
	idx.cachedView = idx.buildView(roots)
	idx.structureDirty = false
	idx.aggregateDirty = false
	clear(idx.dirtySpans)
	idx.builtVersion = idx.version
}

func (idx *TestIndex) buildView(roots []*TestNode) *TestView {
	view := &TestView{
		Roots:        roots,
		ByID:         make(map[TestNodeID]*TestNode),
		BySpan:       make(map[SpanID]*TestNode, len(idx.nodesBySpan)),
		CasesByName:  make(map[string][]*TestNode),
		SuitesByName: make(map[string][]*TestNode),
	}

	rootDone := idx.rootDone()
	for _, root := range roots {
		computeTestAggregates(root, rootDone)
		view.Counts = view.Counts.add(root.Counts)
		if root.MaxDuration > view.MaxDuration {
			view.MaxDuration = root.MaxDuration
		}
	}

	walkTestNodes(roots, func(node *TestNode) {
		view.ByID[node.ID] = node
		if node.Span != nil {
			view.BySpan[node.Span.ID] = node
		}
		switch node.Kind {
		case TestNodeCase:
			addTestNameIndex(view.CasesByName, node)
		case TestNodeSuite, TestNodeVirtualSuite:
			addTestNameIndex(view.SuitesByName, node)
		}
	})

	return view
}

func addTestNameIndex(index map[string][]*TestNode, node *TestNode) {
	if node.FullName != "" {
		index[node.FullName] = append(index[node.FullName], node)
	}
	if node.Name != "" && node.Name != node.FullName {
		index[node.Name] = append(index[node.Name], node)
	}
}

func (idx *TestIndex) applyAggregateUpdates() {
	if idx.cachedView == nil {
		idx.rebuildStructure()
		return
	}
	rootDone := idx.rootDone()
	for spanID := range idx.dirtySpans {
		node := idx.nodesBySpan[spanID]
		if node == nil {
			continue
		}
		idx.updateNodeAggregate(node, rootDone)
	}
	clear(idx.dirtySpans)
	idx.aggregateDirty = false
	idx.builtVersion = idx.version

	idx.cachedView.Counts = TestCounts{}
	idx.cachedView.MaxDuration = 0
	for _, root := range idx.cachedView.Roots {
		idx.cachedView.Counts = idx.cachedView.Counts.add(root.Counts)
		if root.MaxDuration > idx.cachedView.MaxDuration {
			idx.cachedView.MaxDuration = root.MaxDuration
		}
	}
}

func (idx *TestIndex) updateNodeAggregate(node *TestNode, rootDone time.Time) {
	oldSelfCount := TestCounts{}
	if node.Kind == TestNodeCase {
		oldSelfCount = countForCategory(node.SelfCategory)
	}

	newSelfCategory := TestSpanCategory(node.Span)
	newSelfCount := TestCounts{}
	if node.Kind == TestNodeCase {
		newSelfCount = countForCategory(newSelfCategory)
	}
	countDelta := newSelfCount.sub(oldSelfCount)

	node.SelfCategory = newSelfCategory
	if !countDelta.isZero() {
		node.Counts = node.Counts.add(countDelta)
	}

	oldMax := node.MaxDuration
	node.selfDuration = testSpanDuration(node.Span, rootDone)
	if node.selfDuration > node.MaxDuration {
		node.MaxDuration = node.selfDuration
	} else if oldMax > 0 && oldMax == node.MaxDuration && node.selfDuration < oldMax {
		node.MaxDuration = node.selfDuration
		for _, child := range node.Children {
			if child.MaxDuration > node.MaxDuration {
				node.MaxDuration = child.MaxDuration
			}
		}
	}
	node.Category = aggregateTestCategory(node.Kind, node.SelfCategory, node.Counts)

	changedMax := node.MaxDuration
	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if !countDelta.isZero() {
			parent.Counts = parent.Counts.add(countDelta)
		}
		if changedMax > parent.MaxDuration {
			parent.MaxDuration = changedMax
		}
		parent.Category = aggregateTestCategory(parent.Kind, parent.SelfCategory, parent.Counts)
	}
}

func (idx *TestIndex) rootDone() time.Time {
	if idx.db.RootSpan != nil && idx.db.RootSpan.EndTime.After(idx.db.RootSpan.StartTime) {
		return idx.db.RootSpan.EndTime
	}
	if !idx.db.End.IsZero() {
		return idx.db.End
	}
	return time.Now()
}

func testSpanHasNode(span *Span) bool {
	_, _, _, _, ok := testNodeMetadata(span)
	return ok
}

func testNodeMetadata(span *Span) (TestNodeKind, string, string, string, bool) {
	if span == nil {
		return TestNodeCase, "", "", "", false
	}
	if span.TestCaseName != "" {
		return TestNodeCase, testNodeDisplayName(span, span.TestCaseName), span.TestCaseName, span.TestSuiteName, true
	}
	if span.TestSuiteName != "" {
		return TestNodeSuite, testNodeDisplayName(span, span.TestSuiteName), span.TestSuiteName, span.TestSuiteName, true
	}
	return TestNodeCase, "", "", "", false
}

func testNodeDisplayName(span *Span, fallback string) string {
	if span != nil && span.Name != "" {
		return span.Name
	}
	return fallback
}

func nearestTestAncestor(span *Span, nodesBySpan map[SpanID]*TestNode) *TestNode {
	if span == nil {
		return nil
	}
	for parent := range span.Parents {
		if node := nodesBySpan[parent.ID]; node != nil {
			return node
		}
	}
	return nil
}

func groupVirtualSuites(roots []*TestNode, nodesBySpan map[SpanID]*TestNode) []*TestNode {
	realSuites := make(map[string]struct{})
	for _, node := range nodesBySpan {
		if node.Kind == TestNodeSuite {
			realSuites[node.suiteName] = struct{}{}
		}
	}

	suiteGroups := make(map[string]*TestNode)
	var grouped []*TestNode
	for _, node := range roots {
		if node.Kind != TestNodeCase || node.suiteName == "" {
			grouped = append(grouped, node)
			continue
		}
		if _, hasRealSuite := realSuites[node.suiteName]; hasRealSuite {
			grouped = append(grouped, node)
			continue
		}
		suite := suiteGroups[node.suiteName]
		if suite == nil {
			suite = &TestNode{
				ID:                 TestNodeID("suite:" + url.PathEscape(node.suiteName)),
				Kind:               TestNodeVirtualSuite,
				Name:               node.suiteName,
				FullName:           node.suiteName,
				RepresentativeSpan: node.Span,
				suiteName:          node.suiteName,
			}
			suiteGroups[node.suiteName] = suite
			grouped = append(grouped, suite)
		}
		node.Parent = suite
		suite.Children = append(suite.Children, node)
	}
	return grouped
}

func sortTestNodes(nodes []*TestNode) {
	slices.SortFunc(nodes, compareTestNodes)
	for _, node := range nodes {
		sortTestNodes(node.Children)
		if node.Kind == TestNodeVirtualSuite {
			node.RepresentativeSpan = representativeSpan(node)
		}
	}
}

func compareTestNodes(a, b *TestNode) int {
	return cmp.Or(
		cmp.Compare(a.Name, b.Name),
		cmp.Compare(testNodeStart(a).UnixNano(), testNodeStart(b).UnixNano()),
		cmp.Compare(string(a.ID), string(b.ID)),
	)
}

func testNodeStart(node *TestNode) time.Time {
	if node == nil {
		return time.Time{}
	}
	if node.Span != nil {
		return node.Span.StartTime
	}
	if node.RepresentativeSpan != nil {
		return node.RepresentativeSpan.StartTime
	}
	return time.Time{}
}

func representativeSpan(node *TestNode) *Span {
	if node == nil {
		return nil
	}
	if node.Span != nil {
		return node.Span
	}
	for _, child := range node.Children {
		if rep := representativeSpan(child); rep != nil {
			return rep
		}
	}
	return nil
}

func computeTestAggregates(node *TestNode, rootDone time.Time) {
	if node == nil {
		return
	}

	node.Counts = TestCounts{}
	node.MaxDuration = 0
	node.SelfCategory = TestSpanCategory(node.Span)
	node.selfDuration = testSpanDuration(node.Span, rootDone)
	if node.selfDuration > node.MaxDuration {
		node.MaxDuration = node.selfDuration
	}
	if node.Kind == TestNodeCase {
		node.Counts = node.Counts.add(countForCategory(node.SelfCategory))
	}

	for _, child := range node.Children {
		computeTestAggregates(child, rootDone)
		node.Counts = node.Counts.add(child.Counts)
		if child.MaxDuration > node.MaxDuration {
			node.MaxDuration = child.MaxDuration
		}
	}
	if node.Kind == TestNodeVirtualSuite && node.RepresentativeSpan == nil {
		node.RepresentativeSpan = representativeSpan(node)
	}
	node.Category = aggregateTestCategory(node.Kind, node.SelfCategory, node.Counts)
}

func aggregateTestCategory(kind TestNodeKind, self TestCategory, counts TestCounts) TestCategory {
	if self == TestCategoryFailing || counts.Failing > 0 {
		return TestCategoryFailing
	}
	if self == TestCategoryRunning || counts.Running > 0 {
		return TestCategoryRunning
	}
	if total := counts.Total(); total > 0 {
		switch {
		case counts.Skipped == total:
			return TestCategorySkipped
		case counts.Passing == total && self != TestCategorySkipped:
			return TestCategoryPassing
		default:
			return TestCategoryMixed
		}
	}
	if kind != TestNodeVirtualSuite {
		switch self {
		case TestCategorySkipped:
			return TestCategorySkipped
		case TestCategoryPassing:
			return TestCategoryPassing
		}
	}
	return TestCategoryMixed
}

func testSpanDuration(span *Span, rootDone time.Time) time.Duration {
	if span == nil {
		return 0
	}
	if dur := span.Activity.Duration(rootDone); dur > 0 {
		return dur
	}
	end := span.EndTime
	if end.Before(span.StartTime) {
		end = rootDone
	}
	if end.After(span.StartTime) {
		return end.Sub(span.StartTime)
	}
	return 0
}

func walkTestNodes(nodes []*TestNode, f func(*TestNode)) {
	for _, node := range nodes {
		f(node)
		walkTestNodes(node.Children, f)
	}
}
