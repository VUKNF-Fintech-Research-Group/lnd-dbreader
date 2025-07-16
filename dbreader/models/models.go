/*
Package models provides data structures and utilities for working with LND v0.19.1 graph data.

This package contains custom wrappers around LND's native data types to provide
JSON serialization and database compatibility. It handles the new graph database
architecture introduced in LND v0.19.1.
*/
package models

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcwallet/walletdb"
	"github.com/lightningnetwork/lnd/channeldb"
	"github.com/lightningnetwork/lnd/graph/db/models"
	"github.com/lightningnetwork/lnd/kvdb"
	"github.com/lightningnetwork/lnd/lnwire"
)

// Type aliases for LND v0.19.1 graph database models
type (
	ChannelEdgeInfo   = models.ChannelEdgeInfo
	ChannelEdgePolicy = models.ChannelEdgePolicy
	LightningNode     = models.LightningNode
	DB                = channeldb.DB
	ReadTx            = walletdb.ReadTx
)

// CustomNodeAnnouncement wraps lnwire.NodeAnnouncement with custom JSON serialization
type CustomNodeAnnouncement struct {
	lnwire.NodeAnnouncement
}

// CustomAddress represents a network address with JSON-friendly format
type CustomAddress struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	Port    uint16 `json:"port"`
}

// CustomChannelAnnouncement wraps lnwire.ChannelAnnouncement1 with custom JSON serialization
type CustomChannelAnnouncement struct {
	*lnwire.ChannelAnnouncement1
}

// Interface compliance methods for CustomChannelAnnouncement
func (c CustomChannelAnnouncement) SCID() lnwire.ShortChannelID {
	return c.ChannelAnnouncement1.SCID()
}

func (c CustomChannelAnnouncement) GetChainHash() chainhash.Hash {
	return c.ChannelAnnouncement1.GetChainHash()
}

func (c CustomChannelAnnouncement) Node1KeyBytes() [33]byte {
	return c.ChannelAnnouncement1.Node1KeyBytes()
}

func (c CustomChannelAnnouncement) Node2KeyBytes() [33]byte {
	return c.ChannelAnnouncement1.Node2KeyBytes()
}

// MarshalJSON provides custom JSON serialization for NodeAnnouncement
func (c CustomNodeAnnouncement) MarshalJSON() ([]byte, error) {
	// Parse addresses into structured format
	customAddresses := make([]CustomAddress, len(c.Addresses))
	for i, addr := range c.Addresses {
		host, port, err := net.SplitHostPort(addr.String())
		if err != nil {
			customAddresses[i] = CustomAddress{
				Type:    "unknown",
				Address: addr.String(),
			}
		} else {
			portUint, _ := strconv.ParseUint(port, 10, 16)
			customAddresses[i] = CustomAddress{
				Type:    "tcp",
				Address: host,
				Port:    uint16(portUint),
			}
		}
	}

	return json.Marshal(&struct {
		NodeID    string          `json:"node_id"`
		AliasStr  string          `json:"alias"`
		Addresses []CustomAddress `json:"addresses"`
		Timestamp uint32          `json:"timestamp"`
		RGBColor  string          `json:"rgb_color"`
	}{
		NodeID:    hex.EncodeToString(c.NodeID[:]),
		AliasStr:  c.Alias.String(),
		Addresses: customAddresses,
		Timestamp: c.Timestamp,
		RGBColor:  fmt.Sprintf("#%02x%02x%02x", c.RGBColor.R, c.RGBColor.G, c.RGBColor.B),
	})
}

// MarshalJSON provides custom JSON serialization for ChannelAnnouncement
func (c CustomChannelAnnouncement) MarshalJSON() ([]byte, error) {
	// Convert chain hash to little-endian format
	chainHash := c.GetChainHash()
	var chainHashLE [32]byte
	for i := 0; i < 32; i++ {
		chainHashLE[i] = chainHash[31-i]
	}

	// Get node keys
	node1Bytes := c.Node1KeyBytes()
	node2Bytes := c.Node2KeyBytes()

	return json.Marshal(&struct {
		ChainHash       string `json:"chain_hash"`
		ShortChannelID  string `json:"short_channel_id"`
		NodeID1         string `json:"node_id_1"`
		NodeID2         string `json:"node_id_2"`
		BitcoinKey1     string `json:"bitcoin_key_1"`
		BitcoinKey2     string `json:"bitcoin_key_2"`
		ExtraOpaqueData string `json:"extra_opaque_data,omitempty"`
	}{
		ChainHash:       hex.EncodeToString(chainHashLE[:]),
		ShortChannelID:  c.SCID().String(),
		NodeID1:         hex.EncodeToString(node1Bytes[:]),
		NodeID2:         hex.EncodeToString(node2Bytes[:]),
		BitcoinKey1:     hex.EncodeToString(c.ChannelAnnouncement1.BitcoinKey1[:]),
		BitcoinKey2:     hex.EncodeToString(c.ChannelAnnouncement1.BitcoinKey2[:]),
		ExtraOpaqueData: hex.EncodeToString(c.ChannelAnnouncement1.ExtraOpaqueData),
	})
}

// Open opens the channeldb at the specified directory using LND v0.19.1 architecture
func Open(dbDir string) (*channeldb.DB, error) {
	// Create kvdb backend
	backend, err := kvdb.GetBoltBackend(&kvdb.BoltBackendConfig{
		DBPath:            dbDir,
		DBFileName:        "channel.db",
		NoFreelistSync:    true,
		AutoCompact:       false,
		AutoCompactMinAge: kvdb.DefaultBoltAutoCompactMinAge,
		DBTimeout:         kvdb.DefaultDBTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create kvdb backend at %s: %w", dbDir, err)
	}

	// Create channeldb with the backend
	db, err := channeldb.CreateWithBackend(backend)
	if err != nil {
		backend.Close() // Clean up backend on error
		return nil, fmt.Errorf("failed to create channeldb with backend at %s: %w", dbDir, err)
	}

	return db, nil
}
