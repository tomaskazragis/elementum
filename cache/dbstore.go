package cache

import (
	"bytes"
	"errors"
	"time"

	"github.com/klauspost/compress/gzip"
	"github.com/vmihailenco/msgpack"

	"github.com/elgatito/elementum/database"
)

//go:generate msgp -o msgp.go -io=false -tests=false

// DBStore ...
type DBStore struct {
	db *database.Database
}

type dbStoreItem struct {
	Key     string      `json:"key"`
	Value   interface{} `json:"value"`
	Expires time.Time   `json:"expires"`
}

// NewDBStore Returns instance of BoltDB backed cache store
func NewDBStore() *DBStore {
	return &DBStore{database.GetCache()}
}

// Set ...
func (c *DBStore) Set(key string, value interface{}, expires time.Duration) error {
	item := dbStoreItem{
		Key:     key,
		Value:   value,
		Expires: time.Now().UTC().Add(expires),
	}

	buf := bytes.NewBuffer(nil)
	gzWriter, _ := gzip.NewWriterLevel(buf, 1)

	if err := msgpack.NewEncoder(gzWriter).Encode(item); err != nil {
		return err
	}
	if err := gzWriter.Close(); err != nil {
		return err
	}

	go c.db.SetBytes(database.CommonBucket, key, buf.Bytes())

	return nil
}

// Add ...
func (c *DBStore) Add(key string, value interface{}, expires time.Duration) error {
	return c.Set(key, value, expires)
}

// Replace ...
func (c *DBStore) Replace(key string, value interface{}, expires time.Duration) error {
	return c.Set(key, value, expires)
}

// Get ...
func (c *DBStore) Get(key string, value interface{}) error {
	data, errGet := c.db.GetBytes(database.CommonBucket, key)
	if errGet != nil {
		return errGet
	} else if len(data) == 0 {
		return errors.New("data is empty")
	}

	gzReader, err := gzip.NewReader(bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	defer gzReader.Close()

	item := dbStoreItem{
		Value: value,
	}

	if errDecode := msgpack.NewDecoder(gzReader).Decode(&item); errDecode != nil {
		return errDecode
	}

	if item.Expires.Before(time.Now().UTC()) {
		go c.db.Delete(database.CommonBucket, key)
		return errors.New("key is expired")
	}
	return nil
}

// Delete ...
func (c *DBStore) Delete(key string) error {
	return nil
}

// Increment ...
func (c *DBStore) Increment(key string, delta uint64) (uint64, error) {
	return 0, errNotSupported
}

// Decrement ...
func (c *DBStore) Decrement(key string, delta uint64) (uint64, error) {
	return 0, errNotSupported
}

// Flush ...
func (c *DBStore) Flush() error {
	return errNotSupported
}
