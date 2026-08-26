// -----------------------------------------------------------
//  [*] db — graph → MySQL importers (announcements.go)
//
//  The three imports main.go runs on every sync, each one
//  MySQL transaction of multi-row INSERT ... ON DUPLICATE
//  KEY UPDATE statements, batchSize rows per statement. The
//  graph is walked through the models.ChannelGraph
//  interface; the row shapes are the Custom* wrappers in
//  models. Tables come from initialization.go. What is
//  exported is the graph's TOPOLOGY — channels, nodes,
//  addresses; channel policies (fees, limits, disabled) are
//  intentionally left out, see SendChannelAnnouncements.
//
//  All three share one pattern: every batch statement is
//  its own autocommit transaction, nothing spans the walk.
//  One transaction per sync used to hold every row lock of
//  the whole import until the final commit and blew InnoDB's
//  lock table (Error 1206) once the tables had grown. A sync
//  is therefore NOT all-or-nothing: a failure at batch N
//  leaves batches 1..N-1 committed — fine, every row is an
//  idempotent upsert and the next sync re-applies it all.
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
// JSON rendering (models.CustomChannelAnnouncement).
//
// By design, TOPOLOGY ONLY: the two directed edge policies
// the walk hands over (c1, c2 — fees, CLTV delta, HTLC
// limits, the disabled flag, last update) are deliberately
// not exported. If routing economics are ever needed they
// belong in a fourth table keyed (short_channel_id,
// node_id), one row per direction, with a fourth importer —
// not in this one.
//
// short_channel_id is stored as the uint64; the JSON has it
// as "block x tx x out" — the two do not look alike.
//
// Used by:
//   - main.go processLNDDatabase — STEP 4, first of three
// -----------------------------------------------------------

func SendChannelAnnouncements(graph models.ChannelGraph, db *sql.DB) error {
	log.Printf("Importing channel announcements to MySQL")


	// STEP 1: walk the graph, collecting one placeholder group
	// and its values per row, flushing every batchSize rows —
	// each flush commits on its own
	// ========================================================
	var values []interface{}
	var placeholders []string
	count := 0

	err := graph.ForEachChannel(func(edgeInfo *models.ChannelEdgeInfo, c1, c2 *models.ChannelEdgePolicy) error {
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

		// STEP 1.1: full batch — flush and start over (nil, so
		// the old backing arrays go to the GC)
		if count%batchSize == 0 {
			if err := executeBatchChannelAnnouncements(db, placeholders, values); err != nil {
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


	// STEP 2: the tail batch (under batchSize rows)
	// =============================================
	if len(values) > 0 {
		if err := executeBatchChannelAnnouncements(db, placeholders, values); err != nil {
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
// same content). Runs as its own autocommit transaction,
// so its row locks are gone the moment it returns.
//
// Used by:
//   - SendChannelAnnouncements (above) — mid-walk and tail
// -----------------------------------------------------------

func executeBatchChannelAnnouncements(db *sql.DB, placeholders []string, values []interface{}) error {
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

	_, err := db.Exec(query, values...)
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


	// STEP 1: walk the graph, collecting one placeholder group
	// and its values per row, flushing every batchSize rows —
	// each flush commits on its own
	// ========================================================
	var values []interface{}
	var placeholders []string
	count := 0

	err := graph.ForEachNode(func(nodeTx graphdb.NodeRTx) error {
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

		// STEP 1.1: full batch — flush and start over (nil, so
		// the old backing arrays go to the GC)
		if count%batchSize == 0 {
			if err := executeBatchNodeAnnouncements(db, placeholders, values); err != nil {
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


	// STEP 2: the tail batch (under batchSize rows)
	// =============================================
	if len(values) > 0 {
		if err := executeBatchNodeAnnouncements(db, placeholders, values); err != nil {
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
// and timestamp — is overwritten and last_seen bumped. Own
// autocommit transaction, locks released on return.
//
// Used by:
//   - SendNodeAnnouncements (above) — mid-walk and tail
// -----------------------------------------------------------

func executeBatchNodeAnnouncements(db *sql.DB, placeholders []string, values []interface{}) error {
	query := `INSERT INTO node_announcements 
		(node_id, alias, rgb_color, json_data, first_seen, last_seen) 
		VALUES ` + strings.Join(placeholders, ",") + ` 
		ON DUPLICATE KEY UPDATE 
		alias = VALUES(alias),
		rgb_color = VALUES(rgb_color),
		json_data = VALUES(json_data),
		last_seen = NOW()`

	_, err := db.Exec(query, values...)
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


	// STEP 1: walk the graph, collecting one placeholder group
	// and its values per row, flushing every batchSize rows —
	// each flush commits on its own
	// ========================================================
	var values []interface{}
	var placeholders []string
	count := 0

	err := graph.ForEachNode(func(nodeTx graphdb.NodeRTx) error {
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

			// STEP 1.1: full batch — flush and start over (nil, so
			// the old backing arrays go to the GC)
			if count%batchSize == 0 {
				if err := executeBatchNodeAddresses(db, placeholders, values); err != nil {
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


	// STEP 2: the tail batch (under batchSize rows)
	// =============================================
	if len(values) > 0 {
		if err := executeBatchNodeAddresses(db, placeholders, values); err != nil {
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
// matched the key, so only last_seen actually changes. Own
// autocommit transaction, locks released on return.
//
// Used by:
//   - SendNodeAddresses (above) — mid-walk and tail
// -----------------------------------------------------------

func executeBatchNodeAddresses(db *sql.DB, placeholders []string, values []interface{}) error {
	query := `INSERT INTO node_addresses 
		(node_id, address, port, first_seen, last_seen) 
		VALUES ` + strings.Join(placeholders, ",") + ` 
		ON DUPLICATE KEY UPDATE 
		address = VALUES(address),
		port = VALUES(port),
		last_seen = NOW()`

	_, err := db.Exec(query, values...)
	if err != nil {
		return fmt.Errorf("failed to execute batch insert: %w", err)
	}

	return nil
}
