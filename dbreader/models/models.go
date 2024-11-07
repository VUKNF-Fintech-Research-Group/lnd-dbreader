package models

import (
    "encoding/hex"
    "encoding/json"
    "fmt"
    "log"
    "net"
    "strconv"

    "github.com/lightningnetwork/lnd/channeldb"
    "github.com/lightningnetwork/lnd/channeldb/models"
    "github.com/lightningnetwork/lnd/lnwire"
    "github.com/btcsuite/btcwallet/walletdb"
)

type CustomNodeAnnouncement struct {
    lnwire.NodeAnnouncement
}

type CustomAddress struct {
    Type    string `json:"type"`
    Address string `json:"address"`
    Port    uint16 `json:"port"`
}

type CustomChannelAnnouncement struct {
    lnwire.ChannelAnnouncement
}

// MarshalJSON customizes the JSON output for NodeAnnouncement
func (c CustomNodeAnnouncement) MarshalJSON() ([]byte, error) {
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
                Type:    "tcp", // Assuming TCP, adjust if needed
                Address: host,
                Port:    uint16(portUint),
            }
        }
    }

    // features := make(map[string]bool)
    // var featureDebug string
    // if c.Features != nil {
    //     featureDebug = fmt.Sprintf("Features type: %T\n", c.Features)

    //     for i := 0; i < 256; i++ { // Assuming max 256 features
    //         if c.Features.IsSet(lnwire.FeatureBit(i)) {
    //             features[fmt.Sprintf("%d", i)] = i%2 == 1 // Odd bits are required, even are optional
    //             featureDebug += fmt.Sprintf("Feature bit %d is set\n", i)
    //         }
    //     }

    //     if len(features) == 0 {
    //         featureDebug += "No features are set\n"
    //     }
    // } else {
    //     featureDebug = "Features is nil"
    // }

    return json.Marshal(&struct {
        NodeID       string                 `json:"node_id"`
        AliasStr     string                 `json:"alias"`
        Addresses    []CustomAddress        `json:"addresses"`
        // Features     map[string]bool        `json:"features"`
        Timestamp    uint32                 `json:"timestamp"`
        RGBColor     string                 `json:"rgb_color"`
        // FeatureDebug string                 `json:"feature_debug"`
    }{
        NodeID:       hex.EncodeToString(c.NodeID[:]),
        AliasStr:     c.Alias.String(),
        Addresses:    customAddresses,
        // Features:     features,
        Timestamp:    c.Timestamp,
        RGBColor:     fmt.Sprintf("#%02x%02x%02x", c.RGBColor.R, c.RGBColor.G, c.RGBColor.B),
        // FeatureDebug: featureDebug,
    })
}

// MarshalJSON customizes the JSON output for ChannelAnnouncement
func (c CustomChannelAnnouncement) MarshalJSON() ([]byte, error) {
    var chainHashLE [32]byte
    for i := 0; i < 32; i++ {
        chainHashLE[i] = c.ChainHash[31-i]
    }

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
        ShortChannelID:  c.ShortChannelID.String(),
        NodeID1:         hex.EncodeToString(c.NodeID1[:]),
        NodeID2:         hex.EncodeToString(c.NodeID2[:]),
        BitcoinKey1:     hex.EncodeToString(c.BitcoinKey1[:]),
        BitcoinKey2:     hex.EncodeToString(c.BitcoinKey2[:]),
        ExtraOpaqueData: hex.EncodeToString(c.ExtraOpaqueData),
    })
}


// Open opens the channeldb at the specified path and ensures proper resource management
func Open(dbDir string) (*channeldb.DB, error) {
    // Open the channeldb using the provided path
    db, err := channeldb.Open(dbDir)
    if err != nil {
        // Log the error
        log.Printf("Failed to open channeldb at %s: %v", dbDir, err)
        // Attempt to repair the database
        // if repairErr := repairDatabase(dbPath); repairErr != nil {
        //     return nil, fmt.Errorf("failed to open and repair channeldb at %s: %v, repair error: %v", dbPath, err, repairErr)
        // }
        // Try opening the database again after repair
        // db, err = channeldb.Open(dbPath)
        if err != nil {
            return nil, fmt.Errorf("failed to open channeldb at %s after repair: %v", dbDir, err)
        }
    }

    return db, nil
}


// // Ensure that the DB Close method properly closes the underlying database
// func (db *DB) Close() error {
//     if err := db.Close(); err != nil {
//         log.Printf("Failed to close channeldb: %v", err)
//         return err
//     }
//     log.Printf("channeldb closed successfully")
//     return nil
// }


// Use type aliases to make the transition easier
type ChannelEdgeInfo = models.ChannelEdgeInfo
type ChannelEdgePolicy = models.ChannelEdgePolicy
type LightningNode = channeldb.LightningNode
type DB = channeldb.DB
type ReadTx = walletdb.ReadTx

// Add these function aliases to make the transition easier
var NewShortChanIDFromInt = lnwire.NewShortChanIDFromInt
var NewNodeAlias = lnwire.NewNodeAlias
var NewRawFeatureVector = lnwire.NewRawFeatureVector
