// Package difftest drives internal/fastbolt and the real bbolt through the
// same random sequences of bucket, key, cursor and sequence operations, with
// commits and rollbacks, and requires both to hold the same data after every
// transaction. It runs in test-split's testFastbolt check, which supplies the
// real bbolt as the realbolt module.
package difftest

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	mem "go.etcd.io/bbolt"
	real "realbolt"
)

// Both databases are driven through the same small interface.
type bkt interface {
	Bucket([]byte) bkt
	CreateBucket([]byte) (bkt, error)
	CreateBucketIfNotExists([]byte) (bkt, error)
	DeleteBucket([]byte) error
	Get([]byte) []byte
	Put([]byte, []byte) error
	Delete([]byte) error
	NextSequence() (uint64, error)
	ForEach(func(k, v []byte) error) error
	Cursor() cur
}
type cur interface {
	First() ([]byte, []byte)
	Last() ([]byte, []byte)
	Next() ([]byte, []byte)
	Prev() ([]byte, []byte)
	Seek([]byte) ([]byte, []byte)
	Delete() error
}

type mb struct{ b *mem.Bucket }
type rb struct{ b *real.Bucket }

func (x mb) Bucket(k []byte) bkt {
	if c := x.b.Bucket(k); c != nil {
		return mb{c}
	}
	return nil
}
func (x mb) CreateBucket(k []byte) (bkt, error) {
	c, err := x.b.CreateBucket(k)
	if err != nil {
		return nil, err
	}
	return mb{c}, nil
}
func (x mb) CreateBucketIfNotExists(k []byte) (bkt, error) {
	c, err := x.b.CreateBucketIfNotExists(k)
	if err != nil {
		return nil, err
	}
	return mb{c}, nil
}
func (x mb) DeleteBucket(k []byte) error                 { return x.b.DeleteBucket(k) }
func (x mb) Get(k []byte) []byte                         { return x.b.Get(k) }
func (x mb) Put(k, v []byte) error                       { return x.b.Put(k, v) }
func (x mb) Delete(k []byte) error                       { return x.b.Delete(k) }
func (x mb) NextSequence() (uint64, error)               { return x.b.NextSequence() }
func (x mb) ForEach(fn func(k, v []byte) error) error    { return x.b.ForEach(fn) }
func (x mb) Cursor() cur                                 { return x.b.Cursor() }
func (x rb) Bucket(k []byte) bkt {
	if c := x.b.Bucket(k); c != nil {
		return rb{c}
	}
	return nil
}
func (x rb) CreateBucket(k []byte) (bkt, error) {
	c, err := x.b.CreateBucket(k)
	if err != nil {
		return nil, err
	}
	return rb{c}, nil
}
func (x rb) CreateBucketIfNotExists(k []byte) (bkt, error) {
	c, err := x.b.CreateBucketIfNotExists(k)
	if err != nil {
		return nil, err
	}
	return rb{c}, nil
}
func (x rb) DeleteBucket(k []byte) error              { return x.b.DeleteBucket(k) }
func (x rb) Get(k []byte) []byte                      { return x.b.Get(k) }
func (x rb) Put(k, v []byte) error                    { return x.b.Put(k, v) }
func (x rb) Delete(k []byte) error                    { return x.b.Delete(k) }
func (x rb) NextSequence() (uint64, error)            { return x.b.NextSequence() }
func (x rb) ForEach(fn func(k, v []byte) error) error { return x.b.ForEach(fn) }
func (x rb) Cursor() cur                              { return x.b.Cursor() }

func errName(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func dump(b bkt, indent string, sb *strings.Builder) {
	_ = b.ForEach(func(k, v []byte) error {
		if v == nil {
			if c := b.Bucket(k); c != nil {
				fmt.Fprintf(sb, "%s%q/\n", indent, k)
				dump(c, indent+"  ", sb)
				return nil
			}
		}
		fmt.Fprintf(sb, "%s%q=%q\n", indent, k, v)
		return nil
	})
}

// step applies one random operation at a random path to both and compares. Half the time it
// reuses the handles an earlier step of the same transaction opened at that path, so handles held
// across other writes are exercised too.
func step(t *testing.T, r *rand.Rand, roots [2]bkt, held map[string][2]bkt, log *[]string) {
	keys := []string{"a", "b", "c", "aa", "ab", "b\x00", "z", "lease", "snap", "k1", "k2", "k10"}
	path := []string{}
	for d := r.Intn(3); d > 0; d-- {
		path = append(path, keys[r.Intn(4)])
	}
	var bs [2]bkt
	pk := strings.Join(path, "/")
	if h, ok := held[pk]; ok && len(path) > 0 && r.Intn(2) == 0 {
		bs = h
	} else {
		for i := range roots {
			bs[i] = roots[i]
			for _, p := range path {
				if bs[i] == nil {
					break
				}
				bs[i] = bs[i].Bucket([]byte(p))
			}
		}
		if bs[0] != nil && bs[1] != nil {
			held[pk] = bs
		}
	}
	if (bs[0] == nil) != (bs[1] == nil) {
		t.Fatalf("bucket presence differs at %q; log:\n%s", path, strings.Join(*log, "\n"))
	}
	if bs[0] == nil {
		return
	}
	k := []byte(keys[r.Intn(len(keys))])
	v := []byte(fmt.Sprintf("v%d", r.Intn(1000)))
	op := r.Intn(11)
	if len(path) == 0 && (op == 8 || op == 9) {
		op = 10
	}
	var out [2]string
	for i, b := range bs {
		switch op {
		case 0, 1:
			out[i] = errName(b.Put(k, v))
		case 2:
			out[i] = errName(b.Delete(k))
		case 3:
			_, err := b.CreateBucket(k)
			out[i] = errName(err)
		case 4:
			_, err := b.CreateBucketIfNotExists(k)
			out[i] = errName(err)
		case 5:
			out[i] = errName(b.DeleteBucket(k))
			// bbolt frees a deleted bucket's pages under any handle still held to it or below it,
			// so using one is undefined: forget them.
			gone := strings.TrimPrefix(pk+"/"+string(k), "/")
			for h := range held {
				if h == gone || strings.HasPrefix(h, gone+"/") {
					delete(held, h)
				}
			}
		case 6:
			out[i] = fmt.Sprintf("%q", b.Get(k))
		case 7:
			s, err := b.NextSequence()
			out[i] = fmt.Sprintf("%d %s", s, errName(err))
		case 8:
			// cursor walk forward with a delete of every key starting with 'a'
			c := b.Cursor()
			var sb strings.Builder
			// bbolt skips a key when Next follows Cursor.Delete; containerd never
			// deletes through a cursor, so re-seek after each delete instead.
			for ck, cv := c.Seek(k); ck != nil; ck, cv = c.Next() {
				fmt.Fprintf(&sb, "%q=%q ", ck, cv)
				if ck[0] == 'a' {
					kk := append([]byte(nil), ck...)
					fmt.Fprintf(&sb, "del:%s ", errName(c.Delete()))
					ck, cv = c.Seek(kk)
					if ck == nil {
						break
					}
					fmt.Fprintf(&sb, "reseek:%q=%q ", ck, cv)
				}
			}
			out[i] = sb.String()
		case 9:
			c := b.Cursor()
			var sb strings.Builder
			for ck, cv := c.Last(); ck != nil; ck, cv = c.Prev() {
				fmt.Fprintf(&sb, "%q=%q ", ck, cv)
			}
			ck, _ := c.First()
			fmt.Fprintf(&sb, "first=%q", ck)
			out[i] = sb.String()
		case 10:
			var sb strings.Builder
			dump(b, "", &sb)
			out[i] = sb.String()
		}
	}
	*log = append(*log, fmt.Sprintf("%q op%d %q %q -> %s", path, op, k, v, out[1]))
	if out[0] != out[1] {
		t.Fatalf("op %d at %q key %q: mem %s, real %s; log:\n%s", op, path, k, out[0], out[1], strings.Join(*log, "\n"))
	}
}

func TestDifferential(t *testing.T) {
	for seed := int64(1); seed <= 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		dir := t.TempDir()
		mdb, err := mem.Open(filepath.Join(dir, "m.db"), 0600, nil)
		if err != nil {
			t.Fatal(err)
		}
		rdb, err := real.Open(filepath.Join(dir, "r.db"), 0600, &real.Options{NoSync: true})
		if err != nil {
			t.Fatal(err)
		}
		var log []string
		for txn := 0; txn < 40; txn++ {
			rollback := r.Intn(4) == 0
			mtx, _ := mdb.Begin(true)
			rtx, _ := rdb.Begin(true)
			// a read transaction opened before the write must not see it
			mro, _ := mdb.Begin(false)
			var before strings.Builder
			dumpTx(mro, &before)
			roots := [2]bkt{txRoot{m: mtx}, txRoot{r: rtx}}
			held := map[string][2]bkt{}
			for n := r.Intn(30); n > 0; n-- {
				step(t, r, roots, held, &log)
			}
			if rollback {
				log = append(log, "ROLLBACK")
				mtx.Rollback()
				rtx.Rollback()
			} else {
				log = append(log, "COMMIT")
				if err := mtx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err := rtx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			var after strings.Builder
			dumpTx(mro, &after)
			mro.Rollback()
			if before.String() != after.String() {
				t.Fatalf("seed %d: read tx saw a concurrent write", seed)
			}
			var ms, rs strings.Builder
			mdb.View(func(tx *mem.Tx) error { dumpTx(tx, &ms); return nil })
			rdb.View(func(tx *real.Tx) error { dump(txRoot{r: tx}, "", &rs); return nil })
			if ms.String() != rs.String() {
				t.Fatalf("seed %d txn %d: dumps differ\nmem:\n%s\nreal:\n%s\nlog:\n%s", seed, txn, ms.String(), rs.String(), strings.Join(log, "\n"))
			}
		}
		rdb.Close()
		mdb.Close()
	}
}

func dumpTx(tx *mem.Tx, sb *strings.Builder) { dump(txRoot{m: tx}, "", sb) }

// txRoot adapts a transaction's root to bkt (root keys are buckets only).
type txRoot struct {
	m *mem.Tx
	r *real.Tx
}

func (x txRoot) Bucket(k []byte) bkt {
	if x.m != nil {
		if b := x.m.Bucket(k); b != nil {
			return mb{b}
		}
		return nil
	}
	if b := x.r.Bucket(k); b != nil {
		return rb{b}
	}
	return nil
}
func (x txRoot) CreateBucket(k []byte) (bkt, error) {
	if x.m != nil {
		b, err := x.m.CreateBucket(k)
		if err != nil {
			return nil, err
		}
		return mb{b}, nil
	}
	b, err := x.r.CreateBucket(k)
	if err != nil {
		return nil, err
	}
	return rb{b}, nil
}
func (x txRoot) CreateBucketIfNotExists(k []byte) (bkt, error) {
	if x.m != nil {
		b, err := x.m.CreateBucketIfNotExists(k)
		if err != nil {
			return nil, err
		}
		return mb{b}, nil
	}
	b, err := x.r.CreateBucketIfNotExists(k)
	if err != nil {
		return nil, err
	}
	return rb{b}, nil
}
func (x txRoot) DeleteBucket(k []byte) error {
	if x.m != nil {
		return x.m.DeleteBucket(k)
	}
	return x.r.DeleteBucket(k)
}
func (x txRoot) Get([]byte) []byte                { return nil }
func (x txRoot) Put([]byte, []byte) error         { return errors.New("root put") }
func (x txRoot) Delete([]byte) error              { return errors.New("root delete") }
func (x txRoot) NextSequence() (uint64, error)    { return 0, errors.New("root seq") }
func (x txRoot) Cursor() cur                      { return nil }
func (x txRoot) ForEach(fn func(k, v []byte) error) error {
	if x.m != nil {
		return x.m.ForEach(func(k []byte, _ *mem.Bucket) error { return fn(k, nil) })
	}
	return x.r.ForEach(func(k []byte, _ *real.Bucket) error { return fn(k, nil) })
}

// Root-level ops other than bucket ops aren't valid in bolt; step only uses
// them at depth > 0 except for the bucket ops above. Skip ops the root can't do.
func init() { _ = bytes.Compare }

// TestConcurrent: readers run while a writer commits; each reader must see
// a consistent snapshot (both counters equal), every transaction must
// succeed, readers must actually read during the writes, and the last write
// must be what remains. Run with -race.
func TestConcurrent(t *testing.T) {
	db, err := mem.Open(filepath.Join(t.TempDir(), "c.db"), 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Update(func(tx *mem.Tx) error {
		b, err := tx.CreateBucket([]byte("b"))
		if err != nil {
			return err
		}
		if err := b.Put([]byte("x"), []byte("0")); err != nil {
			return err
		}
		return b.Put([]byte("y"), []byte("0"))
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var reads atomic.Int64
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := db.View(func(tx *mem.Tx) error {
					b := tx.Bucket([]byte("b"))
					if b == nil {
						return errors.New("bucket missing")
					}
					x, y := b.Get([]byte("x")), b.Get([]byte("y"))
					if x == nil || !bytes.Equal(x, y) {
						return fmt.Errorf("torn read: x=%q y=%q", x, y)
					}
					return b.ForEach(func(k, v []byte) error { return nil })
				})
				if err != nil {
					t.Error(err)
					return
				}
				reads.Add(1)
			}
		}()
	}
	const writes = 5000
	for i := 1; i <= writes; i++ {
		v := []byte(fmt.Sprint(i))
		if err := db.Update(func(tx *mem.Tx) error {
			b := tx.Bucket([]byte("b"))
			if err := b.Put([]byte("x"), v); err != nil {
				return err
			}
			if err := b.Put([]byte(fmt.Sprintf("k%d", i%50)), v); err != nil {
				return err
			}
			return b.Put([]byte("y"), v)
		}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if reads.Load() == 0 {
		t.Fatal("no reader completed a read during the writes")
	}
	want := []byte(fmt.Sprint(writes))
	if err := db.View(func(tx *mem.Tx) error {
		b := tx.Bucket([]byte("b"))
		if x, y := b.Get([]byte("x")), b.Get([]byte("y")); !bytes.Equal(x, want) || !bytes.Equal(y, want) {
			return fmt.Errorf("after the writes, x=%q y=%q, want %q", x, y, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
