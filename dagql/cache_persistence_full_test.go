package dagql

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	persistdb "github.com/dagger/dagger/dagql/persistdb"
)

// persistCompareTables are the tables a save writes, which a save that writes
// only what changed must leave as a full save would.
var persistCompareTables = []string{
	"results",
	"result_records",
	"eq_classes",
	"eq_class_digests",
	"terms",
	"term_inputs",
	"result_output_eq_classes",
	"result_deps",
	"result_calls",
	"result_taught",
	"persisted_edges",
	"result_snapshot_links",
	"snapshot_content_links",
	"snapshot_content_hashes",
	"imported_layer_blob_index",
	"imported_layer_diff_index",
}

// checkPersistFullSave writes full, the snapshot of a full save taken under a
// save's own hold, into an empty database, and compares it table by table with
// the database that save left: they must hold the same rows, and the same
// next entry number and write count.
func checkPersistFullSave(ctx context.Context, c *Cache, full *persistStateSnapshot) error {
	dir, err := os.MkdirTemp("", "dagql-full-save-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, pdb, err := prepareCacheDBs(ctx, filepath.Join(dir, "full.db"), c.blobBacked)
	if err != nil {
		return err
	}
	defer closeCacheDBs(db, pdb)
	if err := c.applyPersistStateSnapshot(ctx, db, pdb, full); err != nil {
		return fmt.Errorf("full save: %w", err)
	}
	for _, table := range persistCompareTables {
		saved, err := persistTableRows(ctx, c.sqlDB, table)
		if err != nil {
			return err
		}
		whole, err := persistTableRows(ctx, db, table)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(saved, whole) {
			return fmt.Errorf("table %s: the save left %d rows, a full save %d, differing: %s", table, len(saved), len(whole), persistRowsDiff(saved, whole))
		}
	}
	for _, key := range []string{persistdb.MetaKeyNextResultID, persistdb.MetaKeyBundleWrites} {
		saved, _, err := c.pdb.SelectMetaValue(ctx, key)
		if err != nil {
			return err
		}
		whole, _, err := pdb.SelectMetaValue(ctx, key)
		if err != nil {
			return err
		}
		if saved != whole {
			return fmt.Errorf("meta %s: the save left %q, a full save %q", key, saved, whole)
		}
	}
	return nil
}

// persistTableRows reads every row of table, each as its columns' values
// printed, sorted.
func persistTableRows(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%q", vals))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.Sort(out)
	return out, nil
}

// persistRowsDiff names a few rows only one of a and b holds.
func persistRowsDiff(a, b []string) string {
	var only []string
	for _, row := range a {
		if _, found := slices.BinarySearch(b, row); !found {
			only = append(only, "saved only: "+row)
		}
	}
	for _, row := range b {
		if _, found := slices.BinarySearch(a, row); !found {
			only = append(only, "full only: "+row)
		}
	}
	if len(only) > 4 {
		only = append(only[:4], fmt.Sprintf("and %d more", len(only)-4))
	}
	return fmt.Sprint(only)
}
