// -----------------------------------------------------------
//  [*] models — the ChannelGraph interface (graph.go)
//
//  The one thing db/ needs from LND's graph store: two
//  walkers. An interface rather than *graphdb.ChannelGraph
//  so the importers accept anything that walks like the
//  graph — in production it is the graph main.go builds.
//  Sibling of models.go, which wraps the row types.
// -----------------------------------------------------------


package models

import (
	// LND
	graphdb "github.com/lightningnetwork/lnd/graph/db"
	"github.com/lightningnetwork/lnd/graph/db/models"
)








// -----------------------------------------------------------
// ChannelGraph
// -----------------------------------------------------------
//
// Satisfied by *graphdb.ChannelGraph (LND v0.19.3). The
// ForEachChannel callback receives the edge plus BOTH
// directed policies; the ForEachNode callback receives a
// node read-transaction and calls Node() on it. Both walks
// stop at the first error the callback returns.
//
// Used by:
//   - db/announcements.go — the parameter of all three
//     Send* importers; main.go passes the graph in
// -----------------------------------------------------------

type ChannelGraph interface {
	// The edge, then its two directed policies (nil when LND
	// has not seen that direction)
	ForEachChannel(func(*models.ChannelEdgeInfo, *models.ChannelEdgePolicy, *models.ChannelEdgePolicy) error) error

	// A read transaction per node — Node() gives the record
	ForEachNode(func(graphdb.NodeRTx) error) error
}
