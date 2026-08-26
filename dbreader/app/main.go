/*
LND Database Reader v0.19.1

A service that continuously reads Lightning Network Daemon (LND) channel graph data
and synchronizes it to a MySQL database. This application is compatible with LND v0.19.1-beta
and handles the new graph database architecture introduced in that version.

Features:
- Continuous sync with configurable intervals
- Graceful shutdown handling
- Database lock avoidance through file copying
- Robust error handling and recovery
- Batch processing for performance

Environment Variables:
- MYSQL_HOST: MySQL server hostname (default: lnd-dbreader-mysql)
- MYSQL_PORT: MySQL server port (default: 3306)
- MYSQL_USER: MySQL username (default: lnd-dbreader)
- MYSQL_PASSWORD: MySQL password (default: lnd-dbreader)
- MYSQL_DATABASE: MySQL database name (default: lnd-dbreader)
- LND_DB_PATH: Path to LND channel.db file (default: /data/channel.db)
- SYNC_INTERVAL_MINUTES: Sync interval in minutes (default: 30)
*/
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lnd-dbreader/db"
	"lnd-dbreader/models"

	_ "github.com/go-sql-driver/mysql"
	"github.com/lightningnetwork/lnd/kvdb"
	graphdb "github.com/lightningnetwork/lnd/graph/db"
)

const (
	// Application metadata
	appName    = "LND Database Reader"
	appVersion = "v0.19.1"
	
	// Default configuration values
	defaultSyncInterval = 30 * time.Minute
	defaultDBTimeout    = 10 * time.Second
	
	// Graph configuration
	defaultRejectCacheSize  = 1000
	defaultChannelCacheSize = 20000
	
	// Temporary file path for database copying
	tempDatabasePath = "/tmp/channel_copy.db"
)

// Config holds the application configuration
type Config struct {
	MySQL        MySQLConfig
	LNDDBPath    string
	SyncInterval time.Duration
}

// MySQLConfig holds MySQL connection configuration
type MySQLConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// loadConfig loads configuration from environment variables
func loadConfig() *Config {
	syncIntervalStr := getEnv("SYNC_INTERVAL_MINUTES", "30")
	syncInterval := defaultSyncInterval
	
	if intervalMinutes, err := time.ParseDuration(syncIntervalStr + "m"); err == nil {
		syncInterval = intervalMinutes
	}

	return &Config{
		MySQL: MySQLConfig{
			Host:     getEnv("MYSQL_HOST", "lnd-dbreader-mysql"),
			Port:     getEnv("MYSQL_PORT", "3306"),
			User:     getEnv("MYSQL_USER", "lnd-dbreader"),
			Password: getEnv("MYSQL_PASSWORD", "lnd-dbreader"),
			Database: getEnv("MYSQL_DATABASE", "lnd-dbreader"),
		},
		LNDDBPath:    getEnv("LND_DB_PATH", "/data/channel.db"),
		SyncInterval: syncInterval,
	}
}

// copyDatabase creates a copy of the source database file to avoid locking issues
func copyDatabase(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

// processLNDDatabase handles a single iteration of reading and importing LND data
func processLNDDatabase(lndDbPath string, mysqlDB *sql.DB) error {
	log.Printf("Starting LND database processing")

	// Copy database to temporary location to avoid lock issues
	if err := copyDatabase(lndDbPath, tempDatabasePath); err != nil {
		return fmt.Errorf("failed to copy database: %w", err)
	}

	// Ensure temp file is cleaned up
	defer func() {
		if err := os.Remove(tempDatabasePath); err != nil {
			log.Printf("Warning: Failed to remove temporary database file: %v", err)
		}
	}()

	log.Printf("Database copied successfully")

	// Initialize LND components
	kvdbBackend, err := kvdb.Open(kvdb.BoltBackendName, tempDatabasePath, true, defaultDBTimeout, false)
	if err != nil {
		return fmt.Errorf("failed to open LND database backend: %w", err)
	}
	defer func() {
		if err := kvdbBackend.Close(); err != nil {
			log.Printf("Warning: Failed to close database backend: %v", err)
		}
	}()

	// Create channeldb instance
	dbDir := filepath.Dir(tempDatabasePath)
	dbInstance, err := models.Open(dbDir)
	if err != nil {
		return fmt.Errorf("failed to open LND database: %w", err)
	}
	defer func() {
		if err := dbInstance.Close(); err != nil {
			log.Printf("Warning: Failed to close database instance: %v", err)
		}
	}()

	// Create channel graph instance
	graphConfig := &graphdb.Config{
		KVDB: kvdbBackend,
		KVStoreOpts: []graphdb.KVStoreOptionModifier{
			graphdb.WithRejectCacheSize(defaultRejectCacheSize),
			graphdb.WithChannelCacheSize(defaultChannelCacheSize),
		},
	}

	chanGraphOpts := []graphdb.ChanGraphOption{
		graphdb.WithUseGraphCache(true),
	}

	graph, err := graphdb.NewChannelGraph(graphConfig, chanGraphOpts...)
	if err != nil {
		return fmt.Errorf("failed to create channel graph: %w", err)
	}

	// Start the graph
	if err := graph.Start(); err != nil {
		return fmt.Errorf("failed to start channel graph: %w", err)
	}
	defer func() {
		if err := graph.Stop(); err != nil {
			log.Printf("Warning: Failed to stop graph: %v", err)
		}
	}()

	log.Printf("Importing data to MySQL")

	// Initialize database tables
	if err := db.InitializeDatabaseTables(mysqlDB); err != nil {
		return fmt.Errorf("failed to initialize database tables: %w", err)
	}

	// Import data in sequence
	log.Printf("Processing channel announcements")
	if err := db.SendChannelAnnouncements(graph, mysqlDB); err != nil {
		return fmt.Errorf("failed to import channel announcements: %w", err)
	}

	log.Printf("Processing node announcements")
	if err := db.SendNodeAnnouncements(graph, mysqlDB); err != nil {
		return fmt.Errorf("failed to import node announcements: %w", err)
	}

	log.Printf("Processing node addresses")
	if err := db.SendNodeAddresses(graph, mysqlDB); err != nil {
		return fmt.Errorf("failed to import node addresses: %w", err)
	}

	log.Printf("Successfully completed data import")
	return nil
}

// setupGracefulShutdown sets up signal handling for graceful shutdown
func setupGracefulShutdown() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, initiating graceful shutdown", sig)
		cancel()
	}()

	return ctx, cancel
}

// connectToMySQL establishes and tests MySQL connection
func connectToMySQL(config MySQLConfig) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", 
		config.User, config.Password, config.Host, config.Port, config.Database)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping MySQL: %w", err)
	}

	return db, nil
}

func main() {
	log.Printf("Starting %s %s", appName, appVersion)

	// Load configuration
	config := loadConfig()
	
	log.Printf("Configuration:")
	log.Printf("  LND DB Path: %s", config.LNDDBPath)
	log.Printf("  MySQL: %s:***@tcp(%s:%s)/%s", 
		config.MySQL.User, config.MySQL.Host, config.MySQL.Port, config.MySQL.Database)
	log.Printf("  Sync Interval: %v", config.SyncInterval)

	// Connect to MySQL
	mysqlDB, err := connectToMySQL(config.MySQL)
	if err != nil {
		log.Fatalf("MySQL connection failed: %v", err)
	}
	defer func() {
		if err := mysqlDB.Close(); err != nil {
			log.Printf("Warning: Failed to close MySQL connection: %v", err)
		} else {
			log.Printf("MySQL connection closed")
		}
	}()

	log.Printf("MySQL connection established successfully")

	// Set up graceful shutdown
	ctx, cancel := setupGracefulShutdown()
	defer cancel()

	// Run initial sync
	separator := strings.Repeat("=", 80)
	fmt.Printf("\n%s\n", separator)
	fmt.Printf("INITIAL SYNC - %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf("%s\n", separator)

	if err := processLNDDatabase(config.LNDDBPath, mysqlDB); err != nil {
		log.Printf("ERROR during initial sync: %v", err)
		log.Printf("Will retry in %v", config.SyncInterval)
	} else {
		log.Printf("✅ Initial sync completed successfully!")
	}

	// Start continuous sync loop
	ticker := time.NewTicker(config.SyncInterval)
	defer ticker.Stop()

	syncCount := 1

	for {
		select {
		case <-ctx.Done():
			log.Printf("Shutdown signal received, exiting gracefully")
			return

		case <-ticker.C:
			syncCount++
			fmt.Printf("\n%s\n", separator)
			fmt.Printf("SYNC #%d - %s\n", syncCount, time.Now().Format("2006-01-02 15:04:05"))
			fmt.Printf("%s\n", separator)

			if err := processLNDDatabase(config.LNDDBPath, mysqlDB); err != nil {
				log.Printf("❌ ERROR during sync #%d: %v", syncCount, err)
				log.Printf("Will retry in %v", config.SyncInterval)
			} else {
				log.Printf("✅ Sync #%d completed successfully!", syncCount)
				log.Printf("Next sync scheduled in %v", config.SyncInterval)
			}
		}
	}
}