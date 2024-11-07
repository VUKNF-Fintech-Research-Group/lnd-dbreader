package db

import (
    "database/sql"
    "fmt"
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
    last_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY unique_channel (short_channel_id, node_id_1, node_id_2, bitcoin_key_1, bitcoin_key_2, extra_opaque_data(255))
);
`

const createNodeAnnouncementsTable = `
CREATE TABLE IF NOT EXISTS node_announcements (
    id BIGINT UNSIGNED AUTO_INCREMENT NOT NULL,
    node_id VARCHAR(66) NULL,
    alias VARCHAR(255) NULL,
    rgb_color VARCHAR(7) NULL,
    json_data JSON NULL,
    first_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY unique_node (node_id, alias, rgb_color)
);
`

const createNodeAddressesTable = `
CREATE TABLE IF NOT EXISTS node_addresses (
    id BIGINT UNSIGNED AUTO_INCREMENT NOT NULL,
    node_id VARCHAR(66) NOT NULL,
    address VARCHAR(255) NOT NULL,
    port INT UNSIGNED NOT NULL,
    first_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen TIMESTAMP NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY unique_address (node_id, address, port)
);
`

func InitializeDatabaseTables(db *sql.DB) error {
    _, err := db.Exec(createChannelAnnouncementsTable)
    if err != nil {
        return fmt.Errorf("failed to create channel_announcements table: %v", err)
    }

    _, err = db.Exec(createNodeAnnouncementsTable)
    if err != nil {
        return fmt.Errorf("failed to create node_announcements table: %v", err)
    }

    _, err = db.Exec(createNodeAddressesTable)
    if err != nil {
        return fmt.Errorf("failed to create node_addresses table: %v", err)
    }

    return nil
}
