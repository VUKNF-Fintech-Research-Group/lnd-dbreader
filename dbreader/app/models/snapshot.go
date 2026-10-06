// -----------------------------------------------------------
//  [*] models — a whole copy of channel.db (snapshot.go)
//
//  dbreader never opens the node's channel.db — LND holds
//  bbolt's exclusive lock on it — so every sync reads a copy,
//  taken with a plain sequential read while LND keeps
//  committing. bbolt writes copy-on-write: a commit puts its
//  pages on free pages and switches to its new root last, in
//  one of the two meta pages, so the pages the newest root
//  reaches are never written in place. They are only freed by
//  the next commit and reused, at the earliest, by the one
//  after it. A copy is therefore whole when no newer commit
//  has finished by the time the copy is done; otherwise
//  pages of a later state may sit under the copy's root — a
//  torn copy, which the read-only open in graph.go would walk
//  without a word, missing whole subtrees of the graph.
//
//  This file reads the transaction id straight off the meta
//  pages and copies again until a copy is whole.
// -----------------------------------------------------------


package models

import (
	// Standard library
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"os"
	"time"
)








// -----------------------------------------------------------
// bbolt meta page layout
// -----------------------------------------------------------
//
// Pages 0 and 1 of a bbolt file are its meta pages: a 16-byte
// page header, then the meta record — magic, version, page
// size, flags, the root bucket, the freelist page, the
// high-water page id, the transaction id and an FNV-64a
// checksum over everything before it. bbolt writes the
// record in the host's byte order, little-endian on every
// platform the image is built for; the layout has not
// changed since file format version 2.
//
// Used by:
//   - readMeta (below)
// -----------------------------------------------------------

const (
	boltMagic          = 0xED0CDAED
	boltVersion        = 2
	boltPageHeaderSize = 16
	metaPageSizeAt     = 8
	metaTxIDAt         = 48
	metaChecksumAt     = 56
	metaSize           = 64
)








// -----------------------------------------------------------
// readMeta
// -----------------------------------------------------------
//
// The meta record of the page at offset: its transaction id
// and the page size it declares, or ok false when the page
// is not a valid meta — wrong magic or version, or a
// checksum that does not match, as on a meta page that was
// being rewritten while it was read.
//
// Used by:
//   - BoltTxID (below)
// -----------------------------------------------------------

func readMeta(file *os.File, offset int64) (txID uint64, pageSize uint32, ok bool) {
	buf := make([]byte, boltPageHeaderSize+metaSize)
	if _, err := file.ReadAt(buf, offset); err != nil {
		return 0, 0, false
	}
	meta := buf[boltPageHeaderSize:]

	if binary.LittleEndian.Uint32(meta) != boltMagic || binary.LittleEndian.Uint32(meta[4:]) != boltVersion {
		return 0, 0, false
	}

	checksum := fnv.New64a()
	checksum.Write(meta[:metaChecksumAt])
	if checksum.Sum64() != binary.LittleEndian.Uint64(meta[metaChecksumAt:]) {
		return 0, 0, false
	}

	return binary.LittleEndian.Uint64(meta[metaTxIDAt:]), binary.LittleEndian.Uint32(meta[metaPageSizeAt:]), true
}








// -----------------------------------------------------------
// BoltTxID
// -----------------------------------------------------------
//
// The transaction id of the newest valid meta page of the
// bbolt file at path — the state bbolt itself would open.
// The two meta pages are read directly, without bbolt and
// without its lock, so this works on the live file LND
// holds. Page 1 sits one page size into the file: the size
// page 0 declares, or the host's page size when page 0 is
// not valid.
//
// Used by:
//   - CopyChannelDB (below)
// -----------------------------------------------------------

func BoltTxID(path string) (uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	txID0, pageSize, ok0 := readMeta(file, 0)
	if !ok0 {
		pageSize = uint32(os.Getpagesize())
	}
	txID1, _, ok1 := readMeta(file, int64(pageSize))

	switch {
	case ok0 && ok1:
		return max(txID0, txID1), nil
	case ok0:
		return txID0, nil
	case ok1:
		return txID1, nil
	}
	return 0, fmt.Errorf("%s has no valid bbolt meta page", path)
}








// -----------------------------------------------------------
// copyFile
// -----------------------------------------------------------
//
// Plain sequential copy of src to dst. os.Create truncates,
// so a leftover copy is never appended to.
//
// Used by:
//   - CopyChannelDB (below)
// -----------------------------------------------------------

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}








// -----------------------------------------------------------
// CopyChannelDB
// -----------------------------------------------------------
//
// Copies the live channel.db at src to dst and hands the
// copy over only when it is whole: once the copy is done,
// the live file's newest transaction must still be the one
// the copy holds (see the file header for why that is
// enough). A later commit — or a copy whose own meta pages
// do not read — means it is taken again after pause, up to
// attempts times. A file that cannot be copied at all, such
// as a channel.db LND has not created yet, fails at once.
// Giving up removes dst, so no 0.5 GB copy is left in the
// tmpfs, and fails that sync; the next one starts afresh.
//
// Used by:
//   - main.go processLNDDatabase — STEP 1 of every sync
// -----------------------------------------------------------

func CopyChannelDB(src, dst string, attempts int, pause time.Duration) error {
	var lastErr error

	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			time.Sleep(pause)
		}

		if err := copyFile(src, dst); err != nil {
			os.Remove(dst)
			return err
		}

		copied, err := BoltTxID(dst)
		if err != nil {
			lastErr = fmt.Errorf("copy unreadable: %w", err)
			log.Printf("Copy %d of %d is unreadable (%v), copying again", attempt, attempts, err)
			continue
		}

		live, err := BoltTxID(src)
		if err != nil {
			lastErr = fmt.Errorf("live file unreadable: %w", err)
			log.Printf("channel.db is unreadable after copy %d of %d (%v), copying again", attempt, attempts, err)
			continue
		}

		if live == copied {
			return nil
		}

		lastErr = fmt.Errorf("transaction %d committed during the copy of transaction %d", live, copied)
		log.Printf("channel.db moved on during copy %d of %d (transaction %d → %d), copying again", attempt, attempts, copied, live)
	}

	os.Remove(dst)
	return fmt.Errorf("no whole copy in %d attempts: %w", attempts, lastErr)
}
