// -----------------------------------------------------------
//  [*] models — LND v0.19.1 types, wrapped for JSON and MySQL
//
//  Thin layer over LND's own packages: aliases for the graph
//  model types, two wrappers that give lnwire announcements
//  a flat JSON shape (hex strings, "#rrggbb", host/port
//  addresses) for the json_data columns, and Open, which
//  builds a channeldb over a bbolt backend. The ChannelGraph
//  interface lives next door in graph.go.
//
//  Split into:
//
//    ChannelEdgeInfo, ChannelEdgePolicy,
//    LightningNode, DB, ReadTx     — type aliases
//    CustomNodeAnnouncement        — node → JSON
//    CustomAddress                 — one address in that JSON
//    CustomChannelAnnouncement     — channel → JSON
//    Open                          — channeldb over bbolt
// -----------------------------------------------------------


package models

import (
	// Standard library
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	// btcd / btcwallet / LND
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcwallet/walletdb"
	"github.com/lightningnetwork/lnd/channeldb"
	"github.com/lightningnetwork/lnd/graph/db/models"
	"github.com/lightningnetwork/lnd/kvdb"
	"github.com/lightningnetwork/lnd/lnwire"
)








// -----------------------------------------------------------
// type aliases
// -----------------------------------------------------------
//
// = aliases, not new types, so LND values pass straight
// through. Only ChannelEdgeInfo and ChannelEdgePolicy are
// used (the ForEachChannel callback signature in db);
// LightningNode, DB and ReadTx have no consumer at the
// moment.
//
// Used by:
//   - db/announcements.go SendChannelAnnouncements
// -----------------------------------------------------------

type (
	ChannelEdgeInfo   = models.ChannelEdgeInfo
	ChannelEdgePolicy = models.ChannelEdgePolicy
	LightningNode     = models.LightningNode
	DB                = channeldb.DB
	ReadTx            = walletdb.ReadTx
)








// -----------------------------------------------------------
// CustomNodeAnnouncement
// -----------------------------------------------------------
//
// lnwire.NodeAnnouncement by embedding, so every field is
// reachable, plus MarshalJSON below, which replaces the
// wire encoding with the flat JSON stored in
// node_announcements.json_data.
//
// Used by:
//   - db/announcements.go SendNodeAnnouncements
// -----------------------------------------------------------

type CustomNodeAnnouncement struct {
	lnwire.NodeAnnouncement
}








// -----------------------------------------------------------
// CustomAddress
// -----------------------------------------------------------
//
// One entry of the "addresses" array in the node JSON. Type
// is "tcp" when host:port split cleanly, otherwise "unknown"
// with the raw string in Address and Port 0 — the JSON does
// not tell Tor from clearnet.
//
// Used by:
//   - CustomNodeAnnouncement.MarshalJSON (below)
// -----------------------------------------------------------

type CustomAddress struct {
	Type    string `json:"type"`
	Address string `json:"address"`
	Port    uint16 `json:"port"`
}








// -----------------------------------------------------------
// CustomChannelAnnouncement
// -----------------------------------------------------------
//
// *lnwire.ChannelAnnouncement1 by embedding (a pointer, so
// the wrapper copies cheaply) plus MarshalJSON. The four
// forwarding methods below are redundant — the embedded
// pointer already promotes SCID, GetChainHash and the two
// Node*KeyBytes — and stay only because a restyle changes
// no code.
//
// Used by:
//   - db/announcements.go SendChannelAnnouncements
// -----------------------------------------------------------

type CustomChannelAnnouncement struct {
	*lnwire.ChannelAnnouncement1
}






// -----------------------------------------------------------
// CustomChannelAnnouncement.SCID
// -----------------------------------------------------------
//
// Used by:
//   - db/announcements.go SendChannelAnnouncements
//   - CustomChannelAnnouncement.MarshalJSON (below)
// -----------------------------------------------------------

func (c CustomChannelAnnouncement) SCID() lnwire.ShortChannelID {
	return c.ChannelAnnouncement1.SCID()
}






// -----------------------------------------------------------
// CustomChannelAnnouncement.GetChainHash
// -----------------------------------------------------------
//
// Used by:
//   - CustomChannelAnnouncement.MarshalJSON (below)
// -----------------------------------------------------------

func (c CustomChannelAnnouncement) GetChainHash() chainhash.Hash {
	return c.ChannelAnnouncement1.GetChainHash()
}






// -----------------------------------------------------------
// CustomChannelAnnouncement.Node1KeyBytes
// -----------------------------------------------------------
//
// Used by:
//   - db/announcements.go SendChannelAnnouncements
//   - CustomChannelAnnouncement.MarshalJSON (below)
// -----------------------------------------------------------

func (c CustomChannelAnnouncement) Node1KeyBytes() [33]byte {
	return c.ChannelAnnouncement1.Node1KeyBytes()
}






// -----------------------------------------------------------
// CustomChannelAnnouncement.Node2KeyBytes
// -----------------------------------------------------------
//
// Used by:
//   - db/announcements.go SendChannelAnnouncements
//   - CustomChannelAnnouncement.MarshalJSON (below)
// -----------------------------------------------------------

func (c CustomChannelAnnouncement) Node2KeyBytes() [33]byte {
	return c.ChannelAnnouncement1.Node2KeyBytes()
}








// -----------------------------------------------------------
// CustomNodeAnnouncement.MarshalJSON
// -----------------------------------------------------------
//
// The node JSON: node_id (hex), alias, addresses (a
// CustomAddress per entry, in LND's order), timestamp (unix
// seconds, uint32) and rgb_color as "#rrggbb". Features and
// extra opaque data are NOT emitted. A port that fails
// ParseUint becomes 0 — the error is dropped.
//
// Used by:
//   - encoding/json — via json.Marshal in
//     db/announcements.go SendNodeAnnouncements
// -----------------------------------------------------------

func (c CustomNodeAnnouncement) MarshalJSON() ([]byte, error) {
	// SplitHostPort decides tcp vs unknown — see CustomAddress
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








// -----------------------------------------------------------
// CustomChannelAnnouncement.MarshalJSON
// -----------------------------------------------------------
//
// The channel JSON, all hex strings: chain_hash is emitted
// byte-REVERSED, the same digits chainhash.Hash.String()
// prints; short_channel_id is the "block x tx x out" form,
// unlike the uint64 in the short_channel_id column;
// extra_opaque_data is omitted when empty.
//
// Used by:
//   - encoding/json — via json.Marshal in
//     db/announcements.go SendChannelAnnouncements
// -----------------------------------------------------------

func (c CustomChannelAnnouncement) MarshalJSON() ([]byte, error) {
	// Reversed by hand — the same digits Hash.String() prints
	chainHash := c.GetChainHash()
	var chainHashLE [32]byte
	for i := 0; i < 32; i++ {
		chainHashLE[i] = chainHash[31-i]
	}

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








// -----------------------------------------------------------
// Open
// -----------------------------------------------------------
//
// A channeldb over a fresh bbolt backend at dbDir/channel.db
// — the file NAME is hardcoded here, only the directory is
// a parameter. kvdb.GetBoltBackend creates the file when it
// is missing rather than failing, which is how main.go ends
// up with an empty /tmp/channel.db (see the
// processLNDDatabase banner there). The backend is closed
// again if channeldb refuses it.
//
// Used by:
//   - main.go processLNDDatabase — STEP 2.1
// -----------------------------------------------------------

func Open(dbDir string) (*channeldb.DB, error) {
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

	db, err := channeldb.CreateWithBackend(backend)
	if err != nil {
		backend.Close()
		return nil, fmt.Errorf("failed to create channeldb with backend at %s: %w", dbDir, err)
	}

	return db, nil
}
