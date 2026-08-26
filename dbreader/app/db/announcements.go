// -----------------------------------------------------------
//  [*] db — graph → MySQL importers (announcements.go)
//
//  The three imports main.go runs on every sync, each one
//  MySQL transaction of multi-row INSERT ... ON DUPLICATE
//  KEY UPDATE statements, batchSize rows per statement. The
//  graph is walked through the models.ChannelGraph
//  interface; the row shapes are the Custom* wrappers in
//  models. Tables come from initialization.go.
//
//  All three share one transaction pattern — and one bug:
//  the deferred commit/rollback looks at the OUTER err, but
//  the tail batch after the walk runs inside an `if err :=`
//  that shadows it. When that last batch fails the function
//  returns the error while the defer sees err == nil and
//  COMMITS the batches before it. A failure during the walk
//  is fine — the callback's error surfaces through ForEach*
//  into the outer err and the transaction rolls back.
//  tx.Commit()'s own error is discarded either way. Known
//  bug, documented and not fixed in the restyle.
// -----------------------------------------------------------


package db

import (
	// Standard library
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"

	// LND
	graphdb "github.com/lightningnetwork/lnd/graph/db"
	"github.com/lightningnetwork/lnd/lnwire"

	// This module
	"lnd-dbreader/models"
)

const (
	// Rows per INSERT statement, not per transaction: every
	// row is one "(?, ...)" placeholder group, so the channel
	// statement binds up to 7 × 5000 values
	batchSize = 5000
)








// -----------------------------------------------------------
// SendChannelAnnouncements
// -----------------------------------------------------------
//
// Walks every channel with ForEachChannel and upserts the
// ANNOUNCEMENT half into channel_announcements: scid, the
// two node keys, the two bitcoin keys, opaque data and the
// JSON rendering (models.CustomChannelAnnouncement). The
// two edge policies the walk hands over are ignored — fees,
// CLTV deltas and disabled flags are not exported anywhere.
// short_channel_id is stored as the uint64; the JSON has it
// as "block x tx x out" — the two do not look alike.
//
// Used by:
//   - main.go processLNDDatabase — STEP 4, first of three
// -----------------------------------------------------------

func SendChannelAnnouncements(graph models.ChannelGraph, db *sql.DB) error {
	log.Printf("Importing channel announcements to MySQL")


	// STEP 1: one transaction for the whole import; the defer
	// decides commit vs rollback from the outer err — see the
	// file header for the shadowing bug
	// =======================================================
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


	// STEP 2: walk the graph, collecting one placeholder group
	// and its values per row, flushing every batchSize rows
	// ========================================================
	var values []interface{}
	var placeholders []string
	count := 0

	err = graph.ForEachChannel(func(edgeInfo *models.ChannelEdgeInfo, c1, c2 *models.ChannelEdgePolicy) error {
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

		jsonBytes, err := json.Marshal(chanAnn)
		if err != nil {
			return fmt.Errorf("failed to marshal channel announcement to JSON: %w", err)
		}

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

		// STEP 2.1: full batch — flush and start over (nil, so
		// the old backing arrays go to the GC)
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


	// STEP 3: the tail batch (under batchSize rows). The
	// `err :=` here is the shadowing described in the header
	// ======================================================
	if len(values) > 0 {
		if err := executeBatchChannelAnnouncements(tx, placeholders, values); err != nil {
			return err
		}
	}

	log.Printf("Successfully imported %d channel announcements", count)
	return nil
}








// -----------------------------------------------------------
// executeBatchChannelAnnouncements
// -----------------------------------------------------------
//
// One INSERT ... VALUES (...),(...) ON DUPLICATE KEY UPDATE
// for up to batchSize channel rows. Because unique_channel
// covers every inserted column, the UPDATE branch can only
// ever refresh last_seen (and rewrite json_data with the
// same content).
//
// Used by:
//   - SendChannelAnnouncements (above) — mid-walk and tail
// -----------------------------------------------------------

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








// -----------------------------------------------------------
// SendNodeAnnouncements
// -----------------------------------------------------------
//
// Walks every node with ForEachNode and upserts it into
// node_announcements: pubkey, alias, colour as #rrggbb and
// the JSON rendering (models.CustomNodeAnnouncement — id,
// alias, addresses, timestamp, colour; the Features and
// ExtraOpaqueData filled in here never reach the JSON).
// lnwire.NewNodeAlias rejects an alias over 32 bytes and
// that error aborts the WHOLE import — LND never stores
// one, so it does not happen in practice.
//
// Used by:
//   - main.go processLNDDatabase — STEP 4, second of three
// -----------------------------------------------------------

func SendNodeAnnouncements(graph models.ChannelGraph, db *sql.DB) error {
	log.Printf("Importing node announcements to MySQL")


	// STEP 1: one transaction for the whole import; the defer
	// decides commit vs rollback from the outer err — see the
	// file header for the shadowing bug
	// =======================================================
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


	// STEP 2: walk the graph, collecting one placeholder group
	// and its values per row, flushing every batchSize rows
	// ========================================================
	var values []interface{}
	var placeholders []string
	count := 0

	err = graph.ForEachNode(func(nodeTx graphdb.NodeRTx) error {
		node := nodeTx.Node()

		alias, err := lnwire.NewNodeAlias(node.Alias)
		if err != nil {
			return fmt.Errorf("failed to create node alias: %w", err)
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

		// STEP 2.1: full batch — flush and start over (nil, so
		// the old backing arrays go to the GC)
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


	// STEP 3: the tail batch (under batchSize rows). The
	// `err :=` here is the shadowing described in the header
	// ======================================================
	if len(values) > 0 {
		if err := executeBatchNodeAnnouncements(tx, placeholders, values); err != nil {
			return err
		}
	}

	log.Printf("Successfully imported %d node announcements", count)
	return nil
}








// -----------------------------------------------------------
// executeBatchNodeAnnouncements
// -----------------------------------------------------------
//
// Up to batchSize node rows in one statement. On a key hit
// (same node_id, alias, rgb_color) json_data — addresses
// and timestamp — is overwritten and last_seen bumped.
//
// Used by:
//   - SendNodeAnnouncements (above) — mid-walk and tail
// -----------------------------------------------------------

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








// -----------------------------------------------------------
// SendNodeAddresses
// -----------------------------------------------------------
//
// Walks every node and upserts one row per address into
// node_addresses. net.SplitHostPort splits "host:port"
// (bracketed IPv6 and .onion alike); an address it cannot
// split is stored whole with port 0, and an unparsable port
// also becomes 0 — the ParseUint error is dropped. The
// batch counter counts ADDRESSES, so one node can straddle
// two batches; harmless, rows are independent.
//
// Used by:
//   - main.go processLNDDatabase — STEP 4, last of three
// -----------------------------------------------------------

func SendNodeAddresses(graph models.ChannelGraph, db *sql.DB) error {
	log.Printf("Importing node addresses to MySQL")


	// STEP 1: one transaction for the whole import; the defer
	// decides commit vs rollback from the outer err — see the
	// file header for the shadowing bug
	// =======================================================
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


	// STEP 2: walk the graph, collecting one placeholder group
	// and its values per row, flushing every batchSize rows
	// ========================================================
	var values []interface{}
	var placeholders []string
	count := 0

	err = graph.ForEachNode(func(nodeTx graphdb.NodeRTx) error {
		node := nodeTx.Node()

		for _, addr := range node.Addresses {
			host, portStr, err := net.SplitHostPort(addr.String())
			if err != nil {
				// Nothing to split off — the whole string is the
				// address, port 0 marks "had none"
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

			// STEP 2.1: full batch — flush and start over (nil, so
			// the old backing arrays go to the GC)
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


	// STEP 3: the tail batch (under batchSize rows). The
	// `err :=` here is the shadowing described in the header
	// ======================================================
	if len(values) > 0 {
		if err := executeBatchNodeAddresses(tx, placeholders, values); err != nil {
			return err
		}
	}

	log.Printf("Successfully imported %d node addresses", count)
	return nil
}








// -----------------------------------------------------------
// executeBatchNodeAddresses
// -----------------------------------------------------------
//
// Up to batchSize address rows in one statement. The UPDATE
// branch re-sets address and port to the values that just
// matched the key, so only last_seen actually changes.
//
// Used by:
//   - SendNodeAddresses (above) — mid-walk and tail
// -----------------------------------------------------------

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
