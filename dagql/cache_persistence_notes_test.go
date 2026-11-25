package dagql

import (
	"fmt"
	"slices"
	"strings"
)

// checkPersistNotes holds a save's prints against the save before's: every
// entry and term that came, went or changed since must have been noted
// since, and every class a unit names whose digests changed. A unit that
// changed without a note is a write that doesn't note what it changes, and a
// save reading the store while it wrote would have saved it as it was before.
func checkPersistNotes(before, after *persistPrints) error {
	noted := func(ids []uint64) map[uint64]bool {
		m := make(map[uint64]bool, len(ids))
		for _, id := range ids {
			m[id] = true
		}
		return m
	}
	entries := noted(idsOf(after.noted.entries))
	terms := noted(idsOf(after.noted.terms))
	classes := noted(idsOf(after.noted.classes))
	var unnoted []string
	for id, fp := range after.entries {
		if was, ok := before.entries[id]; (!ok || was != fp) && !entries[uint64(id)] {
			what := "came"
			if ok {
				what = "changed its " + entryPrintChanges(was, fp)
			}
			unnoted = append(unnoted, fmt.Sprintf("entry %d %s", id, what))
		}
	}
	for id := range before.entries {
		if _, ok := after.entries[id]; !ok && !entries[uint64(id)] {
			unnoted = append(unnoted, fmt.Sprintf("entry %d went", id))
		}
	}
	for id, fp := range after.terms {
		if was, ok := before.terms[id]; (!ok || was != fp) && !terms[uint64(id)] {
			unnoted = append(unnoted, fmt.Sprintf("term %d %s", id, changeKind(ok)))
		}
	}
	for id := range before.terms {
		if _, ok := after.terms[id]; !ok && !terms[uint64(id)] {
			unnoted = append(unnoted, fmt.Sprintf("term %d went", id))
		}
	}
	for id, fp := range after.classes {
		if was, ok := before.classes[id]; ok && was != fp && !classes[uint64(id)] {
			unnoted = append(unnoted, fmt.Sprintf("class %d changed its digests", id))
		}
	}
	if len(unnoted) == 0 {
		return nil
	}
	slices.Sort(unnoted)
	if len(unnoted) > 8 {
		unnoted = append(unnoted[:8], fmt.Sprintf("and %d more", len(unnoted)-8))
	}
	return fmt.Errorf("changed with no note: %s", strings.Join(unnoted, ", "))
}

func entryPrintChanges(was, is persistEntryPrint) string {
	var kinds []string
	for _, k := range []struct {
		name    string
		was, is uint64
	}{
		{"row", was.Row, is.Row},
		{"record inputs", was.RecordInputs, is.RecordInputs},
		{"deps", was.Deps, is.Deps},
		{"classes", was.Classes, is.Classes},
		{"calls", was.Calls, is.Calls},
		{"taught", was.Taught, is.Taught},
		{"edge", was.Edge, is.Edge},
	} {
		if k.was != k.is {
			kinds = append(kinds, k.name)
		}
	}
	return strings.Join(kinds, "+")
}

func changeKind(existed bool) string {
	if existed {
		return "changed"
	}
	return "came"
}

func idsOf[T ~uint64](ids []T) []uint64 {
	out := make([]uint64, len(ids))
	for i, id := range ids {
		out[i] = uint64(id)
	}
	return out
}
