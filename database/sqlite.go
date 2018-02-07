package database

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"

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
		fileName:       boltFileName,
		backupFileName: backupBoltFileName,
	}

	// Iterate through changesets and apply to the database
	currentVersion := sqliteDatabase.getSchemaVersion()
	newVersion := 0
	for _, s := range schemaChanges {
		if currentVersion < s.version {
			if _, err := sqliteDatabase.DB.Exec(s.sql); err != nil {
				log.Debugf("Error executing query: %s; sql: %s", err, s.sql)
				break
			}
			newVersion = s.version
		}
	}
	if newVersion > 0 {
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

// CreateBackup ...
func (d *SqliteDatabase) CreateBackup(backupPath string) {
	util.CopyFile(d.fileName, d.backupFileName, true)
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
