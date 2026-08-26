// -----------------------------------------------------------
//  [*] dbreader — LND channel graph → MySQL, on a timer
//
//  The whole service in one process: copy the node's
//  channel.db aside, open the copy through LND's own graph
//  package and upsert every channel, node and address into
//  MySQL — once at start, then every SYNC_INTERVAL_MINUTES.
//  The live channel.db is never opened: bbolt takes an
//  exclusive lock and the LND container next door holds it.
//
//  Environment (every variable optional, defaults in
//  loadConfig):
//    MYSQL_HOST, MYSQL_PORT, MYSQL_USER, MYSQL_PASSWORD,
//    MYSQL_DATABASE         — the target database
//    LND_DB_PATH            — the node's channel.db
//    SYNC_INTERVAL_MINUTES  — minutes between syncs
//
//  A failed sync never stops the loop: it is logged and the
//  next tick tries again. Tables are (re)created on EVERY
//  sync with IF NOT EXISTS, so a wiped database heals
//  itself. go.mod pins LND v0.19.1-beta — the graph API
//  moved in 0.19 and the models package wraps that
//  version's types.
//
//  Split into (main last):
//
//    Config, MySQLConfig    — the parsed environment
//    getEnv, loadConfig     — env → Config
//    copyDatabase           — the lock-avoiding file copy
//    processLNDDatabase     — one full sync
//    setupGracefulShutdown  — SIGINT/SIGTERM → ctx cancel
//    connectToMySQL         — open + ping
//    main                   — initial sync, then the ticker
// -----------------------------------------------------------


package main

import (
	// Standard library
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// This module
	"lnd-dbreader/db"

	// MySQL driver (registers itself) and LND's graph store
	_ "github.com/go-sql-driver/mysql"
	graphdb "github.com/lightningnetwork/lnd/graph/db"
	"github.com/lightningnetwork/lnd/kvdb"
)

const (
	// Only ever printed — the startup log line
	appName    = "LND Database Reader"
	appVersion = "v0.19.1"

	// Sync cadence when SYNC_INTERVAL_MINUTES is unset or
	// unparsable; bbolt open timeout for the copied file
	defaultSyncInterval = 30 * time.Minute
	defaultDBTimeout    = 10 * time.Second

	// Cache sizes handed to LND's graphdb — compare its own
	// DefaultRejectCacheSize / DefaultChannelCacheSize
	defaultRejectCacheSize  = 1000
	defaultChannelCacheSize = 20000

	// Where the live channel.db is copied before opening;
	// removed after every sync
	tempDatabasePath = "/tmp/channel_copy.db"
)








// -----------------------------------------------------------
// Config
// -----------------------------------------------------------
//
// Everything the service reads from the environment, parsed
// once by loadConfig. MySQLConfig is nested so the DB half
// can be handed to connectToMySQL on its own.
//
// Used by:
//   - loadConfig, main (below)
// -----------------------------------------------------------

type Config struct {
	MySQL        MySQLConfig
	LNDDBPath    string
	SyncInterval time.Duration
}








// -----------------------------------------------------------
// MySQLConfig
// -----------------------------------------------------------
//
// Host/port/user/password/database as strings straight from
// the environment — Port is never parsed, only formatted
// back into the DSN.
//
// Used by:
//   - Config (above), connectToMySQL, main (below)
// -----------------------------------------------------------

type MySQLConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}








// -----------------------------------------------------------
// getEnv
// -----------------------------------------------------------
//
// os.Getenv with a default. An EMPTY value counts as unset:
// `MYSQL_PASSWORD=` in compose yields the default password,
// not an empty one.
//
// Used by:
//   - loadConfig (below)
// -----------------------------------------------------------

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}








// -----------------------------------------------------------
// loadConfig
// -----------------------------------------------------------
//
// Builds Config from the environment. SYNC_INTERVAL_MINUTES
// is parsed by appending "m" and handing the result to
// time.ParseDuration, so "1.5" works too; anything that does
// not parse falls back to defaultSyncInterval with a warning
// in the log.
//
// Used by:
//   - main (below)
// -----------------------------------------------------------

func loadConfig() *Config {
	syncIntervalStr := getEnv("SYNC_INTERVAL_MINUTES", "30")
	syncInterval := defaultSyncInterval

	if intervalMinutes, err := time.ParseDuration(syncIntervalStr + "m"); err == nil {
		syncInterval = intervalMinutes
	} else {
		log.Printf("Warning: SYNC_INTERVAL_MINUTES=%q is not a number, using %v", syncIntervalStr, defaultSyncInterval)
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








// -----------------------------------------------------------
// copyDatabase
// -----------------------------------------------------------
//
// Plain io.Copy of the live channel.db to tempDatabasePath.
// Nothing locks out the writer: LND may be mid-transaction,
// in which case the copy can be inconsistent and the bbolt
// open in processLNDDatabase fails — that sync errors out
// and the next tick copies again. os.Create truncates, so a
// leftover copy is never appended to.
//
// Used by:
//   - processLNDDatabase (below)
// -----------------------------------------------------------

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








// -----------------------------------------------------------
// processLNDDatabase
// -----------------------------------------------------------
//
// One full sync: copy → open through LND → three imports.
// Every resource is released by a defer, in reverse order
// of acquisition, and the copy is removed LAST — after the
// graph and the bbolt backend are closed.
//
// Used by:
//   - main (below) — the initial sync and every tick
// -----------------------------------------------------------

func processLNDDatabase(lndDbPath string, mysqlDB *sql.DB) error {
	log.Printf("Starting LND database processing")


	// STEP 1: copy the live file aside; the defer that removes
	// the copy is registered first, so it runs last
	// ========================================================
	if err := copyDatabase(lndDbPath, tempDatabasePath); err != nil {
		return fmt.Errorf("failed to copy database: %w", err)
	}

	defer func() {
		if err := os.Remove(tempDatabasePath); err != nil {
			log.Printf("Warning: Failed to remove temporary database file: %v", err)
		}
	}()

	log.Printf("Database copied successfully")


	// STEP 2: open the copy read-only as a bbolt backend and
	// build LND's graph on top of it
	// ======================================================
	kvdbBackend, err := kvdb.Open(kvdb.BoltBackendName, tempDatabasePath, true, defaultDBTimeout, false)
	if err != nil {
		return fmt.Errorf("failed to open LND database backend: %w", err)
	}
	defer func() {
		if err := kvdbBackend.Close(); err != nil {
			log.Printf("Warning: Failed to close database backend: %v", err)
		}
	}()

	// STEP 2.1: the graph over kvdbBackend, graph cache on —
	// Start() then loads the whole graph into memory
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

	// STEP 2.2: Start populates the cache; Stop is deferred
	if err := graph.Start(); err != nil {
		return fmt.Errorf("failed to start channel graph: %w", err)
	}
	defer func() {
		if err := graph.Stop(); err != nil {
			log.Printf("Warning: Failed to stop graph: %v", err)
		}
	}()

	log.Printf("Importing data to MySQL")


	// STEP 3: make sure the MySQL tables exist — every sync, so
	// a wiped database heals itself
	// =========================================================
	if err := db.InitializeDatabaseTables(mysqlDB); err != nil {
		return fmt.Errorf("failed to initialize database tables: %w", err)
	}


	// STEP 4: the three imports, in order. Every batch commits
	// on its own (see announcements.go), so a failure keeps
	// what was imported so far and skips the later importers
	// ========================================================
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








// -----------------------------------------------------------
// setupGracefulShutdown
// -----------------------------------------------------------
//
// A context cancelled on SIGINT/SIGTERM (docker stop). Only
// the ticker loop in main watches it: a signal during a
// running sync waits for that sync to finish — nothing
// inside processLNDDatabase checks ctx.
//
// Used by:
//   - main (below)
// -----------------------------------------------------------

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








// -----------------------------------------------------------
// connectToMySQL
// -----------------------------------------------------------
//
// sql.Open plus a Ping, because Open alone never touches the
// network. The DSN is user:password@tcp(host:port)/db with
// no parameters — no parseTime, no TLS.
//
// Used by:
//   - main (below)
// -----------------------------------------------------------

func connectToMySQL(config MySQLConfig) (*sql.DB, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s",
		config.User, config.Password, config.Host, config.Port, config.Database)

	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to MySQL: %w", err)
	}

	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to ping MySQL: %w", err)
	}

	return conn, nil
}








// -----------------------------------------------------------
// main
// -----------------------------------------------------------
//
// Config → MySQL → initial sync → ticker. Sync #1 runs
// immediately, later ones every SyncInterval, numbered by
// syncCount for the log. An error never ends the loop. The
// separator lines go to stdout via fmt, everything else to
// stderr via log — docker logs shows both.
//
// Used by:
//   - Dockerfile — CMD ["./lnd-dbreader"], the
//     lnd-dbreader-dbreader compose service
// -----------------------------------------------------------

func main() {
	log.Printf("Starting %s %s", appName, appVersion)


	// STEP 1: configuration, printed with the password masked
	// =======================================================
	config := loadConfig()

	log.Printf("Configuration:")
	log.Printf("  LND DB Path: %s", config.LNDDBPath)
	log.Printf("  MySQL: %s:***@tcp(%s:%s)/%s",
		config.MySQL.User, config.MySQL.Host, config.MySQL.Port, config.MySQL.Database)
	log.Printf("  Sync Interval: %v", config.SyncInterval)


	// STEP 2: MySQL — fatal if unreachable; the container's
	// restart policy is the retry
	// =====================================================
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


	// STEP 3: the shutdown context
	// ============================
	ctx, cancel := setupGracefulShutdown()
	defer cancel()


	// STEP 4: sync #1, right away
	// ===========================
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


	// STEP 5: the ticker loop until ctx is cancelled. A sync
	// longer than the interval does not pile up — Ticker drops
	// ticks nobody is receiving
	// ========================================================
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
