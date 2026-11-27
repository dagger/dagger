package dagui

import (
	"slices"
	"testing"
)

// collectionGroupSnapshot is a collection check's group span, as `dagger
// check` reports it: named by its path, beneath parent.
func collectionGroupSnapshot(id byte, path string, parent byte) SpanSnapshot {
	s := checkSnapshot(id, path, testID(parent), path)
	s.CheckCollectionGroup = true
	return s
}

// suiteTestSnapshot is a test case reported in a Go package's suite.
func suiteTestSnapshot(id byte, name string, parent byte, pkg string) SpanSnapshot {
	s := testSnapshot(id, name, testID(parent), TestStatusSuccess)
	s.TestSuiteName = "github.com/acme/app/" + pkg
	return s
}

func goTestItem(pkg string, tests ...string) string {
	a := "dag://?check=go/test&go-package=" + pkg
	for _, test := range tests {
		a += "&go-test=" + test
	}
	return a
}

func spanOf(db *DB, id byte) *Span {
	return db.Spans.Map[testID(id)]
}

// A collection check's items are labeled by what tells them apart: the
// tests they ran are their tests' to show, so the package labels them.
func TestCollectionTable(t *testing.T) {
	a, b, c := goTestItem("pkg/a", "TestA"), goTestItem("pkg/b"), goTestItem("pkg/c", "TestC")
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		checkSnapshot(1, "dagger check", SpanID{}, ""),
		collectionGroupSnapshot(2, "go/test", 1),
		checkSnapshot(3, a, testID(2), a),
		checkSnapshot(4, "exec go test", testID(3), ""),
		suiteTestSnapshot(5, "TestA", 4, "pkg/a"),
		checkSnapshot(6, b, testID(2), b),
		checkSnapshot(7, "exec go build", testID(6), ""),
		checkSnapshot(8, c, testID(2), c),
		checkSnapshot(9, "exec go test", testID(8), ""),
		suiteTestSnapshot(10, "TestC", 9, "pkg/c"),
	})
	group, itemA := spanOf(db, 2), spanOf(db, 3)

	table := db.CollectionTable(group)
	if table == nil {
		t.Fatal("no table for the collection")
	}
	if len(table.Items) != 3 {
		t.Errorf("items = %d, want 3", len(table.Items))
	}
	if !slices.Equal(table.Columns, []string{"go-package"}) {
		t.Errorf("columns = %v, want [go-package]", table.Columns)
	}
	if row, ok := db.CollectionItemRow(itemA); !ok || !slices.Equal(row, []string{"pkg/a"}) {
		t.Errorf("item row = %v, %v; want [pkg/a]", row, ok)
	}
	if itemA.CheckCollection() != group || group.CheckCollection() != group {
		t.Error("an item and its group belong to the group")
	}
	if _, ok := db.CollectionItemRow(group); ok {
		t.Error("the collection itself isn't one of its items")
	}
	if db.CollectionTable(itemA) != nil {
		t.Error("an item isn't a collection")
	}
}

// When the items' packages are all a test rollup shows too, the items still
// need a column to tell them apart: the one with the fewest keys.
func TestCollectionTableKeepsAColumn(t *testing.T) {
	a, b := goTestItem("pkg/a", "TestA"), goTestItem("pkg/b", "TestB")
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		checkSnapshot(1, "dagger check", SpanID{}, ""),
		collectionGroupSnapshot(2, "go/test", 1),
		checkSnapshot(3, a, testID(2), a),
		suiteTestSnapshot(4, "TestA", 3, "pkg/a"),
		checkSnapshot(5, b, testID(2), b),
		suiteTestSnapshot(6, "TestB", 5, "pkg/b"),
	})
	if row, _ := db.CollectionItemRow(spanOf(db, 3)); !slices.Equal(row, []string{"pkg/a"}) {
		t.Errorf("item row = %v, want [pkg/a]", row)
	}
}

// Items whose addresses don't name one artifact don't make a table.
func TestCollectionTableNeedsOnePath(t *testing.T) {
	db := NewDB()
	db.ImportSnapshots([]SpanSnapshot{
		checkSnapshot(1, "dagger check", SpanID{}, ""),
		collectionGroupSnapshot(2, "go/test", 1),
		checkSnapshot(3, "dag://?check=go/test&go-package=a", testID(2), "dag://?check=go/test&go-package=a"),
		checkSnapshot(4, "dag://?check=go/lint&go-package=b", testID(2), "dag://?check=go/lint&go-package=b"),
	})
	if table := db.CollectionTable(spanOf(db, 2)); table != nil {
		t.Errorf("table = %+v, want none", table)
	}
	if _, ok := db.CollectionItemRow(spanOf(db, 3)); ok {
		t.Error("an item of no table has no row")
	}
}

func TestCollectionTableRowCells(t *testing.T) {
	table := buildCollectionTable([]collectionItem{
		{name: "dag://?check=verify&view-package=left&view-case=Bar&tag=x&tag=y"},
		{name: "dag://?check=verify&view-package=right&view-case=Baz&tag"},
		{name: "dag://?check=verify&view-package=left&view-case=Foo"},
	})
	if !slices.Equal(table.Columns, []string{"view-package", "view-case", "tag"}) {
		t.Fatalf("columns = %v", table.Columns)
	}
	for name, want := range map[string][]string{
		"dag://?check=verify&view-package=left&view-case=Bar&tag=x&tag=y": {"left", "Bar", "tag ×2"},
		"dag://?check=verify&view-package=right&view-case=Baz&tag":        {"right", "Baz", "*"},
		"dag://?check=verify&view-package=left&view-case=Foo":             {"left", "Foo", "–"},
	} {
		if row, ok := table.Row(name); !ok || !slices.Equal(row, want) {
			t.Errorf("Row(%q) = %v, %v; want %v", name, row, ok, want)
		}
	}
}
