package database

import (
	bolt "github.com/coreos/bbolt"
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
	db             *bolt.DB
	quit           chan struct{}
	fileName       string
	backupFileName string
}

// BTItem ...
type BTItem struct {
	ID      int      `json:"id"`
	State   int      `json:"state"`
	Type    string   `json:"type"`
	File    int      `json:"file"`
	Files   []string `json:"files"`
	ShowID  int      `json:"showid"`
	Season  int      `json:"season"`
	Episode int      `json:"episode"`
}
