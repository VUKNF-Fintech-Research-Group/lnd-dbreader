package db

import (
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "net"
    "strconv"
    "strings"

    "github.com/btcsuite/btcwallet/walletdb"
    "github.com/lightningnetwork/lnd/lnwire"
    "lnd-dbreader/models"
)

const batchSize = 5000

func SendChannelAnnouncements(graph models.ChannelGraph, db *sql.DB) error {
    fmt.Println("Sending Channel Announcements to MySQL:")

    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        } else {
            tx.Commit()
        }
    }()

    var values []interface{}
    var placeholders []string
    count := 0

    err = graph.ForEachChannel(func(edgeInfo *models.ChannelEdgeInfo, c1, c2 *models.ChannelEdgePolicy) error {
        chanAnn := models.CustomChannelAnnouncement{
            ChannelAnnouncement: lnwire.ChannelAnnouncement{
                ShortChannelID:  lnwire.NewShortChanIDFromInt(edgeInfo.ChannelID),
                NodeID1:         edgeInfo.NodeKey1Bytes,
                NodeID2:         edgeInfo.NodeKey2Bytes,
                BitcoinKey1:     edgeInfo.BitcoinKey1Bytes,
                BitcoinKey2:     edgeInfo.BitcoinKey2Bytes,
                ExtraOpaqueData: edgeInfo.ExtraOpaqueData,
            },
        }

        jsonBytes, err := json.Marshal(chanAnn)
        if err != nil {
            return fmt.Errorf("failed to marshal channel announcement to JSON: %v", err)
        }

        shortChannelIDInt := chanAnn.ShortChannelID.ToUint64()

        values = append(values,
            shortChannelIDInt,
            hex.EncodeToString(chanAnn.NodeID1[:]),
            hex.EncodeToString(chanAnn.NodeID2[:]),
            hex.EncodeToString(chanAnn.BitcoinKey1[:]),
            hex.EncodeToString(chanAnn.BitcoinKey2[:]),
            hex.EncodeToString(chanAnn.ExtraOpaqueData),
            string(jsonBytes),
        )
        placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, NOW(), NOW())")

        count++

        if count%batchSize == 0 {
            if err := executeBatchChannelAnnouncements(tx, placeholders, values); err != nil {
                return err
            }
            values = nil
            placeholders = nil
        }

        return nil
    })

    if err != nil {
        return err
    }

    if len(values) > 0 {
        if err := executeBatchChannelAnnouncements(tx, placeholders, values); err != nil {
            return err
        }
    }

    fmt.Printf("Processed %d channel announcements\n", count)
    return nil
}

func executeBatchChannelAnnouncements(tx *sql.Tx, placeholders []string, values []interface{}) error {
    query := fmt.Sprintf(`
        INSERT INTO channel_announcements 
        (short_channel_id, node_id_1, node_id_2, bitcoin_key_1, bitcoin_key_2, extra_opaque_data, json_data, first_seen, last_seen)
        VALUES %s
        ON DUPLICATE KEY UPDATE
            json_data = VALUES(json_data),
            last_seen = VALUES(last_seen)
    `, strings.Join(placeholders, ","))

    _, err := tx.Exec(query, values...)
    if err != nil {
        return fmt.Errorf("failed to execute batch insert: %v", err)
    }

    return nil
}

func SendNodeAnnouncements(graph models.ChannelGraph, db *sql.DB) error {
    fmt.Println("Sending Node Announcements to MySQL:")

    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        } else {
            tx.Commit()
        }
    }()

    var values []interface{}
    var placeholders []string
    count := 0

    err = graph.ForEachNode(func(dbTx walletdb.ReadTx, node *models.LightningNode) error {
        // Ensure not to hold onto dbTx or open new transactions within the callback

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

        jsonBytes, err := json.Marshal(nodeAnn)
        if err != nil {
            return fmt.Errorf("failed to marshal node announcement to JSON: %v", err)
        }

        values = append(values,
            hex.EncodeToString(nodeAnn.NodeID[:]),
            nodeAnn.Alias.String(),
            fmt.Sprintf("#%02x%02x%02x", nodeAnn.RGBColor.R, nodeAnn.RGBColor.G, nodeAnn.RGBColor.B),
            string(jsonBytes),
        )
        placeholders = append(placeholders, "(?, ?, ?, ?, NOW(), NOW())")

        count++

        if len(placeholders) >= batchSize {
            if err := executeBatchNodeAnnouncements(tx, placeholders, values); err != nil {
                return err
            }
            values = nil
            placeholders = nil
        }

        return nil
    })

    if err != nil {
        return err
    }

    if len(values) > 0 {
        if err := executeBatchNodeAnnouncements(tx, placeholders, values); err != nil {
            return err
        }
    }

    fmt.Printf("Processed %d node announcements\n", count)
    return nil
}

func executeBatchNodeAnnouncements(tx *sql.Tx, placeholders []string, values []interface{}) error {
    query := fmt.Sprintf(`
        INSERT INTO node_announcements 
        (node_id, alias, rgb_color, json_data, first_seen, last_seen)
        VALUES %s
        ON DUPLICATE KEY UPDATE
            alias = VALUES(alias),
            rgb_color = VALUES(rgb_color),
            json_data = VALUES(json_data),
            last_seen = VALUES(last_seen)
    `, strings.Join(placeholders, ","))

    _, err := tx.Exec(query, values...)
    if err != nil {
        return fmt.Errorf("failed to execute batch insert: %v", err)
    }

    return nil
}

func SendNodeAddresses(graph models.ChannelGraph, db *sql.DB) error {
    fmt.Println("Sending Node Addresses to MySQL:")

    tx, err := db.Begin()
    if err != nil {
        return fmt.Errorf("failed to begin transaction: %v", err)
    }
    defer func() {
        if err != nil {
            tx.Rollback()
        } else {
            tx.Commit()
        }
    }()

    var values []interface{}
    var placeholders []string
    count := 0

    err = graph.ForEachNode(func(dbTx walletdb.ReadTx, node *models.LightningNode) error {
        // Ensure not to hold onto dbTx or open new transactions within the callback

        for _, addr := range node.Addresses {
            host, port, err := net.SplitHostPort(addr.String())
            if err != nil {
                return fmt.Errorf("failed to parse address: %v", err)
            }

            portUint, err := strconv.ParseUint(port, 10, 16)
            if err != nil {
                return fmt.Errorf("failed to parse port: %v", err)
            }

            values = append(values,
                hex.EncodeToString(node.PubKeyBytes[:]),
                host,
                portUint,
            )
            placeholders = append(placeholders, "(?, ?, ?, NOW(), NOW())")

            count++

            if len(placeholders) >= batchSize {
                if err := executeBatchNodeAddresses(tx, placeholders, values); err != nil {
                    return err
                }
                values = nil
                placeholders = nil
            }
        }

        return nil
    })

    if err != nil {
        return err
    }

    if len(values) > 0 {
        if err := executeBatchNodeAddresses(tx, placeholders, values); err != nil {
            return err
        }
    }

    fmt.Printf("Processed %d node addresses\n", count)
    return nil
}

func executeBatchNodeAddresses(tx *sql.Tx, placeholders []string, values []interface{}) error {
    query := fmt.Sprintf(`
        INSERT INTO node_addresses 
        (node_id, address, port, first_seen, last_seen)
        VALUES %s
        ON DUPLICATE KEY UPDATE
            last_seen = VALUES(last_seen)
    `, strings.Join(placeholders, ","))

    _, err := tx.Exec(query, values...)
    if err != nil {
        return fmt.Errorf("failed to execute batch insert: %v", err)
    }

    return nil
}
