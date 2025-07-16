/*
Package models provides interfaces for working with LND v0.19.1 graph database.

This file defines the ChannelGraph interface that abstracts the graph database
operations for compatibility with different LND versions.
*/
package models

import (
	"github.com/lightningnetwork/lnd/graph/db/models"
	graphdb "github.com/lightningnetwork/lnd/graph/db"
)

// ChannelGraph defines the interface for iterating over channel graph data
type ChannelGraph interface {
	// ForEachChannel iterates over all channels in the graph
	ForEachChannel(func(*models.ChannelEdgeInfo, *models.ChannelEdgePolicy, *models.ChannelEdgePolicy) error) error
	
	// ForEachNode iterates over all nodes in the graph
	ForEachNode(func(graphdb.NodeRTx) error) error
}