package dagui

import (
	"cmp"
	"net/url"
	"slices"
	"time"
)

// TestStatus is the normalized OpenTelemetry semantic status for a test span.
//
// OpenTelemetry has separate well-known values for test.case.result.status
// (pass/fail) and test.suite.run.status (success, failure, skipped, aborted,
// timed_out, in_progress). The UI only needs render categories, so case-level
// pass/fail values are normalized to success/failure.
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
	switch raw {
	case "pass", "success":
		return TestStatusSuccess
	case "fail", "failure":
		return TestStatusFailure
	case "skipped":
		return TestStatusSkipped
	case "aborted":
		return TestStatusAborted
	case "timed_out":
		return TestStatusTimedOut
	case "in_progress":
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

// TestNodeID is stable across view rebuilds. Real test case and suite nodes
// use "span:<span-id>"; virtual suites use "suite:<url-escaped-suite-name>".
type TestNodeID string

type TestNode struct {
	ID   TestNodeID
	Kind TestNodeKind

	// Name is the local display name for this node. Real test nodes use the
	// backing span name; virtual suites use their synthetic suite name.
	Name string

	// FullName is the fully-qualified semantic name reported by test.case.name
	// or test.suite.name. Use this for stable lookups and URL compatibility.
	FullName string

	// Span is nil for virtual suites.
	Span *Span

	Parent   *TestNode
	Children []*TestNode

	// RepresentativeSpan is the first real descendant span for a virtual suite.
	// It is not a pseudo-span: virtual suites keep Span nil because they are
	// synthetic, and this span is only used to focus/open a related trace and
	// sort the synthetic node.
	RepresentativeSpan *Span

	// SelfCategory is the backing span's own category before child aggregation.
	// Category is the aggregate category for the rendered node and includes the
	// counted test cases under it.
	SelfCategory TestCategory
	Category     TestCategory
	Counts       TestCounts

	suiteName string
}

type TestView struct {
	Roots []*TestNode

	ByID         map[TestNodeID]*TestNode
	BySpan       map[SpanID]*TestNode
	CasesByName  map[string][]*TestNode
	SuitesByName map[string][]*TestNode

	Counts TestCounts
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
	// Mixed keeps aggregate test-case nodes with heterogeneous child results in
	// an explicit bucket so the sidebar can render them after suites but before
	// fully passing or skipped tests.
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

func (span *Span) TestCategory() TestCategory {
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

func (db *DB) TestViewForSpan(root *Span) *TestView {
	if db.testIndex == nil {
		db.testIndex = &TestIndex{db: db}
	}
	return db.testIndex.ViewForSpan(root)
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

func (idx *TestIndex) ViewForSpan(root *Span) *TestView {
	if root == nil {
		return idx.View()
	}
	idx.View()

	nodesBySpan := make(map[SpanID]*TestNode)
	for id, span := range idx.knownTestSpans {
		if span == nil {
			continue
		}
		insideRoot := span.ID == root.ID
		for parent := span.ParentSpan; !insideRoot && parent != nil; parent = parent.ParentSpan {
			insideRoot = parent.ID == root.ID
		}
		if !insideRoot {
			continue
		}
		kind, name, fullName, suiteName, ok := testNodeMetadata(span)
		if !ok {
			continue
		}
		nodesBySpan[id] = &TestNode{
			ID:        TestNodeID("span:" + span.ID.String()),
			Kind:      kind,
			Name:      name,
			FullName:  fullName,
			Span:      span,
			suiteName: suiteName,
		}
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

	return buildTestView(roots, nodesBySpan)
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
	idx.cachedView = buildTestView(roots, idx.nodesBySpan)
	idx.structureDirty = false
	idx.aggregateDirty = false
	clear(idx.dirtySpans)
	idx.builtVersion = idx.version
}

func buildTestView(roots []*TestNode, nodesBySpan map[SpanID]*TestNode) *TestView {
	view := &TestView{
		Roots:        roots,
		ByID:         make(map[TestNodeID]*TestNode),
		BySpan:       make(map[SpanID]*TestNode, len(nodesBySpan)),
		CasesByName:  make(map[string][]*TestNode),
		SuitesByName: make(map[string][]*TestNode),
	}

	for _, root := range roots {
		computeTestAggregates(root)
		view.Counts = view.Counts.add(root.Counts)
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
	for spanID := range idx.dirtySpans {
		node := idx.nodesBySpan[spanID]
		if node == nil {
			continue
		}
		idx.updateNodeAggregate(node)
	}
	clear(idx.dirtySpans)
	idx.aggregateDirty = false
	idx.builtVersion = idx.version

	idx.cachedView.Counts = TestCounts{}
	for _, root := range idx.cachedView.Roots {
		idx.cachedView.Counts = idx.cachedView.Counts.add(root.Counts)
	}
}

func (idx *TestIndex) updateNodeAggregate(node *TestNode) {
	oldSelfCount := TestCounts{}
	if node.Kind == TestNodeCase {
		oldSelfCount = countForCategory(node.SelfCategory)
	}

	newSelfCategory := node.Span.TestCategory()
	newSelfCount := TestCounts{}
	if node.Kind == TestNodeCase {
		newSelfCount = countForCategory(newSelfCategory)
	}
	countDelta := newSelfCount.sub(oldSelfCount)

	node.SelfCategory = newSelfCategory
	if !countDelta.isZero() {
		node.Counts = node.Counts.add(countDelta)
	}

	node.Category = aggregateTestCategory(node.Kind, node.SelfCategory, node.Counts)

	for parent := node.Parent; parent != nil; parent = parent.Parent {
		if !countDelta.isZero() {
			parent.Counts = parent.Counts.add(countDelta)
		}
		parent.Category = aggregateTestCategory(parent.Kind, parent.SelfCategory, parent.Counts)
	}
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
		return TestNodeCase, span.Name, span.TestCaseName, span.TestSuiteName, true
	}
	if span.TestSuiteName != "" {
		return TestNodeSuite, span.Name, span.TestSuiteName, span.TestSuiteName, true
	}
	return TestNodeCase, "", "", "", false
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

func computeTestAggregates(node *TestNode) {
	if node == nil {
		return
	}

	node.Counts = TestCounts{}
	node.SelfCategory = node.Span.TestCategory()
	if node.Kind == TestNodeCase {
		node.Counts = node.Counts.add(countForCategory(node.SelfCategory))
	}

	for _, child := range node.Children {
		computeTestAggregates(child)
		node.Counts = node.Counts.add(child.Counts)
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

func walkTestNodes(nodes []*TestNode, f func(*TestNode)) {
	for _, node := range nodes {
		f(node)
		walkTestNodes(node.Children, f)
	}
}
