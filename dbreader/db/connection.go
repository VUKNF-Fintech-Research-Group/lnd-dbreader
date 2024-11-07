package db

import (
    "fmt"
    "io"
    "os"
    "path/filepath"
    "time"
	"log"

    "lnd-dbreader/models"
    "go.etcd.io/bbolt"
)

const (
    maxRetries = 5
    retryDelay = 500 * time.Millisecond
)

func OpenDBWithRetry(dbPath string) (*models.DB, error) {
    var db *models.DB
    var err error

    for i := 0; i < maxRetries; i++ {
        db, err = models.Open(dbPath)
        if err == nil {
            break
        }
        time.Sleep(retryDelay)
    }

    if err != nil {
        return nil, fmt.Errorf("failed to open channeldb after %d attempts: %v", maxRetries, err)
    }

    return db, nil
}

func CopyDatabase(src, dst string) error {
    sourceFile, err := os.Open(src)
    if err != nil {
        return fmt.Errorf("failed to open source database: %v", err)
    }
    defer sourceFile.Close()

    destFile, err := os.Create(dst)
    if err != nil {
        return fmt.Errorf("failed to create destination database: %v", err)
    }
    defer destFile.Close()

    _, err = io.Copy(destFile, sourceFile)
    if err != nil {
        return fmt.Errorf("failed to copy database: %v", err)
    }

    return nil
}

func OpenDatabase(dbDir string) (*models.DB, error) {
    db, err := models.Open(dbDir)
    if err != nil {
        // Attempt to repair the database
        log.Printf("Failed to open database, attempting repair: %v", err)
        err = RepairDatabase(filepath.Join(dbDir, "channel.db"))
        if err != nil {
            return nil, fmt.Errorf("failed to repair database: %v", err)
        }

        // Try opening again after repair
        db, err = models.Open(dbDir)
        if err != nil {
            return nil, fmt.Errorf("failed to open database after repair: %v", err)
        }
    }

    return db, nil
}

func RepairDatabase(dbPath string) error {
    // Open the database in read-only mode to check its integrity
    db, err := bbolt.Open(dbPath, 0600, &bbolt.Options{ReadOnly: true, Timeout: 1 * time.Second})
    if err != nil {
        return fmt.Errorf("failed to open database for repair: %v", err)
    }
    defer db.Close()

    // Check database integrity
    err = db.View(func(tx *bbolt.Tx) error {
        return tx.ForEach(func(name []byte, b *bbolt.Bucket) error {
            return b.ForEach(func(k, v []byte) error {
                return nil // Just iterate through all keys
            })
        })
    })

    if err != nil {
        return fmt.Errorf("database integrity check failed: %v", err)
    }

    return nil
}
