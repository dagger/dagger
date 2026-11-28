package dagui

import (
	"slices"
	"strings"
)

// A collection check runs as items: `dagger check` splits it across the keys
// of its collections -- a Go package and the tests in it, say -- and runs
// each item as a check of its own, named by the address that selects it,
// beneath one span for the whole check (see Span.CheckCollectionGroup). The
// items are one check, so a frontend can list them as the rows of a table,
// labeled by what tells them apart.

// CollectionTable lays a collection check's items out as a table, as
// dagger's artifact tables do (dagger/dagger#15177): each dimension of their
// addresses is a column, and only the columns that tell the items apart
// show. A column whose keys are all among the tests and suites an item
// reported -- the tests it ran, as `go-test` lists them -- says what the
// item's tests already show, so it shows only if the items need it to tell
// them apart.
type CollectionTable struct {
	// Columns are the dimensions that label the items, in address order.
	Columns []string
	// Items maps each item's check name to its address.
	Items map[string]*Address
}

// RowDimensions returns an item's row as its dimensions, one per column: a
// column the item's address lacks is Absent. It returns false if the table
// has no such item.
func (t *CollectionTable) RowDimensions(checkName string) ([]AddressDimension, bool) {
	if t == nil {
		return nil, false
	}
	addr, ok := t.Items[checkName]
	if !ok {
		return nil, false
	}
	dims := make([]AddressDimension, 0, len(t.Columns))
	for _, name := range t.Columns {
		dim, ok := addr.Dimension(name)
		if !ok {
			dim = AddressDimension{Name: name, Absent: true}
		}
		dims = append(dims, dim)
	}
	return dims, true
}

// CheckCollection returns the collection group check span is, or sits
// beneath, or nil. It walks the span's ancestors, so it holds whatever the
// view is zoomed to.
func (span *Span) CheckCollection() *Span {
	for ; span != nil; span = span.ParentSpan {
		if span.CheckCollectionGroup {
			return span
		}
	}
	return nil
}

// CollectionItemDimensions returns the row of span, an item of a collection
// check, in its collection's table, as its dimensions (see
// CollectionTable.RowDimensions), or false if it isn't one or the items'
// addresses don't make a table.
func (db *DB) CollectionItemDimensions(span *Span) ([]AddressDimension, bool) {
	if span == nil || span.CheckName == "" || span.CheckCollectionGroup {
		return nil, false
	}
	group := span.CheckCollection()
	if group == nil {
		return nil, false
	}
	return db.CollectionTable(group).RowDimensions(span.CheckName)
}

// CollectionTable returns the table of the items beneath group, a collection
// check's span, or nil if it isn't one or their addresses don't make one:
// they must all be addresses of the same artifact path.
//
// The items are the checks surfaced beneath the group; their tests are
// those TestViewForSpan reports beneath each. The result is cached per DB
// mutation; callers must treat it as read-only.
func (db *DB) CollectionTable(group *Span) *CollectionTable {
	if group == nil || !group.CheckCollectionGroup {
		return nil
	}
	if db.collectionTables == nil || db.collectionTablesAt != db.mutations {
		db.collectionTables = map[SpanID]*CollectionTable{}
		db.collectionTablesAt = db.mutations
	}
	if table, ok := db.collectionTables[group.ID]; ok {
		return table
	}
	var items []collectionItem
	for _, node := range db.collectionItems(group) {
		items = append(items, collectionItem{
			name:     node.Name,
			reported: reportedTestNames(db.TestViewForSpan(node.Span)),
		})
	}
	table := buildCollectionTable(items)
	db.collectionTables[group.ID] = table
	return table
}

// collectionItems returns the checks surfaced beneath group: a check
// surfaces beneath itself, with its sub-checks -- the items -- beneath that.
func (db *DB) collectionItems(group *Span) []*CheckNode {
	var items []*CheckNode
	for _, node := range db.SurfacedChecksForSpan(group) {
		if node.Name == group.CheckName {
			items = append(items, node.Children...)
		} else {
			items = append(items, node)
		}
	}
	return items
}

// collectionItem is an item of a collection check: its check name, and the
// names of the tests and suites it reported (see reportedTestNames).
type collectionItem struct {
	name     string
	reported map[string]bool
}

func buildCollectionTable(items []collectionItem) *CollectionTable {
	if len(items) == 0 {
		return nil
	}
	table := &CollectionTable{Items: map[string]*Address{}}
	addrs := make([]*Address, len(items))
	var order []string
	seen := map[string]bool{}
	for i, item := range items {
		addr, ok := ParseAddress(item.name)
		if !ok || (i > 0 && addr.Path != addrs[0].Path) {
			// Not items of one check after all.
			return nil
		}
		addrs[i] = addr
		table.Items[item.name] = addr
		for _, dim := range addr.Dimensions {
			if !seen[dim.Name] {
				seen[dim.Name] = true
				order = append(order, dim.Name)
			}
		}
	}
	keysOf := func(i int, name string) []string {
		dim, _ := addrs[i].Dimension(name)
		return dim.Keys
	}
	varies := func(name string) bool {
		first, _ := addrs[0].Dimension(name)
		for i := 1; i < len(items); i++ {
			if dim, _ := addrs[i].Dimension(name); !sameSelection(dim, first) {
				return true
			}
		}
		return false
	}
	// covered reports whether every key of a column is a test or suite the
	// item it labels reported.
	covered := func(name string) bool {
		some := false
		for i, item := range items {
			for _, key := range keysOf(i, name) {
				if !item.reported[key] {
					return false
				}
				some = true
			}
		}
		return some
	}
	var columns, hidden []string
	for _, name := range order {
		switch {
		case !varies(name):
		case covered(name):
			hidden = append(hidden, name)
		default:
			columns = append(columns, name)
		}
	}
	distinct := func(columns []string) bool {
		rows := map[string]bool{}
		for i := range items {
			row := make([]string, 0, len(columns))
			for _, name := range columns {
				row = append(row, strings.Join(keysOf(i, name), "\x00"))
			}
			key := strings.Join(row, "\x01")
			if rows[key] {
				return false
			}
			rows[key] = true
		}
		return true
	}
	maxKeys := func(name string) int {
		most := 0
		for i := range items {
			most = max(most, len(keysOf(i, name)))
		}
		return most
	}
	// Bring back what the tests cover only as needed to tell the items
	// apart, the column with the fewest keys per item first: a package,
	// before the tests in it.
	slices.SortStableFunc(hidden, func(a, b string) int {
		return maxKeys(a) - maxKeys(b)
	})
	for len(hidden) > 0 && !distinct(columns) {
		columns = append(columns, hidden[0])
		hidden = hidden[1:]
	}
	// in address order
	slices.SortStableFunc(columns, func(a, b string) int {
		return slices.Index(order, a) - slices.Index(order, b)
	})
	table.Columns = columns
	return table
}

// reportedTestNames returns the names a test view reports: its tests' names
// and full names, and its suites', each also by every trailing part of it
// after a "/", so a key names a test or suite whichever way it's qualified
// (pkg/dang names github.com/vito/dang/pkg/dang).
func reportedTestNames(view *TestView) map[string]bool {
	names := map[string]bool{}
	if view == nil {
		return names
	}
	add := func(name string) {
		for name != "" {
			names[name] = true
			_, rest, ok := strings.Cut(name, "/")
			if !ok {
				break
			}
			name = rest
		}
	}
	for _, node := range view.BySpan {
		add(node.Name)
		add(node.FullName)
		if node.Span != nil {
			add(node.Span.TestSuiteName)
		}
	}
	return names
}
