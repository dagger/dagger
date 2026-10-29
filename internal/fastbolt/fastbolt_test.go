package bbolt

import (
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.etcd.io/bbolt/errors"
)

func dump(t *testing.T, db *DB) string {
	t.Helper()
	var sb strings.Builder
	var rec func(b *Bucket, indent string)
	rec = func(b *Bucket, indent string) {
		fmt.Fprintf(&sb, "%sseq=%d\n", indent, b.Sequence())
		_ = b.ForEach(func(k, v []byte) error {
			if v == nil {
				fmt.Fprintf(&sb, "%s%q/\n", indent, k)
				rec(b.Bucket(k), indent+"  ")
				return nil
			}
			fmt.Fprintf(&sb, "%s%q=%q\n", indent, k, v)
			return nil
		})
	}
	if err := db.View(func(tx *Tx) error {
		rec(&tx.root, "")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return sb.String()
}

func fill(t *testing.T, db *DB) {
	t.Helper()
	if err := db.Update(func(tx *Tx) error {
		v1, err := tx.CreateBucket([]byte("v1"))
		if err != nil {
			return err
		}
		for i := 0; i < 50; i++ {
			s, err := v1.CreateBucket([]byte(fmt.Sprintf("snap-%03d", i)))
			if err != nil {
				return err
			}
			if err := s.Put([]byte("parent"), []byte(fmt.Sprintf("snap-%03d", i-1))); err != nil {
				return err
			}
			if err := s.Put([]byte("empty"), []byte{}); err != nil {
				return err
			}
			if _, err := s.NextSequence(); err != nil {
				return err
			}
		}
		return v1.SetSequence(77)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	db, err := Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, db)
	want := dump(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(*Tx) error { return nil }); !stderrors.Is(err, errors.ErrDatabaseNotOpen) {
		t.Fatalf("view after close: %v", err)
	}

	db, err = Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := dump(t, db); got != want {
		t.Fatalf("reopened database differs:\n%s\nwant:\n%s", got, want)
	}
	// An empty value stays non-nil, as in bbolt.
	if err := db.View(func(tx *Tx) error {
		if v := tx.Bucket([]byte("v1")).Bucket([]byte("snap-001")).Get([]byte("empty")); v == nil {
			t.Error("empty value read back as nil")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Writes after a reopen persist too.
	if err := db.Update(func(tx *Tx) error {
		return tx.Bucket([]byte("v1")).DeleteBucket([]byte("snap-010"))
	}); err != nil {
		t.Fatal(err)
	}
	want = dump(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := dump(t, db); got != want {
		t.Fatalf("second reopen differs:\n%s\nwant:\n%s", got, want)
	}
}

func TestOpenRefusesUncleanFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	db, err := Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, db)
	// The process "dies": the database is never closed.
	if _, err := Open(path, 0o600, nil); !stderrors.Is(err, ErrUncleanShutdown) {
		t.Fatalf("open of a never-closed new database: %v", err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, 0o600, nil); err != nil {
		t.Fatal(err)
	}
	// Opened from a clean file, then never closed.
	if _, err := Open(path, 0o600, nil); !stderrors.Is(err, ErrUncleanShutdown) {
		t.Fatalf("open of a never-closed reopened database: %v", err)
	}
}

func TestOpenRefusesDamagedFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.db")
	db, err := Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		data []byte
		want error
	}{
		"flipped byte": {append(append([]byte(nil), good[:40]...), append([]byte{good[40] ^ 1}, good[41:]...)...), errors.ErrChecksum},
		"truncated":    {good[:len(good)/2], errors.ErrChecksum},
		"a bbolt file": {append([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0xed, 0xda, 0x0c, 0xed}, make([]byte, 4096)...), errors.ErrInvalid},
		"short":        {[]byte("dg"), errors.ErrInvalid},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(p, tc.data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(p, 0o600, nil); !stderrors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	// A missing or empty file starts an empty database.
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, "missing"), empty} {
		db, err := Open(p, 0o600, nil)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if got := dump(t, db); got != "seq=0\n" {
			t.Fatalf("%s: not empty: %q", p, got)
		}
		db.Close()
	}
}

// A reader that began before Close keeps its view, and Close waits for the
// writer, whose commit is in the file.
func TestCloseWithTransactions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	db, err := Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	fill(t, db)
	ro, err := db.Begin(false)
	if err != nil {
		t.Fatal(err)
	}
	wtx, err := db.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wtx.CreateBucket([]byte("late")); err != nil {
		t.Fatal(err)
	}
	closed := make(chan error)
	go func() { closed <- db.Close() }()
	if err := wtx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if ro.Bucket([]byte("late")) != nil || ro.Bucket([]byte("v1")) == nil {
		t.Fatal("reader's view changed")
	}
	ro.Rollback()
	db, err = Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.View(func(tx *Tx) error {
		if tx.Bucket([]byte("late")) == nil {
			t.Error("the writer's commit is missing from the file")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A write transaction may hold several handles to one bucket, opened before
// or after writes through the others, deep in a scan or not: each must see
// every write, and a handle to a deleted bucket must not bring it back.
func TestHandlesToOneBucket(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fill(t, db)
	if err := db.Update(func(tx *Tx) error {
		v1 := tx.Bucket([]byte("v1"))
		var early []*Bucket
		for i := 0; i < 50; i++ {
			early = append(early, v1.Bucket([]byte(fmt.Sprintf("snap-%03d", i))))
		}
		for i, h := range early {
			name := []byte(fmt.Sprintf("snap-%03d", i))
			// Through a second root path, so even v1's handle differs.
			late := tx.Bucket([]byte("v1")).Bucket(name)
			if err := h.Put([]byte("n"), []byte(fmt.Sprint(i))); err != nil {
				return err
			}
			if got := string(late.Get([]byte("n"))); got != fmt.Sprint(i) {
				t.Errorf("%s: a later handle read %q", name, got)
			}
			if err := late.Put([]byte("m"), []byte(fmt.Sprint(i))); err != nil {
				return err
			}
			if got := string(h.Get([]byte("m"))); got != fmt.Sprint(i) {
				t.Errorf("%s: an earlier handle read %q", name, got)
			}
			if _, err := h.NextSequence(); err != nil {
				return err
			}
			if _, err := late.NextSequence(); err != nil {
				return err
			}
		}
		gone := early[20]
		if err := tx.Bucket([]byte("v1")).DeleteBucket([]byte("snap-020")); err != nil {
			return err
		}
		if err := gone.Put([]byte("ghost"), []byte("x")); err != nil {
			return err
		}
		if v1.Bucket([]byte("snap-020")) != nil {
			t.Error("deleted bucket came back")
		}
		fresh, err := v1.CreateBucket([]byte("snap-021-fresh"))
		if err != nil {
			return err
		}
		if err := fresh.Put([]byte("k"), []byte("v")); err != nil {
			return err
		}
		if _, err := v1.CreateBucket([]byte("snap-020")); err != nil {
			return err
		}
		// The old handle stays detached from the new bucket of its name.
		if err := gone.Put([]byte("ghost2"), []byte("x")); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(tx *Tx) error {
		v1 := tx.Bucket([]byte("v1"))
		for i := 0; i < 50; i++ {
			b := v1.Bucket([]byte(fmt.Sprintf("snap-%03d", i)))
			n, m, seq := string(b.Get([]byte("n"))), string(b.Get([]byte("m"))), b.Sequence()
			if i == 20 {
				if n != "" || m != "" || b.Get([]byte("ghost")) != nil || b.Get([]byte("ghost2")) != nil || seq != 0 {
					t.Errorf("recreated bucket has old contents: n=%q m=%q seq=%d", n, m, seq)
				}
				continue
			}
			if n != fmt.Sprint(i) || m != fmt.Sprint(i) || seq != 3 {
				t.Errorf("snap-%03d: n=%q m=%q seq=%d", i, n, m, seq)
			}
		}
		if got := string(v1.Bucket([]byte("snap-021-fresh")).Get([]byte("k"))); got != "v" {
			t.Errorf("new bucket: %q", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Readers share one handle per bucket version; it refuses writes, and a write
// that commits gives later readers a new version's handle, not the old one.
func TestReadHandles(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "meta.db"), 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	fill(t, db)
	var first *Bucket
	for i := 0; i < 2; i++ {
		if err := db.View(func(tx *Tx) error {
			b := tx.Bucket([]byte("v1")).Bucket([]byte("snap-003"))
			if first == nil {
				first = b
			} else if b != first {
				t.Error("two readers of one version got different handles")
			}
			if b.Writable() {
				t.Error("read handle is writable")
			}
			if err := b.Put([]byte("x"), []byte("y")); !stderrors.Is(err, errors.ErrTxNotWritable) {
				t.Errorf("put through a read handle: %v", err)
			}
			if _, err := b.CreateBucket([]byte("x")); !stderrors.Is(err, errors.ErrTxNotWritable) {
				t.Errorf("create through a read handle: %v", err)
			}
			n := 0
			return b.ForEach(func(k, v []byte) error { n++; return nil })
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Update(func(tx *Tx) error {
		return tx.Bucket([]byte("v1")).Bucket([]byte("snap-003")).Put([]byte("x"), []byte("y"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(tx *Tx) error {
		b := tx.Bucket([]byte("v1")).Bucket([]byte("snap-003"))
		if b == first {
			t.Error("a reader after a commit got the old version's handle")
		}
		if string(b.Get([]byte("x"))) != "y" {
			t.Error("committed write missing")
		}
		if first.Get([]byte("x")) != nil {
			t.Error("the old version's handle sees the new write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
