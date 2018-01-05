package database

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	bolt "github.com/coreos/bbolt"
	"github.com/op/go-logging"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

type callBack func([]byte, []byte)
type callBackWithError func([]byte, []byte) error

// DWriter ...
type DWriter struct {
	bucket   []byte
	key      []byte
	database *Database
}

// Database ...
type Database struct {
	db *bolt.DB
}

var (
	databaseFileName    = "library.db"
	backupFileName      = "library-backup.db"
	cacheFileName       = "cache.db"
	backupCacheFileName = "cache-backup.db"
)

var (
	// LibraryBucket ...
	LibraryBucket = []byte("Library")
	// BitTorrentBucket ...
	BitTorrentBucket = []byte("BitTorrent")
	// HistoryBucket ...
	HistoryBucket = []byte("History")
	// TorrentHistoryBucket ...
	TorrentHistoryBucket = []byte("TorrentHistory")
	// SearchCacheBucket ...
	SearchCacheBucket = []byte("SearchCache")

	// CommonBucket ...
	CommonBucket = []byte("Common")
)

// Buckets ...
var Buckets = [][]byte{
	LibraryBucket,
	BitTorrentBucket,
	HistoryBucket,
	TorrentHistoryBucket,
	SearchCacheBucket,
}

// CacheBuckets represents buckets in Cache database
var CacheBuckets = [][]byte{
	CommonBucket,
}

var (
	databaseLog   = logging.MustGetLogger("database")
	quit          chan struct{}
	database      *Database
	cacheDatabase *Database
	once          sync.Once
)

// InitDB ...
func InitDB(conf *config.Configuration) (*Database, error) {
	db, err := CreateDB(conf, databaseFileName, backupFileName)
	if err != nil || db == nil {
		return nil, errors.New("database not created")
	}

	database = &Database{db: db}
	quit = make(chan struct{}, 1)

	for _, bucket := range Buckets {
		if err = database.CheckBucket(bucket); err != nil {
			xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			databaseLog.Error(err)
			return database, err
		}
	}

	return database, nil
}

// InitCacheDB ...
func InitCacheDB(conf *config.Configuration) (*Database, error) {
	db, err := CreateDB(conf, cacheFileName, backupCacheFileName)
	if err != nil || db == nil {
		return nil, errors.New("database not created")
	}

	cacheDatabase = &Database{db: db}
	quit = make(chan struct{}, 1)

	for _, bucket := range CacheBuckets {
		if err = cacheDatabase.CheckBucket(bucket); err != nil {
			xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			databaseLog.Error(err)
			return cacheDatabase, err
		}
	}

	return cacheDatabase, nil
}

// CreateDB ...
func CreateDB(conf *config.Configuration, fileName string, backupFileName string) (*bolt.DB, error) {
	databasePath := filepath.Join(conf.Info.Profile, fileName)
	backupPath := filepath.Join(conf.Info.Profile, backupFileName)

	defer func() {
		if r := recover(); r != nil {
			RestoreBackup(databasePath, backupPath)
			os.Exit(1)
		}
	}()

	db, err := bolt.Open(databasePath, 0600, &bolt.Options{
		ReadOnly: false,
		Timeout:  15 * time.Second,
	})
	if err != nil {
		databaseLog.Warningf("Could not open database at %s: %s", databasePath, err.Error())
		return nil, err
	}

	return db, nil
}

// NewDB ...
func NewDB() (*Database, error) {
	if database == nil {
		return InitDB(config.Reload())
	}

	return database, nil
}

// Get returns common database
func Get() *Database {
	return database
}

// GetCache returns Cache database
func GetCache() *Database {
	return cacheDatabase
}

// Close ...
func (database *Database) Close() {
	databaseLog.Debug("Closing Database")
	quit <- struct{}{}
	database.db.Close()
}

// CheckBucket ...
func (database *Database) CheckBucket(bucket []byte) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	})
}

// MaintenanceRefreshHandler ...
func (database *Database) MaintenanceRefreshHandler() {
	backupPath := filepath.Join(config.Get().Info.Profile, backupFileName)
	cacheBackupPath := filepath.Join(config.Get().Info.Profile, backupCacheFileName)

	database.CreateBackup(backupPath)
	cacheDatabase.CreateBackup(cacheBackupPath)

	// database.CacheCleanup()

	tickerBackup := time.NewTicker(1 * time.Hour)
	defer tickerBackup.Stop()

	// tickerCache := time.NewTicker(1 * time.Hour)
	// defer tickerCache.Stop()
	defer close(quit)

	for {
		select {
		case <-tickerBackup.C:
			go func() {
				database.CreateBackup(backupPath)
				cacheDatabase.CreateBackup(cacheBackupPath)
			}()
		// case <-tickerCache.C:
		// 	go database.CacheCleanup()
		case <-quit:
			tickerBackup.Stop()
			// tickerCache.Stop()
			return
		}
	}
}

// RestoreBackup ...
func RestoreBackup(databasePath string, backupPath string) {
	databaseLog.Debug("Restoring backup")

	// Remove existing library.db if needed
	if _, err := os.Stat(databasePath); err == nil {
		if err := os.Remove(databasePath); err != nil {
			databaseLog.Warningf("Could not delete existing library file (%s): %s", databasePath, err)
			return
		}
	}

	// Restore backup if exists
	if _, err := os.Stat(backupPath); err == nil {
		errorMsg := fmt.Sprintf("Could not restore backup from '%s' to '%s': ", backupPath, databasePath)

		srcFile, err := os.Open(backupPath)
		if err != nil {
			databaseLog.Warning(errorMsg, err)
			return
		}
		defer srcFile.Close()

		destFile, err := os.Create(databasePath)
		if err != nil {
			databaseLog.Warning(errorMsg, err)
			return
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, srcFile); err != nil {
			databaseLog.Warning(errorMsg, err)
			return
		}

		if err := destFile.Sync(); err != nil {
			databaseLog.Warning(errorMsg, err)
			return
		}

		databaseLog.Warningf("Restored backup to %s", databasePath)
	}
}

// CreateBackup ...
func (database *Database) CreateBackup(backupPath string) {
	if err := database.CheckBucket(LibraryBucket); err == nil {
		database.db.View(func(tx *bolt.Tx) error {
			tx.CopyFile(backupPath, 0600)
			databaseLog.Debugf("Database backup saved at: %s", backupPath)
			return nil
		})
	}
}

// CacheCleanup ...
func (database *Database) CacheCleanup() {
	now := util.NowInt()
	for _, bucket := range Buckets {
		if !strings.Contains(string(bucket), "Cache") {
			continue
		}

		toRemove := []string{}
		database.ForEach(bucket, func(key []byte, value []byte) error {
			expire, _ := ParseCacheItem(value)
			if expire > 0 && expire < now {
				toRemove = append(toRemove, string(key))
			}

			return nil
		})

		if len(toRemove) > 0 {
			database.BatchDelete(bucket, toRemove)
		}
	}
}

//
//	Callback operations
//

// Seek ...
func (database *Database) Seek(bucket []byte, prefix string, callback callBack) error {
	return database.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucket).Cursor()
		bytePrefix := []byte(prefix)
		for k, v := c.Seek(bytePrefix); k != nil && bytes.HasPrefix(k, bytePrefix); k, v = c.Next() {
			callback(k, v)
		}
		return nil
	})
}

// ForEach ...
func (database *Database) ForEach(bucket []byte, callback callBackWithError) error {
	return database.db.View(func(tx *bolt.Tx) error {
		tx.Bucket(bucket).ForEach(callback)
		return nil
	})
}

//
// Cache operations
//

// ParseCacheItem ...
func ParseCacheItem(item []byte) (int, []byte) {
	expire, _ := strconv.Atoi(string(item[0:10]))
	return expire, item[11:]
}

// GetCachedBytes ...
func (database *Database) GetCachedBytes(bucket []byte, key string) (cacheValue []byte, err error) {
	var value []byte
	err = database.db.View(func(tx *bolt.Tx) error {
		value = tx.Bucket(bucket).Get([]byte(key))
		return nil
	})

	if err != nil || len(value) == 0 {
		return
	}

	expire, v := ParseCacheItem(value)
	if expire > 0 && expire < util.NowInt() {
		database.Delete(bucket, key)
		return nil, errors.New("Key Expired")
	}

	return v, nil
}

// GetCached ...
func (database *Database) GetCached(bucket []byte, key string) (string, error) {
	value, err := database.GetCachedBytes(bucket, key)
	return string(value), err
}

// GetCachedObject ...
func (database *Database) GetCachedObject(bucket []byte, key string, item interface{}) (err error) {
	v, err := database.GetCachedBytes(bucket, key)
	if err != nil {
		return err
	}

	if err = json.Unmarshal(v, &item); err != nil {
		databaseLog.Warningf("Could not unmarshal object for key: '%s', in bucket '%s': %s; Value: %#v", key, bucket, err, string(v))
		return err
	}

	return
}

//
// Get/Set operations
//

// GetBytes ...
func (database *Database) GetBytes(bucket []byte, key string) (value []byte, err error) {
	err = database.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		value = b.Get([]byte(key))
		return nil
	})

	return
}

// Get ...
func (database *Database) Get(bucket []byte, key string) (string, error) {
	value, err := database.GetBytes(bucket, key)
	return string(value), err
}

// GetObject ...
func (database *Database) GetObject(bucket []byte, key string, item interface{}) (err error) {
	v, err := database.GetBytes(bucket, key)
	if err != nil {
		return err
	}

	if len(v) == 0 {
		return
	}

	if err = json.Unmarshal(v, &item); err != nil {
		databaseLog.Warningf("Could not unmarshal object for key: '%s', in bucket '%s': %s", key, bucket, err)
		return err
	}

	return
}

// SetCachedBytes ...
func (database *Database) SetCachedBytes(bucket []byte, seconds int, key string, value []byte) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		value = append([]byte(strconv.Itoa(util.NowPlusSecondsInt(seconds))+"|"), value...)
		return tx.Bucket(bucket).Put([]byte(key), value)
	})
}

// SetCached ...
func (database *Database) SetCached(bucket []byte, seconds int, key string, value string) error {
	return database.SetCachedBytes(bucket, seconds, key, []byte(value))
}

// SetCachedObject ...
func (database *Database) SetCachedObject(bucket []byte, seconds int, key string, item interface{}) error {
	if buf, err := json.Marshal(item); err != nil {
		return err
	} else if err := database.SetCachedBytes(bucket, seconds, key, buf); err != nil {
		return err
	}

	return nil
}

// SetBytes ...
func (database *Database) SetBytes(bucket []byte, key string, value []byte) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(key), value)
	})
}

// Set ...
func (database *Database) Set(bucket []byte, key string, value string) error {
	return database.SetBytes(bucket, key, []byte(value))
}

// SetObject ...
func (database *Database) SetObject(bucket []byte, key string, item interface{}) error {
	if buf, err := json.Marshal(item); err != nil {
		return err
	} else if err := database.SetBytes(bucket, key, buf); err != nil {
		return err
	}

	return nil
}

// BatchSet ...
func (database *Database) BatchSet(bucket []byte, objects map[string]string) error {
	return database.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		for key, value := range objects {
			if err := b.Put([]byte(key), []byte(value)); err != nil {
				return err
			}
		}
		return nil
	})
}

// BatchSetBytes ...
func (database *Database) BatchSetBytes(bucket []byte, objects map[string][]byte) error {
	return database.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		for key, value := range objects {
			if err := b.Put([]byte(key), value); err != nil {
				return err
			}
		}
		return nil
	})
}

// BatchSetObject ...
func (database *Database) BatchSetObject(bucket []byte, objects map[string]interface{}) error {
	serialized := map[string][]byte{}
	for k, item := range objects {
		buf, err := json.Marshal(item)
		if err != nil {
			return err
		}
		serialized[k] = buf
	}

	return database.BatchSetBytes(bucket, serialized)
}

// Delete ...
func (database *Database) Delete(bucket []byte, key string) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Delete([]byte(key))
	})
}

// BatchDelete ...
func (database *Database) BatchDelete(bucket []byte, keys []string) error {
	return database.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		for _, key := range keys {
			if err := b.Delete([]byte(key)); err != nil {
				return err
			}
		}
		return nil
	})
}

// AsWriter ...
func (database *Database) AsWriter(bucket []byte, key string) *DWriter {
	return &DWriter{
		database: database,
		bucket:   bucket,
		key:      []byte(key),
	}
}

// Write ...
func (w *DWriter) Write(b []byte) (n int, err error) {
	return len(b), w.database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(w.bucket).Put(w.key, b)
	})
}
