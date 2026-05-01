package dagui

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

func testID(id byte) SpanID {
	return SpanID{SpanID: trace.SpanID{id}}
}

func testSnapshot(id byte, name string, parent SpanID, status TestStatus) SpanSnapshot {
	start := time.Unix(int64(id), 0)
	return SpanSnapshot{
		ID:           testID(id),
		TraceID:      TraceID{TraceID: trace.TraceID{1}},
		Name:         name,
		StartTime:    start,
		EndTime:      start.Add(time.Second),
		ParentID:     parent,
		TestCaseName: name,
		TestStatus:   status,
		Status:       sdktrace.Status{Code: codes.Ok},
	}
}

func TestProcessAttributeTestMetadata(t *testing.T) {
	var snapshot SpanSnapshot
	snapshot.ProcessAttribute(string(semconv.TestCaseNameKey), "case-a")
	snapshot.ProcessAttribute(string(semconv.TestSuiteNameKey), "suite-a")
	snapshot.ProcessAttribute(string(semconv.TestSuiteRunStatusKey), "skipped")
	if snapshot.TestCaseName != "case-a" {
		t.Fatalf("expected test case name, got %q", snapshot.TestCaseName)
	}
	if snapshot.TestSuiteName != "suite-a" {
		t.Fatalf("expected test suite name, got %q", snapshot.TestSuiteName)
	}
	if snapshot.TestStatus != TestStatusSkipped {
		t.Fatalf("expected skipped status, got %q", snapshot.TestStatus)
	}
}

func TestProcessAttributeTestStatusNormalizationAndPrecedence(t *testing.T) {
	var pass SpanSnapshot
	pass.ProcessAttribute(string(semconv.TestCaseResultStatusKey), "pass")
	if pass.TestStatus != TestStatusSuccess {
		t.Fatalf("expected pass to normalize to success, got %q", pass.TestStatus)
	}

	var fail SpanSnapshot
	fail.ProcessAttribute(string(semconv.TestCaseResultStatusKey), "fail")
	if fail.TestStatus != TestStatusFailure {
		t.Fatalf("expected fail to normalize to failure, got %q", fail.TestStatus)
	}

	for _, unknown := range []string{"passed", "successful", "ok", "failed", "error", "skip", "abort", "timeout", "timed-out", "timedout", "running"} {
		if got := normalizeTestStatus(unknown); got != TestStatusUnset {
			t.Fatalf("expected %q to be ignored, got %q", unknown, got)
		}
	}

	for _, strong := range []string{"failure", "timed_out", "aborted", "skipped"} {
		var snapshot SpanSnapshot
		snapshot.ProcessAttribute(string(semconv.TestSuiteRunStatusKey), strong)
		snapshot.ProcessAttribute(string(semconv.TestCaseResultStatusKey), "pass")
		if snapshot.TestStatus == TestStatusSuccess {
			t.Fatalf("expected %q not to be overwritten by pass", strong)
		}
	}
}

func TestTestClassification(t *testing.T) {
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		{
			ID:           testID(1),
			TraceID:      TraceID{TraceID: trace.TraceID{1}},
			Name:         "explicit failure",
			StartTime:    time.Unix(1, 0),
			EndTime:      time.Unix(2, 0),
			TestCaseName: "explicit failure",
			TestStatus:   TestStatusFailure,
			Status:       sdktrace.Status{Code: codes.Ok},
		},
		{
			ID:           testID(2),
			TraceID:      TraceID{TraceID: trace.TraceID{1}},
			Name:         "explicit skipped",
			StartTime:    time.Unix(2, 0),
			EndTime:      time.Unix(3, 0),
			TestCaseName: "explicit skipped",
			TestStatus:   TestStatusSkipped,
			Status:       sdktrace.Status{Code: codes.Ok},
		},
		{
			ID:           testID(3),
			TraceID:      TraceID{TraceID: trace.TraceID{1}},
			Name:         "span failure",
			StartTime:    time.Unix(3, 0),
			EndTime:      time.Unix(4, 0),
			TestCaseName: "span failure",
			Status:       sdktrace.Status{Code: codes.Error},
		},
		{
			ID:           testID(4),
			TraceID:      TraceID{TraceID: trace.TraceID{1}},
			Name:         "running",
			StartTime:    time.Unix(4, 0),
			EndTime:      time.Unix(3, 0),
			TestCaseName: "running",
		},
		{
			ID:           testID(5),
			TraceID:      TraceID{TraceID: trace.TraceID{1}},
			Name:         "explicit success",
			StartTime:    time.Unix(5, 0),
			EndTime:      time.Unix(6, 0),
			TestCaseName: "explicit success",
			TestStatus:   TestStatusSuccess,
			Status:       sdktrace.Status{Code: codes.Error},
		},
	})

	view := db.TestView()
	assertCategory := func(name string, want TestCategory) {
		t.Helper()
		node := view.FindCaseByName(name)
		if node == nil {
			t.Fatalf("missing test node %q", name)
		}
		if node.Category != want {
			t.Fatalf("%s: expected %s, got %s", name, want, node.Category)
		}
	}
	assertCategory("explicit failure", TestCategoryFailing)
	assertCategory("explicit skipped", TestCategorySkipped)
	assertCategory("span failure", TestCategoryFailing)
	assertCategory("running", TestCategoryRunning)
	assertCategory("explicit success", TestCategoryPassing)
}

func TestTestNodeNames(t *testing.T) {
	db := NewDB()
	snapshot := testSnapshot(1, "baz", SpanID{}, TestStatusSuccess)
	snapshot.TestCaseName = "TestFoo/TestBar/baz"
	db.ImportSnapshots([]SpanSnapshot{snapshot})

	view := db.TestView()
	node := view.FindCaseByName("TestFoo/TestBar/baz")
	if node == nil {
		t.Fatal("expected lookup by fully-qualified test name")
	}
	if node.Name != "baz" {
		t.Fatalf("expected local test name from span name, got %q", node.Name)
	}
	if node.FullName != "TestFoo/TestBar/baz" {
		t.Fatalf("expected fully-qualified test name, got %q", node.FullName)
	}
	if view.FindCaseByName("baz") != node {
		t.Fatal("expected lookup by local name alias to find the same node")
	}
}

func TestTestHierarchyCountsAndSuites(t *testing.T) {
	db := NewDB()
	parent := testSnapshot(1, "parent", SpanID{}, TestStatusSuccess)
	childA := testSnapshot(2, "child-a", parent.ID, TestStatusSuccess)
	childB := testSnapshot(3, "child-b", parent.ID, TestStatusSuccess)
	db.ImportSnapshots([]SpanSnapshot{parent, childA, childB})

	view := db.TestView()
	parentNode := view.FindCaseByName("parent")
	if parentNode == nil {
		t.Fatal("missing parent test node")
	}
	if got := parentNode.Counts.Total(); got != 3 {
		t.Fatalf("expected parent plus two subtests to count as 3 tests, got %d", got)
	}

	db = NewDB()
	failedParent := testSnapshot(1, "parent", SpanID{}, TestStatusFailure)
	passingChild := testSnapshot(2, "child", failedParent.ID, TestStatusSuccess)
	db.ImportSnapshots([]SpanSnapshot{failedParent, passingChild})
	parentNode = db.TestView().FindCaseByName("parent")
	if parentNode.Counts.Failing != 1 || parentNode.Counts.Passing != 1 || parentNode.Category != TestCategoryFailing {
		t.Fatalf("expected failing parent + passing child counts/category, got counts=%+v category=%s", parentNode.Counts, parentNode.Category)
	}

	db = NewDB()
	passingParent := testSnapshot(1, "parent", SpanID{}, TestStatusSuccess)
	failingChild := testSnapshot(2, "child", passingParent.ID, TestStatusFailure)
	db.ImportSnapshots([]SpanSnapshot{passingParent, failingChild})
	parentNode = db.TestView().FindCaseByName("parent")
	if parentNode.Counts.Failing != 1 || parentNode.Counts.Passing != 1 || parentNode.Category != TestCategoryFailing {
		t.Fatalf("expected passing parent + failing child aggregate failure, got counts=%+v category=%s", parentNode.Counts, parentNode.Category)
	}

	db = NewDB()
	suite := SpanSnapshot{
		ID:            testID(1),
		TraceID:       TraceID{TraceID: trace.TraceID{1}},
		Name:          "suite",
		StartTime:     time.Unix(1, 0),
		EndTime:       time.Unix(2, 0),
		TestSuiteName: "suite",
		Status:        sdktrace.Status{Code: codes.Ok},
	}
	caseInSuite := testSnapshot(2, "case", suite.ID, TestStatusSuccess)
	db.ImportSnapshots([]SpanSnapshot{suite, caseInSuite})
	suiteNode := db.TestView().FindSuiteByName("suite")
	if suiteNode == nil || suiteNode.Kind != TestNodeSuite {
		t.Fatalf("expected real suite node, got %#v", suiteNode)
	}
	if got := suiteNode.Counts.Total(); got != 1 {
		t.Fatalf("expected suite-only node not to count as a test, got %d", got)
	}

	db = NewDB()
	orphanA := testSnapshot(1, "case-a", SpanID{}, TestStatusSuccess)
	orphanA.TestSuiteName = "virtual-suite"
	orphanB := testSnapshot(2, "case-b", SpanID{}, TestStatusSuccess)
	orphanB.TestSuiteName = "virtual-suite"
	db.ImportSnapshots([]SpanSnapshot{orphanA, orphanB})
	virtualSuite := db.TestView().FindSuiteByName("virtual-suite")
	if virtualSuite == nil || virtualSuite.Kind != TestNodeVirtualSuite || virtualSuite.Span != nil {
		t.Fatalf("expected virtual suite node, got %#v", virtualSuite)
	}
	if got := virtualSuite.Counts.Total(); got != 2 {
		t.Fatalf("expected virtual suite not to count itself, got %d", got)
	}
}

func TestTestViewForSpanScopesVirtualSuites(t *testing.T) {
	db := NewDB()
	checkA := SpanSnapshot{
		ID:        testID(1),
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      "check-a",
		StartTime: time.Unix(1, 0),
		EndTime:   time.Unix(2, 0),
		CheckName: "check-a",
	}
	checkB := SpanSnapshot{
		ID:        testID(2),
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      "check-b",
		StartTime: time.Unix(2, 0),
		EndTime:   time.Unix(3, 0),
		CheckName: "check-b",
	}
	caseA := testSnapshot(3, "case-a", checkA.ID, TestStatusSuccess)
	caseA.TestSuiteName = "shared-suite"
	caseB := testSnapshot(4, "case-b", checkB.ID, TestStatusFailure)
	caseB.TestSuiteName = "shared-suite"
	db.ImportSnapshots([]SpanSnapshot{checkA, checkB, caseA, caseB})

	globalSuite := db.TestView().FindSuiteByName("shared-suite")
	if globalSuite == nil || globalSuite.Counts.Total() != 2 {
		t.Fatalf("expected global view to contain both cases, got %#v", globalSuite)
	}

	viewA := db.TestViewForSpan(db.Spans.Map[checkA.ID])
	if got := viewA.Counts.Total(); got != 1 {
		t.Fatalf("expected check-a scoped view to contain one case, got %d", got)
	}
	if viewA.FindCaseByName("case-a") == nil || viewA.FindCaseByName("case-b") != nil {
		t.Fatalf("expected check-a scoped cases only, got case-a=%#v case-b=%#v", viewA.FindCaseByName("case-a"), viewA.FindCaseByName("case-b"))
	}
	suiteA := viewA.FindSuiteByName("shared-suite")
	if suiteA == nil || suiteA.Counts.Total() != 1 || suiteA.Counts.Passing != 1 {
		t.Fatalf("expected check-a scoped suite to contain one passing case, got %#v", suiteA)
	}

	viewB := db.TestViewForSpan(db.Spans.Map[checkB.ID])
	if got := viewB.Counts.Total(); got != 1 {
		t.Fatalf("expected check-b scoped view to contain one case, got %d", got)
	}
	if viewB.FindCaseByName("case-b") == nil || viewB.FindCaseByName("case-a") != nil {
		t.Fatalf("expected check-b scoped cases only, got case-a=%#v case-b=%#v", viewB.FindCaseByName("case-a"), viewB.FindCaseByName("case-b"))
	}
	suiteB := viewB.FindSuiteByName("shared-suite")
	if suiteB == nil || suiteB.Counts.Total() != 1 || suiteB.Counts.Failing != 1 {
		t.Fatalf("expected check-b scoped suite to contain one failing case, got %#v", suiteB)
	}
}

func TestPartitionTests(t *testing.T) {
	db := NewDB()
	failing := testSnapshot(1, "failing", SpanID{}, TestStatusFailure)
	running := testSnapshot(2, "running", SpanID{}, TestStatusInProgress)
	passing := testSnapshot(3, "passing", SpanID{}, TestStatusSuccess)
	skipped := testSnapshot(4, "skipped", SpanID{}, TestStatusSkipped)
	mixedParent := testSnapshot(5, "mixed", SpanID{}, TestStatusSuccess)
	mixedChild := testSnapshot(6, "mixed child", mixedParent.ID, TestStatusSkipped)
	suite := SpanSnapshot{
		ID:            testID(7),
		TraceID:       TraceID{TraceID: trace.TraceID{1}},
		Name:          "suite",
		StartTime:     time.Unix(7, 0),
		EndTime:       time.Unix(8, 0),
		TestSuiteName: "suite",
	}
	db.ImportSnapshots([]SpanSnapshot{failing, running, passing, skipped, mixedParent, mixedChild, suite})

	partition := PartitionTests(db.TestView().Roots)
	if len(partition.Failing) != 1 || partition.Failing[0].Name != "failing" {
		t.Fatalf("expected failing partition to contain failing node, got %#v", partition.Failing)
	}
	if len(partition.Running) != 1 || partition.Running[0].Name != "running" {
		t.Fatalf("expected running partition to contain running node, got %#v", partition.Running)
	}
	if len(partition.Passing) != 1 || partition.Passing[0].Name != "passing" {
		t.Fatalf("expected passing partition to contain passing node, got %#v", partition.Passing)
	}
	if len(partition.Skipped) != 1 || partition.Skipped[0].Name != "skipped" {
		t.Fatalf("expected skipped partition to contain skipped node, got %#v", partition.Skipped)
	}
	if len(partition.Mixed) != 1 || partition.Mixed[0].Name != "mixed" {
		t.Fatalf("expected mixed partition to contain mixed node, got %#v", partition.Mixed)
	}
	if len(partition.Suites) != 1 || partition.Suites[0].Name != "suite" {
		t.Fatalf("expected suites partition to contain suite node, got %#v", partition.Suites)
	}
}

func TestTestIndexIncrementalBehavior(t *testing.T) {
	db := NewDB()
	first := testSnapshot(1, "first", SpanID{}, TestStatusSuccess)
	db.ImportSnapshots([]SpanSnapshot{first})

	view := db.TestView()
	idx := db.testIndex
	if idx.initialScanCount != 1 {
		t.Fatalf("expected one initial scan, got %d", idx.initialScanCount)
	}
	initialRebuilds := idx.structuralRebuildCount
	if db.TestView() != view {
		t.Fatal("expected cached test view to be reused")
	}
	if idx.initialScanCount != 1 {
		t.Fatalf("expected cached view not to rescan spans, got %d scans", idx.initialScanCount)
	}

	nonTest := SpanSnapshot{
		ID:        testID(9),
		TraceID:   TraceID{TraceID: trace.TraceID{1}},
		Name:      "non-test",
		StartTime: time.Unix(9, 0),
		EndTime:   time.Unix(10, 0),
	}
	db.ImportSnapshots([]SpanSnapshot{nonTest})
	if db.TestView() != view {
		t.Fatal("expected non-test update not to rebuild test view")
	}
	if idx.structuralRebuildCount != initialRebuilds {
		t.Fatalf("expected no structural rebuild for non-test update, got %d -> %d", initialRebuilds, idx.structuralRebuildCount)
	}

	updatedFirst := testSnapshot(1, "first", SpanID{}, TestStatusFailure)
	db.ImportSnapshots([]SpanSnapshot{updatedFirst})
	if db.TestView() != view {
		t.Fatal("expected aggregate-only update to reuse test view pointer")
	}
	if idx.structuralRebuildCount != initialRebuilds {
		t.Fatalf("expected no structural rebuild for status update, got %d -> %d", initialRebuilds, idx.structuralRebuildCount)
	}
	if node := view.FindCaseByName("first"); node == nil || node.Counts.Failing != 1 || node.Category != TestCategoryFailing {
		t.Fatalf("expected status update to update counts/category, got %#v", node)
	}

	second := testSnapshot(2, "second", SpanID{}, TestStatusSuccess)
	db.ImportSnapshots([]SpanSnapshot{second})
	newView := db.TestView()
	if newView == view {
		t.Fatal("expected structural update to rebuild test view")
	}
	if idx.initialScanCount != 1 {
		t.Fatalf("expected structural rebuild not to rescan all spans, got %d scans", idx.initialScanCount)
	}
	if idx.structuralRebuildCount != initialRebuilds+1 {
		t.Fatalf("expected one structural rebuild for added test, got %d -> %d", initialRebuilds, idx.structuralRebuildCount)
	}
	if got := newView.Counts.Total(); got != 2 {
		t.Fatalf("expected two tests after adding second test, got %d", got)
	}
}
