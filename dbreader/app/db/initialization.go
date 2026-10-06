// -----------------------------------------------------------
//  [*] db — the MySQL schema (initialization.go)
//
//  The three tables the importers in announcements.go write
//  to, created with IF NOT EXISTS on every sync. Every table
//  is APPEND-ONLY history: rows are upserted, never deleted,
//  so a channel or node that leaves the graph keeps its row
//  with a stale last_seen. The UNIQUE constraint decides
//  what counts as "the same row" — see each statement.
// -----------------------------------------------------------


package db

import (
	// Standard library
	"database/sql"
	"fmt"
	"log"
)








// -----------------------------------------------------------
// createChannelAnnouncementsTable
// -----------------------------------------------------------
//
// unique_channel spans EVERY announced column. An index can
// only cover a TEXT column by a prefix, so extra_opaque_data
// takes part by its first 255 characters — of the hex, so
// about 127 bytes of the raw data: a re-announcement whose
// opaque data differs inside that prefix is a NEW row, a
// difference past it updates the old row in place. LND
// keeps one announcement per channel, so in practice the
// upsert in announcements.go just bumps last_seen.
// json_data is outside the key.
//
// Used by:
//   - InitializeDatabaseTables (below)
// -----------------------------------------------------------

const createChannelAnnouncementsTable = `
CREATE TABLE IF NOT EXISTS channel_announcements ( 
  id BIGINT UNSIGNED AUTO_INCREMENT NOT NULL,
  short_channel_id BIGINT UNSIGNED NULL,
  node_id_1 VARCHAR(66) NULL,
  node_id_2 VARCHAR(66) NULL,
  bitcoin_key_1 VARCHAR(66) NULL,
  bitcoin_key_2 VARCHAR(66) NULL,
  extra_opaque_data TEXT NULL,
  json_data JSON NULL,
  first_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT unique_channel UNIQUE (short_channel_id, node_id_1, node_id_2, bitcoin_key_1, bitcoin_key_2, extra_opaque_data(255))
) ENGINE = InnoDB;
`








// -----------------------------------------------------------
// createNodeAnnouncementsTable
// -----------------------------------------------------------
//
// unique_node is (node_id, alias, rgb_color): a node that
// renames or recolours itself gets a new row and the old
// one stays as history. json_data (addresses, timestamp) is
// outside the key and is overwritten in place.
//
// Used by:
//   - InitializeDatabaseTables (below)
// -----------------------------------------------------------

const createNodeAnnouncementsTable = `
CREATE TABLE IF NOT EXISTS node_announcements ( 
  id BIGINT UNSIGNED AUTO_INCREMENT NOT NULL,
  node_id VARCHAR(66) NULL,
  alias VARCHAR(255) NULL,
  rgb_color VARCHAR(7) NULL,
  json_data JSON NULL,
  first_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT unique_node UNIQUE (node_id, alias, rgb_color)
) ENGINE = InnoDB;
`








// -----------------------------------------------------------
// createNodeAddressesTable
// -----------------------------------------------------------
//
// One row per (node_id, address, port). port is 0 when the
// address had none (see SendNodeAddresses). Addresses a
// node drops stay with their old last_seen.
//
// address is sized for the longest the gossip protocol
// allows. An address LND cannot decode is stored as the hex
// of everything from its type byte to the end of the
// node's address list, and that list may fill a whole
// node_announcement: 65,393 bytes once the fixed fields are
// paid for, 130,786 hex characters — past TEXT's 65,535
// bytes, hence MEDIUMTEXT. Every decodable address is far
// shorter (a DNS hostname is at most 255).
//
// unique_address covers the first 255 characters of
// address: every decodable address in full, an over-long
// undecodable one by its prefix, as extra_opaque_data is in
// channel_announcements. Fixed 2026-10-06: the column was a
// VARCHAR(255), which such an address outgrew, and MySQL's
// strict mode then rejected the whole batch. IF NOT EXISTS
// never alters an existing table — an older database needs
// the ALTER TABLE in the README's schema section.
//
// Used by:
//   - InitializeDatabaseTables (below)
// -----------------------------------------------------------

const createNodeAddressesTable = `
CREATE TABLE IF NOT EXISTS node_addresses ( 
  id BIGINT UNSIGNED AUTO_INCREMENT NOT NULL,
  node_id VARCHAR(66) NOT NULL,
  address MEDIUMTEXT NOT NULL,
  port INT UNSIGNED NOT NULL,
  first_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT unique_address UNIQUE (node_id, address(255), port)
) ENGINE = InnoDB;
`








// -----------------------------------------------------------
// InitializeDatabaseTables
// -----------------------------------------------------------
//
// Runs the three CREATE TABLE IF NOT EXISTS statements in
// order, outside any transaction (DDL commits implicitly in
// MySQL anyway). Cheap, so it runs on every sync.
//
// Used by:
//   - main.go processLNDDatabase — STEP 3 of every sync
// -----------------------------------------------------------

func InitializeDatabaseTables(db *sql.DB) error {
	tables := []struct {
		name string
		sql  string
	}{
		{"channel_announcements", createChannelAnnouncementsTable},
		{"node_announcements", createNodeAnnouncementsTable},
		{"node_addresses", createNodeAddressesTable},
	}

	for _, table := range tables {
		if _, err := db.Exec(table.sql); err != nil {
			return fmt.Errorf("failed to create table %s: %w", table.name, err)
		}
	}

	log.Printf("Database tables initialized successfully")
	return nil
}
