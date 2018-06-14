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
	"time"

	"github.com/boltdb/bolt"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
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

// InitBoltDB ...
func InitBoltDB(conf *config.Configuration) (*BoltDatabase, error) {
	db, err := CreateBoltDB(conf, boltFileName, backupBoltFileName)
	if err != nil || db == nil {
		return nil, errors.New("database not created")
	}

	boltDatabase = &BoltDatabase{
		db:             db,
		quit:           make(chan struct{}, 2),
		fileName:       boltFileName,
		backupFileName: backupBoltFileName,
	}

	for _, bucket := range Buckets {
		if err = boltDatabase.CheckBucket(bucket); err != nil {
			xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			log.Error(err)
			return boltDatabase, err
		}
	}

	return boltDatabase, nil
}

// InitCacheDB ...
func InitCacheDB(conf *config.Configuration) (*BoltDatabase, error) {
	db, err := CreateBoltDB(conf, cacheFileName, backupCacheFileName)
	if err != nil || db == nil {
		return nil, errors.New("database not created")
	}

	cacheDatabase = &BoltDatabase{
		db:             db,
		quit:           make(chan struct{}, 2),
		fileName:       cacheFileName,
		backupFileName: backupCacheFileName,
	}

	for _, bucket := range CacheBuckets {
		if err = cacheDatabase.CheckBucket(bucket); err != nil {
			xbmc.Notify("Elementum", err.Error(), config.AddonIcon())
			log.Error(err)
			return cacheDatabase, err
		}
	}

	return cacheDatabase, nil
}

// CreateBoltDB ...
func CreateBoltDB(conf *config.Configuration, fileName string, backupFileName string) (*bolt.DB, error) {
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
	db.NoSync = true

	if err != nil {
		log.Warningf("Could not open database at %s: %s", databasePath, err.Error())
		return nil, err
	}

	return db, nil
}

// NewBoltDB ...
func NewBoltDB() (*BoltDatabase, error) {
	if boltDatabase == nil {
		return InitBoltDB(config.Reload())
	}

	return boltDatabase, nil
}

// GetBolt returns common database
func GetBolt() *BoltDatabase {
	return boltDatabase
}

// GetCache returns Cache database
func GetCache() *BoltDatabase {
	return cacheDatabase
}

// Close ...
func (database *BoltDatabase) Close() {
	log.Debug("Closing Database")
	database.quit <- struct{}{}
	database.db.Close()
}

// CheckBucket ...
func (database *BoltDatabase) CheckBucket(bucket []byte) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	})
}

// MaintenanceRefreshHandler ...
func (database *BoltDatabase) MaintenanceRefreshHandler() {
	backupPath := filepath.Join(config.Get().Info.Profile, database.backupFileName)

	database.CreateBackup(backupPath)

	// database.CacheCleanup()

	tickerBackup := time.NewTicker(2 * time.Hour)

	defer tickerBackup.Stop()
	defer close(database.quit)

	for {
		select {
		case <-tickerBackup.C:
			go func() {
				database.CreateBackup(backupPath)
			}()
			// case <-tickerCache.C:
			// 	go database.CacheCleanup()
		case <-database.quit:
			return
		}
	}
}

// RestoreBackup ...
func RestoreBackup(databasePath string, backupPath string) {
	log.Debug("Restoring backup")

	// Remove existing library.db if needed
	if _, err := os.Stat(databasePath); err == nil {
		if err := os.Remove(databasePath); err != nil {
			log.Warningf("Could not delete existing library file (%s): %s", databasePath, err)
			return
		}
	}

	// Restore backup if exists
	if _, err := os.Stat(backupPath); err == nil {
		errorMsg := fmt.Sprintf("Could not restore backup from '%s' to '%s': ", backupPath, databasePath)

		srcFile, err := os.Open(backupPath)
		if err != nil {
			log.Warning(errorMsg, err)
			return
		}
		defer srcFile.Close()

		destFile, err := os.Create(databasePath)
		if err != nil {
			log.Warning(errorMsg, err)
			return
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, srcFile); err != nil {
			log.Warning(errorMsg, err)
			return
		}

		if err := destFile.Sync(); err != nil {
			log.Warning(errorMsg, err)
			return
		}

		log.Warningf("Restored backup to %s", databasePath)
	}
}

// CreateBackup ...
func (database *BoltDatabase) CreateBackup(backupPath string) {
	if err := database.CheckBucket(LibraryBucket); err != nil {
		return
	}

	database.db.View(func(tx *bolt.Tx) error {
		tx.CopyFile(backupPath, 0600)
		log.Debugf("Database backup saved at: %s", backupPath)
		return nil
	})
}

// CacheCleanup ...
func (database *BoltDatabase) CacheCleanup() {
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

// DeleteWithPrefix ...
func (database *BoltDatabase) DeleteWithPrefix(bucket []byte, prefix []byte) {
	toRemove := []string{}
	database.ForEach(bucket, func(key []byte, v []byte) error {
		if bytes.HasPrefix(key, prefix) {
			toRemove = append(toRemove, string(key))
		}

		return nil
	})

	if len(toRemove) > 0 {
		database.BatchDelete(bucket, toRemove)
	}
}

//
//	Callback operations
//

// Seek ...
func (database *BoltDatabase) Seek(bucket []byte, prefix string, callback callBack) error {
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
func (database *BoltDatabase) ForEach(bucket []byte, callback callBackWithError) error {
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
func (database *BoltDatabase) GetCachedBytes(bucket []byte, key string) (cacheValue []byte, err error) {
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
func (database *BoltDatabase) GetCached(bucket []byte, key string) (string, error) {
	value, err := database.GetCachedBytes(bucket, key)
	return string(value), err
}

// GetCachedObject ...
func (database *BoltDatabase) GetCachedObject(bucket []byte, key string, item interface{}) (err error) {
	v, err := database.GetCachedBytes(bucket, key)
	if err != nil || len(v) == 0 {
		return err
	}

	if err = json.Unmarshal(v, &item); err != nil {
		log.Warningf("Could not unmarshal object for key: '%s', in bucket '%s': %s; Value: %#v", key, bucket, err, string(v))
		return err
	}

	return
}

//
// Get/Set operations
//

// Has checks for existence of a key
func (database *BoltDatabase) Has(bucket []byte, key string) (ret bool) {
	database.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		ret = len(b.Get([]byte(key))) > 0
		return nil
	})

	return
}

// GetBytes ...
func (database *BoltDatabase) GetBytes(bucket []byte, key string) (value []byte, err error) {
	err = database.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		value = b.Get([]byte(key))
		return nil
	})

	return
}

// Get ...
func (database *BoltDatabase) Get(bucket []byte, key string) (string, error) {
	value, err := database.GetBytes(bucket, key)
	return string(value), err
}

// GetObject ...
func (database *BoltDatabase) GetObject(bucket []byte, key string, item interface{}) (err error) {
	v, err := database.GetBytes(bucket, key)
	if err != nil {
		return err
	}

	if len(v) == 0 {
		return errors.New("Bytes empty")
	}

	if err = json.Unmarshal(v, &item); err != nil {
		log.Warningf("Could not unmarshal object for key: '%s', in bucket '%s': %s", key, bucket, err)
		return err
	}

	return
}

// SetCachedBytes ...
func (database *BoltDatabase) SetCachedBytes(bucket []byte, seconds int, key string, value []byte) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		value = append([]byte(strconv.Itoa(util.NowPlusSecondsInt(seconds))+"|"), value...)
		return tx.Bucket(bucket).Put([]byte(key), value)
	})
}

// SetCached ...
func (database *BoltDatabase) SetCached(bucket []byte, seconds int, key string, value string) error {
	return database.SetCachedBytes(bucket, seconds, key, []byte(value))
}

// SetCachedObject ...
func (database *BoltDatabase) SetCachedObject(bucket []byte, seconds int, key string, item interface{}) error {
	if buf, err := json.Marshal(item); err != nil {
		return err
	} else if err := database.SetCachedBytes(bucket, seconds, key, buf); err != nil {
		return err
	}

	return nil
}

// SetBytes ...
func (database *BoltDatabase) SetBytes(bucket []byte, key string, value []byte) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(key), value)
	})
}

// Set ...
func (database *BoltDatabase) Set(bucket []byte, key string, value string) error {
	return database.SetBytes(bucket, key, []byte(value))
}

// SetObject ...
func (database *BoltDatabase) SetObject(bucket []byte, key string, item interface{}) error {
	if buf, err := json.Marshal(item); err != nil {
		return err
	} else if err := database.SetBytes(bucket, key, buf); err != nil {
		return err
	}

	return nil
}

// BatchSet ...
func (database *BoltDatabase) BatchSet(bucket []byte, objects map[string]string) error {
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
func (database *BoltDatabase) BatchSetBytes(bucket []byte, objects map[string][]byte) error {
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
func (database *BoltDatabase) BatchSetObject(bucket []byte, objects map[string]interface{}) error {
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
func (database *BoltDatabase) Delete(bucket []byte, key string) error {
	return database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Delete([]byte(key))
	})
}

// BatchDelete ...
func (database *BoltDatabase) BatchDelete(bucket []byte, keys []string) error {
	return database.db.Batch(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		for _, key := range keys {
			b.Delete([]byte(key))
		}
		return nil
	})
}

// AsWriter ...
func (database *BoltDatabase) AsWriter(bucket []byte, key string) *DBWriter {
	return &DBWriter{
		database: database,
		bucket:   bucket,
		key:      []byte(key),
	}
}

// Write ...
func (w *DBWriter) Write(b []byte) (n int, err error) {
	return len(b), w.database.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(w.bucket).Put(w.key, b)
	})
}
