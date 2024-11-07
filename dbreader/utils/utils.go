package utils

import (
    "encoding/json"
    "fmt"

    "github.com/btcsuite/btcwallet/walletdb"
    "github.com/lightningnetwork/lnd/channeldb"
    "github.com/lightningnetwork/lnd/lnwire"
    "lnd-dbreader/models"
)

func PrintChannelAnnouncements(graph *channeldb.ChannelGraph) error {
    fmt.Println("Channel Announcements:")
    err := graph.ForEachChannel(func(edgeInfo *models.ChannelEdgeInfo, c1, c2 *models.ChannelEdgePolicy) error {
        chanAnn := models.CustomChannelAnnouncement{
            ChannelAnnouncement: lnwire.ChannelAnnouncement{
                ChainHash:       edgeInfo.ChainHash,
                ShortChannelID:  lnwire.NewShortChanIDFromInt(edgeInfo.ChannelID),
                NodeID1:         edgeInfo.NodeKey1Bytes,
                NodeID2:         edgeInfo.NodeKey2Bytes,
                BitcoinKey1:     edgeInfo.BitcoinKey1Bytes,
                BitcoinKey2:     edgeInfo.BitcoinKey2Bytes,
                ExtraOpaqueData: edgeInfo.ExtraOpaqueData,
            },
        }

        // Convert to JSON
        jsonBytes, err := json.MarshalIndent(chanAnn, "", "  ")
        if err != nil {
            return fmt.Errorf("failed to marshal channel announcement to JSON: %v", err)
        }
        fmt.Printf("%s\n\n", jsonBytes)
        return nil
    })
    return err
}

func PrintNodeAnnouncements(graph *channeldb.ChannelGraph) error {
    fmt.Println("Node Announcements:")
    err := graph.ForEachNode(func(tx walletdb.ReadTx, node *channeldb.LightningNode) error {
        alias, err := lnwire.NewNodeAlias(node.Alias)
        if err != nil {
            return fmt.Errorf("failed to create node alias: %v", err)
        }

        nodeAnn := models.CustomNodeAnnouncement{
            NodeAnnouncement: lnwire.NodeAnnouncement{
                Features:        lnwire.NewRawFeatureVector(),
                Timestamp:       uint32(node.LastUpdate.Unix()),
                NodeID:          node.PubKeyBytes,
                RGBColor:        node.Color,
                Alias:           alias,
                Addresses:       node.Addresses,
                ExtraOpaqueData: node.ExtraOpaqueData,
            },
        }

        // Convert to JSON
        jsonBytes, err := json.MarshalIndent(nodeAnn, "", "  ")
        if err != nil {
            return fmt.Errorf("failed to marshal node announcement to JSON: %v", err)
        }
        fmt.Printf("%s\n\n", jsonBytes)
        return nil
    })
    return err
}
