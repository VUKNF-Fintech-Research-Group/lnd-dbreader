// -----------------------------------------------------------
//  [*] models — the graph walker and its opener (graph.go)
//
//  The one thing db/ needs from LND's graph store: two
//  walkers, behind the ChannelGraph interface, and
//  OpenChannelGraph, which builds them over a channel.db
//  file. An interface rather than *graphdb.VersionedGraph
//  so the importers accept anything that walks like the
//  graph. Sibling of models.go, which wraps the row types.
// -----------------------------------------------------------


package models

import (
	// Standard library
	"context"
	"fmt"
	"time"

	// LND
	graphdb "github.com/lightningnetwork/lnd/graph/db"
	"github.com/lightningnetwork/lnd/graph/db/models"
	"github.com/lightningnetwork/lnd/kvdb"
	"github.com/lightningnetwork/lnd/lnwire"
)








// bbolt open timeout — only another opener of the same file
// could make it wait; dbreader's copy is private
const defaultDBTimeout = 10 * time.Second








// -----------------------------------------------------------
// ChannelGraph
// -----------------------------------------------------------
//
// Satisfied by *graphdb.VersionedGraph (LND v0.21.4) pinned
// to gossip v1 — the only version a bolt channel.db holds.
// ForEachChannel hands over the edge plus BOTH directed
// policies: nil when LND has not seen that direction, and
// also nil when the stored policy's extra bytes are not a
// valid TLV stream (v0.21.4 parses them, v0.19.3 stored
// them unchecked). ForEachNode hands over the full node
// record, shell nodes included. Both walks run in one read
// transaction, call reset once before it starts (bolt never
// retries) and stop at the first error the callback returns.
//
// Used by:
//   - db/announcements.go — the parameter of all three
//     Send* importers
//   - OpenChannelGraph (below) — returns one
// -----------------------------------------------------------

type ChannelGraph interface {
	// The edge, then its two directed policies
	ForEachChannel(ctx context.Context, cb func(*models.ChannelEdgeInfo, *models.ChannelEdgePolicy, *models.ChannelEdgePolicy) error, reset func()) error

	// The full node record; a shell node has no Alias or Color
	ForEachNode(ctx context.Context, cb func(*models.Node) error, reset func()) error
}








// -----------------------------------------------------------
// OpenChannelGraph
// -----------------------------------------------------------
//
// Opens the channel.db at dbPath READ-ONLY and puts LND's
// own graph store over it. Never the live file — bbolt takes
// an exclusive lock and LND holds it — main.go hands in its
// private copy.
//
// Read-only is load-bearing: the copy is taken while LND
// writes, and a read-write open made bbolt rebuild the
// unsynced freelist (LND runs NoFreelistSync) by scanning
// every page, which PANICS on a torn copy ("freepages:
// failed to get all reachable pages") and restarted the
// container. A read-only open never rebuilds it. NoMigration
// keeps the store from creating its buckets in a write
// transaction; a file LND's graph store never initialized
// therefore fails the walks (ErrGraphNotFound) instead of
// importing nothing — impossible for a channel.db LND has
// started on.
//
// The graph cache stays OFF: both walks read the store
// whether it is on or not, and since v0.21 LND fills it in a
// background goroutine — on, it would only cost memory.
//
// The returned func closes the bolt file; call it after the
// last walk.
//
// Used by:
//   - main.go processLNDDatabase — STEP 2 of every sync
// -----------------------------------------------------------

func OpenChannelGraph(dbPath string) (ChannelGraph, func() error, error) {
	backend, err := kvdb.Open(kvdb.BoltBackendName, dbPath, true, defaultDBTimeout, true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open LND database backend: %w", err)
	}

	// No With* helper exists for NoMigration — the modifier
	// type is a plain func, so set the field directly
	store, err := graphdb.NewKVStore(backend, func(o *graphdb.StoreOptions) {
		o.NoMigration = true
	})
	if err != nil {
		backend.Close()
		return nil, nil, fmt.Errorf("failed to create graph store: %w", err)
	}

	graph, err := graphdb.NewChannelGraph(store, graphdb.WithUseGraphCache(false))
	if err != nil {
		backend.Close()
		return nil, nil, fmt.Errorf("failed to create channel graph: %w", err)
	}

	return graphdb.NewVersionedGraph(graph, lnwire.GossipVersion1), backend.Close, nil
}
