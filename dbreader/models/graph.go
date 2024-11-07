package models

import (
    "github.com/btcsuite/btcwallet/walletdb"
    "github.com/lightningnetwork/lnd/channeldb"
    "github.com/lightningnetwork/lnd/channeldb/models"
)

// ChannelGraph defines the interface for the channel graph
type ChannelGraph interface {
    ForEachChannel(func(*models.ChannelEdgeInfo, *models.ChannelEdgePolicy, *models.ChannelEdgePolicy) error) error
    ForEachNode(func(walletdb.ReadTx, *channeldb.LightningNode) error) error
}