// -----------------------------------------------------------
//  [*] tests — the whole-copy check (snapshot_test.go)
//
//  models.CopyChannelDB and the BoltTxID it rests on, no
//  MySQL needed. The transaction id read straight off the
//  meta pages must be the one bbolt itself opens, falling
//  back to the other meta page when the newest is damaged; a
//  quiet file is copied as it is; a file that is not bbolt
//  is given up on without leaving a copy behind. The crown
//  jewels: while a writer keeps committing — every commit
//  rewriting all values to its own number — a copy is handed
//  over only when it reads back as one single state, never a
//  mix of two. With commits in bursts the copy waits for a
//  quiet spell; against a big file and a writer that never
//  pauses, where plain copies come out torn more often than
//  not, it refuses.
// -----------------------------------------------------------


package tests

import (
	// Standard library
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	// bbolt
	bolt "go.etcd.io/bbolt"

	// This module
	"lnd-dbreader/models"
)








// The churn file's shape: a bucket of churnKeys values of
// churnValueSize bytes, every commit rewriting all of them —
// big enough that a copy and a commit overlap often
const (
	churnKeys      = 3000
	churnValueSize = 256
)

// The one bucket every churn file keeps its values in
var churnBucket = []byte("churn")








// -----------------------------------------------------------
// openChurnFile
// -----------------------------------------------------------
//
// A new bbolt file at path, opened the way LND opens its own
// (NoFreelistSync) but without fsync, so commits come fast,
// closed when the test ends.
//
// Used by:
//   - TestBoltTxIDMatchesBbolt,
//     TestBoltTxIDFallsBackToTheOtherMeta,
//     TestCopyChannelDBNeverHandsOverATornCopy,
//     TestCopyChannelDBRefusesAFileThatKeepsChanging (below)
// -----------------------------------------------------------

func openChurnFile(t *testing.T, path string) *bolt.DB {
	t.Helper()

	db, err := bolt.Open(path, 0600, &bolt.Options{NoFreelistSync: true, NoSync: true})
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}








// -----------------------------------------------------------
// writeAll
// -----------------------------------------------------------
//
// One commit that rewrites every value of the churn bucket
// to commitNo, big-endian in its first 8 bytes — so a
// consistent state holds one number everywhere and a torn
// one holds two.
//
// Used by:
//   - TestBoltTxIDMatchesBbolt,
//     TestBoltTxIDFallsBackToTheOtherMeta (below)
//   - TestCopyChannelDBNeverHandsOverATornCopy,
//     TestCopyChannelDBRefusesAFileThatKeepsChanging (below)
//     — the first commit and the writer goroutine
// -----------------------------------------------------------

func writeAll(db *bolt.DB, commitNo uint64) error {
	return db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(churnBucket)
		if err != nil {
			return err
		}

		value := make([]byte, churnValueSize)
		binary.BigEndian.PutUint64(value, commitNo)
		for i := 0; i < churnKeys; i++ {
			if err := bucket.Put([]byte(fmt.Sprintf("key-%05d", i)), value); err != nil {
				return err
			}
		}
		return nil
	})
}








// -----------------------------------------------------------
// readOneState
// -----------------------------------------------------------
//
// Opens a copy read-only and checks it holds one single
// state: every key present, every value carrying the same
// commit number, which is returned. A torn copy shows up as
// a mix of numbers, missing keys, or bbolt panicking — or
// faulting on a page past the end of the file — on a broken
// tree; both are turned into errors here.
//
// Used by:
//   - TestCopyChannelDBNeverHandsOverATornCopy,
//     TestCopyChannelDBRefusesAFileThatKeepsChanging (below)
// -----------------------------------------------------------

func readOneState(path string) (commitNo uint64, err error) {
	defer debug.SetPanicOnFault(debug.SetPanicOnFault(true))
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("bbolt panicked reading it: %v", r)
		}
	}()

	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return 0, err
	}
	defer db.Close()

	err = db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(churnBucket)
		if bucket == nil {
			return errors.New("bucket missing")
		}

		keys := 0
		err := bucket.ForEach(func(key, value []byte) error {
			if len(value) < 8 {
				return fmt.Errorf("key %s holds %d bytes", key, len(value))
			}
			n := binary.BigEndian.Uint64(value)
			if keys == 0 {
				commitNo = n
			} else if n != commitNo {
				return fmt.Errorf("key %s holds commit %d, the keys before it %d", key, n, commitNo)
			}
			keys++
			return nil
		})
		if err == nil && keys != churnKeys {
			err = fmt.Errorf("%d keys, want %d", keys, churnKeys)
		}
		return err
	})
	return commitNo, err
}








// -----------------------------------------------------------
// TestBoltTxIDMatchesBbolt
// -----------------------------------------------------------
//
// The transaction id BoltTxID reads off the meta pages is,
// commit after commit, the one bbolt's own read transaction
// reports — taken while the file is open for writing, as
// LND keeps channel.db.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestBoltTxIDMatchesBbolt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.db")
	db := openChurnFile(t, path)

	for commitNo := uint64(1); commitNo <= 5; commitNo++ {
		if err := writeAll(db, commitNo); err != nil {
			t.Fatalf("commit %d: %v", commitNo, err)
		}

		var want uint64
		db.View(func(tx *bolt.Tx) error {
			want = uint64(tx.ID())
			return nil
		})

		got, err := models.BoltTxID(path)
		if err != nil {
			t.Fatalf("after commit %d: %v", commitNo, err)
		}
		if got != want {
			t.Errorf("after commit %d: BoltTxID = %d, bbolt says %d", commitNo, got, want)
		}
	}
}








// -----------------------------------------------------------
// TestBoltTxIDFallsBackToTheOtherMeta
// -----------------------------------------------------------
//
// bbolt alternates its two meta pages, the newest on page
// txid mod 2. With that page damaged — as a meta page torn
// mid-write would be — BoltTxID must fall back to the other
// one, one transaction older, exactly as bbolt itself does
// when it opens the file.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestBoltTxIDFallsBackToTheOtherMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live.db")
	db := openChurnFile(t, path)
	for commitNo := uint64(1); commitNo <= 3; commitNo++ {
		if err := writeAll(db, commitNo); err != nil {
			t.Fatalf("commit %d: %v", commitNo, err)
		}
	}
	newest, err := models.BoltTxID(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	db.Close()

	// Flip one byte of the newest meta's transaction id; its
	// checksum no longer matches
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	offset := int64(newest%2)*int64(os.Getpagesize()) + 16 + 48
	if _, err := file.WriteAt([]byte{0xff}, offset); err != nil {
		t.Fatalf("damage: %v", err)
	}
	file.Close()

	got, err := models.BoltTxID(path)
	if err != nil {
		t.Fatalf("read the damaged file: %v", err)
	}
	if got != newest-1 {
		t.Errorf("BoltTxID = %d, want %d — the other meta page", got, newest-1)
	}

	reopened, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		t.Fatalf("bbolt open: %v", err)
	}
	defer reopened.Close()
	reopened.View(func(tx *bolt.Tx) error {
		if uint64(tx.ID()) != got {
			t.Errorf("bbolt opens transaction %d, BoltTxID says %d", tx.ID(), got)
		}
		return nil
	})
}








// -----------------------------------------------------------
// TestBoltTxIDRejectsAFileThatIsNotBolt
// -----------------------------------------------------------
//
// No file, an empty file, a file of garbage: an error, never
// a transaction id made up from whatever bytes are there.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestBoltTxIDRejectsAFileThatIsNotBolt(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
	}{
		{"missing", nil},
		{"empty", []byte{}},
		{"garbage", bytes.Repeat([]byte{0xab}, 64*1024)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "channel.db")
			if c.content != nil {
				if err := os.WriteFile(path, c.content, 0600); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			if txID, err := models.BoltTxID(path); err == nil {
				t.Errorf("read transaction %d off a %s file", txID, c.name)
			}
		})
	}
}








// -----------------------------------------------------------
// TestCopyChannelDBCopiesAQuietFile
// -----------------------------------------------------------
//
// With nothing committing, the first copy is whole: it is
// handed over byte for byte, holding the same transaction as
// the original.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestCopyChannelDBCopiesAQuietFile(t *testing.T) {
	src := v0193Fixture(t)
	dst := filepath.Join(t.TempDir(), "copy.db")

	if err := models.CopyChannelDB(src, dst, 1, 0); err != nil {
		t.Fatalf("copy: %v", err)
	}

	original, _ := os.ReadFile(src)
	copied, _ := os.ReadFile(dst)
	if !bytes.Equal(original, copied) {
		t.Errorf("the copy differs from the original")
	}
}








// -----------------------------------------------------------
// TestCopyChannelDBGivesUpOnAFileThatIsNotBolt
// -----------------------------------------------------------
//
// A copy with no readable meta page is never handed over:
// after the last attempt the error comes back and the copy
// is gone, not left in the tmpfs.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestCopyChannelDBGivesUpOnAFileThatIsNotBolt(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "channel.db")
	dst := filepath.Join(dir, "copy.db")
	if err := os.WriteFile(src, bytes.Repeat([]byte{0xab}, 64*1024), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := models.CopyChannelDB(src, dst, 3, time.Millisecond); err == nil {
		t.Fatalf("handed over a copy of a file that is not bbolt")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("the copy was left behind (stat: %v)", err)
	}
}








// -----------------------------------------------------------
// TestCopyChannelDBNeverHandsOverATornCopy
// -----------------------------------------------------------
//
// The regression behind the check: a writer commits in
// bursts — back to back for a while, then quiet — and every
// commit rewrites all values to its own number, the way LND
// keeps committing graph updates while dbreader copies.
// Every copy CopyChannelDB hands over must read back as one
// single state; copies that overlapped a commit are taken
// again until one falls into a quiet spell.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestCopyChannelDBNeverHandsOverATornCopy(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "live.db")
	db := openChurnFile(t, livePath)
	if err := writeAll(db, 1); err != nil {
		t.Fatalf("first commit: %v", err)
	}


	// STEP 1: the writer — 15 ms of back-to-back commits, then
	// 15 ms of quiet, until the test is done
	// ========================================================
	var stop atomic.Bool
	var writerErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		commitNo := uint64(2)
		for !stop.Load() {
			burstEnd := time.Now().Add(15 * time.Millisecond)
			for time.Now().Before(burstEnd) && !stop.Load() {
				if err := writeAll(db, commitNo); err != nil {
					writerErr = err
					return
				}
				commitNo++
			}
			time.Sleep(15 * time.Millisecond)
		}
	}()


	// STEP 2: copy after copy, each read back for one state
	// =====================================================
	for round := 1; round <= 20; round++ {
		copyPath := filepath.Join(dir, fmt.Sprintf("copy-%02d.db", round))
		if err := models.CopyChannelDB(livePath, copyPath, 200, time.Millisecond); err != nil {
			t.Errorf("round %d: no whole copy: %v", round, err)
			continue
		}
		if _, err := readOneState(copyPath); err != nil {
			t.Errorf("round %d: handed over a torn copy: %v", round, err)
		}
	}

	stop.Store(true)
	wg.Wait()
	if writerErr != nil {
		t.Fatalf("writer: %v", writerErr)
	}
}








// -----------------------------------------------------------
// TestCopyChannelDBRefusesAFileThatKeepsChanging
// -----------------------------------------------------------
//
// The hard case: a big file — 32 MB of padding no commit
// touches — and a writer committing back to back, so about
// a dozen commits land during every copy and a plain copy
// comes out torn more often than not. CopyChannelDB must
// refuse, or hand over only a copy that reads back as one
// state — never a mix — and a refusal must not leave the
// copy behind.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestCopyChannelDBRefusesAFileThatKeepsChanging(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "live.db")
	db := openChurnFile(t, livePath)


	// STEP 1: the padding, then the first state
	// =========================================
	err := db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("padding"))
		if err != nil {
			return err
		}
		value := make([]byte, 4096)
		for i := 0; i < 8000; i++ {
			if err := bucket.Put([]byte(fmt.Sprintf("pad-%05d", i)), value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("padding: %v", err)
	}
	if err := writeAll(db, 1); err != nil {
		t.Fatalf("first commit: %v", err)
	}


	// STEP 2: the writer — commits back to back until the test
	// is done
	// ========================================================
	var stop atomic.Bool
	var writerErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for commitNo := uint64(2); !stop.Load(); commitNo++ {
			if err := writeAll(db, commitNo); err != nil {
				writerErr = err
				return
			}
		}
	}()


	// STEP 3: copies — refused, or one single state
	// =============================================
	for round := 1; round <= 5; round++ {
		copyPath := filepath.Join(dir, fmt.Sprintf("copy-%d.db", round))
		if err := models.CopyChannelDB(livePath, copyPath, 3, 0); err != nil {
			if _, statErr := os.Stat(copyPath); !os.IsNotExist(statErr) {
				t.Errorf("round %d: refused, but left the copy behind", round)
			}
			continue
		}
		if _, err := readOneState(copyPath); err != nil {
			t.Errorf("round %d: handed over a torn copy: %v", round, err)
		}
	}

	stop.Store(true)
	wg.Wait()
	if writerErr != nil {
		t.Fatalf("writer: %v", writerErr)
	}
}
