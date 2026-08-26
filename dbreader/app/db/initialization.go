/*
Package db provides database initialization for LND graph data storage.

This file contains the MySQL table definitions required for storing
channel announcements, node announcements, and node addresses from
LND v0.19.1 graph database.
*/
package db

import (
	"database/sql"
	"fmt"
	"log"
)

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

const createNodeAddressesTable = `
CREATE TABLE IF NOT EXISTS node_addresses ( 
  id BIGINT UNSIGNED AUTO_INCREMENT NOT NULL,
  node_id VARCHAR(66) NOT NULL,
  address VARCHAR(255) NOT NULL,
  port INT UNSIGNED NOT NULL,
  first_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  last_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (id),
  CONSTRAINT unique_address UNIQUE (node_id, address, port)
) ENGINE = InnoDB;
`

// InitializeDatabaseTables creates the required MySQL tables if they don't exist
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
