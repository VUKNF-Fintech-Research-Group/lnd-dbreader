package main

import (
    "database/sql"
    "log"
    "os"
    "path/filepath"
    "time"
	"fmt"

    "lnd-dbreader/db"
    "lnd-dbreader/utils"
    "lnd-dbreader/models"
    "io/ioutil"
    _ "github.com/go-sql-driver/mysql"
)



func getEnv(key, defaultValue string) string {
    value := os.Getenv(key)
    if value == "" {
        return defaultValue
    }
    return value
}



func main() {
    // MySQL ENV variables
    mysqlHost :=     getEnv("MYSQL_HOST",     "lnd-dbreader-mysql")
    mysqlPort :=     getEnv("MYSQL_PORT",     "3306")
    mysqlDBName :=   getEnv("MYSQL_DBNAME",   "lnd_data")
    mysqlUser :=     getEnv("MYSQL_USER",     "lnd_data")
    mysqlPassword := getEnv("MYSQL_PASSWORD", "lnd_data")
    dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", mysqlUser, mysqlPassword, mysqlHost, mysqlPort, mysqlDBName)

    for {
        processData(dsn)

        // Wait for 30 minutes before the next iteration
        time.Sleep(30 * time.Minute)
    }
}



func processData(dsn string) {
    dbDir := "/data"
    dbFile := "channel.db"
    dbPath := filepath.Join(dbDir, dbFile)

    // Create a temporary directory
    tempDir, err := ioutil.TempDir("", "lnd_db_copy")
    if err != nil {
        log.Printf("Failed to create temporary directory: %v", err)
        return
    }
    // Ensure the temporary directory is removed
    defer func() {
        if err := os.RemoveAll(tempDir); err != nil {
            log.Printf("Failed to remove temporary directory %s: %v", tempDir, err)
        } else {
            log.Printf("Successfully removed temporary directory: %s", tempDir)
        }
    }()

    tempDBPath := filepath.Join(tempDir, dbFile)

    err = db.CopyDatabase(dbPath, tempDBPath)
    if err != nil {
        log.Printf("Failed to copy database: %v", err)
        return
    }

    // Open the copy of the database
    dbInstance, err := models.Open(tempDir)
    if err != nil {
        log.Printf("Failed to open database: %v", err)
        return
    }
    defer func() {
        if err := dbInstance.Close(); err != nil {
            log.Printf("Failed to close database instance: %v", err)
        } else {
            log.Printf("Successfully closed database instance")
        }
    }()

    graph := dbInstance.ChannelGraph()

    // Connect to MySQL
    mysqlDB, err := sql.Open("mysql", dsn)
    if err != nil {
        log.Printf("Failed to connect to MySQL: %v", err)
        return
    }
    // Ensure the MySQL connection is closed
    defer func() {
        if err := mysqlDB.Close(); err != nil {
            log.Printf("Failed to close MySQL connection: %v", err)
        } else {
            log.Printf("Successfully closed MySQL connection")
        }
    }()

    fmt.Printf("[*] %s: Importing data to MySQL\n", time.Now().Format("2006-01-02 15:04:05"))

    // Initialize database tables
    if err := db.InitializeDatabaseTables(mysqlDB); err != nil {
        log.Printf("Failed to initialize database tables: %v", err)
        return
    }

    // Print channel announcements
    if getEnv("PRINT_CHANNELS_ANNOUNCEMENTS", "0") == "1" || getEnv("PRINT_ALL_ANNOUNCEMENTS", "0") == "1" {
        if err := utils.PrintChannelAnnouncements(graph); err != nil {
            log.Printf("Failed to print channel announcements: %v", err)
            // Decide whether to continue or handle the error
        }
    }

    // Print node announcements
    if getEnv("PRINT_NODE_ANNOUNCEMENTS", "0") == "1" || getEnv("PRINT_ALL_ANNOUNCEMENTS", "0") == "1" {
        if err := utils.PrintNodeAnnouncements(graph); err != nil {
            log.Printf("Failed to print node announcements: %v", err)
            // Decide whether to continue or handle the error
        }
    }

    // Send channel announcements
    if err := db.SendChannelAnnouncements(graph, mysqlDB); err != nil {
        log.Printf("Failed to send channel announcements to MySQL: %v", err)
    }

    // Send node announcements
    if err := db.SendNodeAnnouncements(graph, mysqlDB); err != nil {
        log.Printf("Failed to send node announcements to MySQL: %v", err)
    }

    // Send node addresses
    if err := db.SendNodeAddresses(graph, mysqlDB); err != nil {
        log.Printf("Failed to send node addresses to MySQL: %v", err)
    }

    fmt.Printf("\n")
}