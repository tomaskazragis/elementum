package database

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	// Importing sqlite driver
	_ "github.com/mattn/go-sqlite3"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/util"
)

// InitSqliteDB ...
func InitSqliteDB(conf *config.Configuration) (*SqliteDatabase, error) {
	db, err := CreateSqliteDB(conf, sqliteFileName, backupSqliteFileName)
	if err != nil || db == nil {
		return nil, errors.New("database not created")
	}

	sqliteDatabase = &SqliteDatabase{
		DB:             db,
		quit:           make(chan struct{}, 2),
		fileName:       sqliteFileName,
		backupFileName: backupSqliteFileName,
	}

	// Iterate through changesets and apply to the database
	currentVersion := sqliteDatabase.getSchemaVersion()
	newVersion := currentVersion
	for _, s := range schemaChanges {
		if ok, err := s(&newVersion, sqliteDatabase); err != nil {
			log.Debugf("Error migrating schema: %s", err)
			break
		} else if ok {
			continue
		}

		break
	}
	if newVersion > currentVersion {
		log.Debugf("Updated database to version: %d", newVersion)
		sqliteDatabase.setSchemaVersion(newVersion)
	}

	return sqliteDatabase, nil
}

// CreateSqliteDB ...
func CreateSqliteDB(conf *config.Configuration, fileName string, backupFileName string) (*sql.DB, error) {
	databasePath := filepath.Join(conf.Info.Profile, fileName)
	backupPath := filepath.Join(conf.Info.Profile, backupFileName)

	defer func() {
		if r := recover(); r != nil {
			RestoreBackup(databasePath, backupPath)
			os.Exit(1)
		}
	}()

	db, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		log.Warningf("Could not open database at %s: %s", databasePath, err.Error())
		return nil, err
	}

	// Setting up default properties for connection
	db.Exec("PRAGMA journal_mode=WAL")
	db.SetMaxOpenConns(1)

	return db, nil
}

// Get returns sqlite database
func Get() *SqliteDatabase {
	return sqliteDatabase
}

// MaintenanceRefreshHandler ...
func (d *SqliteDatabase) MaintenanceRefreshHandler() {
	backupPath := filepath.Join(config.Get().Info.Profile, d.backupFileName)

	d.CreateBackup(backupPath)

	// database.CacheCleanup()

	tickerBackup := time.NewTicker(1 * time.Hour)
	defer tickerBackup.Stop()

	defer close(d.quit)

	for {
		select {
		case <-tickerBackup.C:
			go func() {
				d.CreateBackup(backupPath)
			}()
			// case <-tickerCache.C:
			// 	go database.CacheCleanup()
		case <-d.quit:
			return
		}
	}
}

// CreateBackup ...
func (d *SqliteDatabase) CreateBackup(backupPath string) {
	src := filepath.Join(config.Get().Info.Profile, d.fileName)
	dest := filepath.Join(config.Get().Info.Profile, d.backupFileName)
	if err := util.CopyFile(src, dest, true); err != nil {
		log.Warningf("Could not backup from '%s' to '%s' the database: %s", src, dest, err)
	}
}

// Close ...
func (d *SqliteDatabase) Close() {
	log.Debug("Closing Database")
	d.quit <- struct{}{}
	d.DB.Close()
}

func (d *SqliteDatabase) getSchemaVersion() (version int) {
	d.DB.QueryRow(`SELECT version FROM meta`).Scan(&version)
	return
}

func (d *SqliteDatabase) setSchemaVersion(version int) {
	if _, err := d.DB.Exec(`UPDATE meta SET version = ?`, version); err != nil {
		log.Debugf("Could not update schema version: %s", err)
	}
}

// GetCount is a helper for returning single column int result
func (d *SqliteDatabase) GetCount(sql string) (count int) {
	_ = d.DB.QueryRow(sql).Scan(&count)
	return
}

// AddTorrentHistory saves link between torrent file and tmdbID entry
func (d *SqliteDatabase) AddTorrentHistory(tmdbID, infoHash string, b []byte) {
	log.Debugf("Saving torrent entry for TMDB %s with infohash %s", tmdbID, infoHash)

	var infohashID int64
	d.QueryRow(`SELECT rowid FROM torrent_items WHERE infohash = ?`, infoHash).Scan(&infohashID)
	if infohashID == 0 {
		res, err := d.Exec(`INSERT INTO torrent_items (infohash, metainfo) VALUES(?, ?)`, infoHash, b)
		if err != nil {
			log.Debugf("Could not insert torrent: %s", err)
			return
		}
		infohashID, err = res.LastInsertId()
		if err != nil {
			log.Debugf("Could not get inserted rowid: %s", err)
			return
		}
	}

	if infohashID == 0 {
		return
	}

	var oldInfohashID int64
	d.QueryRow(`SELECT infohash_id FROM torrent_links WHERE item_id = ?`, tmdbID).Scan(&oldInfohashID)
	d.Exec(`INSERT OR REPLACE INTO torrent_links (infohash_id, item_id) VALUES (?, ?)`, infohashID, tmdbID)

	// Clean up previously saved torrent metainfo if there is any
	// and it's not used by any other tmdbID entry
	if oldInfohashID != 0 {
		var left int
		d.QueryRow(`SELECT COUNT(*) FROM torrent_links WHERE infohash_id = ?`, oldInfohashID).Scan(&left)
		if left == 0 {
			d.Exec(`DELETE FROM torrent_items WHERE rowid = ?`, oldInfohashID)
		}
	}
}
