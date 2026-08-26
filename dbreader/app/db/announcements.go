/*
Package db provides database operations for importing LND graph data into MySQL.

This package handles the import of channel announcements, node announcements,
and node addresses from LND v0.19.1 graph database into MySQL for analysis
and monitoring purposes.
*/
package db

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	"github.com/lightningnetwork/lnd/lnwire"
	graphdb "github.com/lightningnetwork/lnd/graph/db"
	"lnd-dbreader/models"
)

const (
	// batchSize defines the number of records to process in a single database transaction
	batchSize = 5000
)

// SendChannelAnnouncements imports all channel announcements from the LND graph to MySQL
func SendChannelAnnouncements(graph models.ChannelGraph, db *sql.DB) error {
	log.Printf("Importing channel announcements to MySQL")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
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
		// Create channel announcement wrapper
		chanAnn := models.CustomChannelAnnouncement{
			ChannelAnnouncement1: &lnwire.ChannelAnnouncement1{
				ChainHash:       edgeInfo.ChainHash,
				ShortChannelID:  lnwire.NewShortChanIDFromInt(edgeInfo.ChannelID),
				NodeID1:         edgeInfo.NodeKey1Bytes,
				NodeID2:         edgeInfo.NodeKey2Bytes,
				BitcoinKey1:     edgeInfo.BitcoinKey1Bytes,
				BitcoinKey2:     edgeInfo.BitcoinKey2Bytes,
				ExtraOpaqueData: edgeInfo.ExtraOpaqueData,
			},
		}

		// Serialize to JSON
		jsonBytes, err := json.Marshal(chanAnn)
		if err != nil {
			return fmt.Errorf("failed to marshal channel announcement to JSON: %w", err)
		}

		// Extract data for database insertion
		shortChannelIDInt := chanAnn.SCID().ToUint64()
		node1Bytes := chanAnn.Node1KeyBytes()
		node2Bytes := chanAnn.Node2KeyBytes()

		values = append(values,
			shortChannelIDInt,
			hex.EncodeToString(node1Bytes[:]),
			hex.EncodeToString(node2Bytes[:]),
			hex.EncodeToString(edgeInfo.BitcoinKey1Bytes[:]),
			hex.EncodeToString(edgeInfo.BitcoinKey2Bytes[:]),
			hex.EncodeToString(edgeInfo.ExtraOpaqueData),
			string(jsonBytes),
		)
		placeholders = append(placeholders, "(?, ?, ?, ?, ?, ?, ?, NOW(), NOW())")

		count++

		// Process batch when limit reached
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
		return fmt.Errorf("failed to iterate channels: %w", err)
	}

	// Process remaining records
	if len(values) > 0 {
		if err := executeBatchChannelAnnouncements(tx, placeholders, values); err != nil {
			return err
		}
	}

	log.Printf("Successfully imported %d channel announcements", count)
	return nil
}

// executeBatchChannelAnnouncements executes a batch insert for channel announcements
func executeBatchChannelAnnouncements(tx *sql.Tx, placeholders []string, values []interface{}) error {
	query := `INSERT INTO channel_announcements 
		(short_channel_id, node_id_1, node_id_2, bitcoin_key_1, bitcoin_key_2, extra_opaque_data, json_data, first_seen, last_seen) 
		VALUES ` + strings.Join(placeholders, ",") + ` 
		ON DUPLICATE KEY UPDATE 
		node_id_1 = VALUES(node_id_1),
		node_id_2 = VALUES(node_id_2), 
		bitcoin_key_1 = VALUES(bitcoin_key_1),
		bitcoin_key_2 = VALUES(bitcoin_key_2),
		extra_opaque_data = VALUES(extra_opaque_data),
		json_data = VALUES(json_data),
		last_seen = NOW()`

	_, err := tx.Exec(query, values...)
	if err != nil {
		return fmt.Errorf("failed to execute batch insert: %w", err)
	}

	return nil
}

// SendNodeAnnouncements imports all node announcements from the LND graph to MySQL
func SendNodeAnnouncements(graph models.ChannelGraph, db *sql.DB) error {
	log.Printf("Importing node announcements to MySQL")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
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

	err = graph.ForEachNode(func(nodeTx graphdb.NodeRTx) error {
		node := nodeTx.Node()
		
		// Create node alias
		alias, err := lnwire.NewNodeAlias(node.Alias)
		if err != nil {
			return fmt.Errorf("failed to create node alias: %w", err)
		}

		// Create node announcement wrapper
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

		// Serialize to JSON
		jsonBytes, err := json.Marshal(nodeAnn)
		if err != nil {
			return fmt.Errorf("failed to marshal node announcement to JSON: %w", err)
		}

		values = append(values,
			hex.EncodeToString(node.PubKeyBytes[:]),
			alias.String(),
			fmt.Sprintf("#%02x%02x%02x", node.Color.R, node.Color.G, node.Color.B),
			string(jsonBytes),
		)
		placeholders = append(placeholders, "(?, ?, ?, ?, NOW(), NOW())")

		count++

		// Process batch when limit reached
		if count%batchSize == 0 {
			if err := executeBatchNodeAnnouncements(tx, placeholders, values); err != nil {
				return err
			}
			values = nil
			placeholders = nil
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to iterate nodes: %w", err)
	}

	// Process remaining records
	if len(values) > 0 {
		if err := executeBatchNodeAnnouncements(tx, placeholders, values); err != nil {
			return err
		}
	}

	log.Printf("Successfully imported %d node announcements", count)
	return nil
}

// executeBatchNodeAnnouncements executes a batch insert for node announcements
func executeBatchNodeAnnouncements(tx *sql.Tx, placeholders []string, values []interface{}) error {
	query := `INSERT INTO node_announcements 
		(node_id, alias, rgb_color, json_data, first_seen, last_seen) 
		VALUES ` + strings.Join(placeholders, ",") + ` 
		ON DUPLICATE KEY UPDATE 
		alias = VALUES(alias),
		rgb_color = VALUES(rgb_color),
		json_data = VALUES(json_data),
		last_seen = NOW()`

	_, err := tx.Exec(query, values...)
	if err != nil {
		return fmt.Errorf("failed to execute batch insert: %w", err)
	}

	return nil
}

// SendNodeAddresses imports all node addresses from the LND graph to MySQL
func SendNodeAddresses(graph models.ChannelGraph, db *sql.DB) error {
	log.Printf("Importing node addresses to MySQL")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
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

	err = graph.ForEachNode(func(nodeTx graphdb.NodeRTx) error {
		node := nodeTx.Node()

		for _, addr := range node.Addresses {
			host, portStr, err := net.SplitHostPort(addr.String())
			if err != nil {
				// Handle addresses without port
				host = addr.String()
				portStr = "0"
			}

			port, _ := strconv.ParseUint(portStr, 10, 32)

			values = append(values,
				hex.EncodeToString(node.PubKeyBytes[:]),
				host,
				uint32(port),
			)
			placeholders = append(placeholders, "(?, ?, ?, NOW(), NOW())")

			count++

			// Process batch when limit reached
			if count%batchSize == 0 {
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
		return fmt.Errorf("failed to iterate node addresses: %w", err)
	}

	// Process remaining records
	if len(values) > 0 {
		if err := executeBatchNodeAddresses(tx, placeholders, values); err != nil {
			return err
		}
	}

	log.Printf("Successfully imported %d node addresses", count)
	return nil
}

// executeBatchNodeAddresses executes a batch insert for node addresses
func executeBatchNodeAddresses(tx *sql.Tx, placeholders []string, values []interface{}) error {
	query := `INSERT INTO node_addresses 
		(node_id, address, port, first_seen, last_seen) 
		VALUES ` + strings.Join(placeholders, ",") + ` 
		ON DUPLICATE KEY UPDATE 
		address = VALUES(address),
		port = VALUES(port),
		last_seen = NOW()`

	_, err := tx.Exec(query, values...)
	if err != nil {
		return fmt.Errorf("failed to execute batch insert: %w", err)
	}

	return nil
}
