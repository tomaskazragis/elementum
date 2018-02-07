package database

var schemaChanges = []schemaChange{
	schemaV1,
}

func schemaV1(previousVersion *int, db *SqliteDatabase) (success bool, err error) {
	version := 1

	if *previousVersion > version {
		return
	}

	sql := `

-- Table that stores database specific info, like last rolled version
CREATE TABLE IF NOT EXISTS meta (
  version INT NOT NULL DEFAULT 0
);
INSERT INTO meta (version) SELECT 0 WHERE NOT EXISTS(SELECT 1 FROM meta);

-- Table for Search queries history
CREATE TABLE IF NOT EXISTS history_queries (
  type INT NOT NULL DEFAULT "",
  query TEXT NOT NULL DEFAULT "",
  dt INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS history_queries_idx ON history_queries (type,  dt DESC);

-- Table that stores torrents' metadata
CREATE TABLE IF NOT EXISTS torrent_items (
  infohash TEXT NOT NULL UNIQUE,
  metainfo BLOB
);
CREATE INDEX IF NOT EXISTS torrent_items_idx ON torrent_items (infohash);

-- Table stores links between items and stored metadata
CREATE TABLE IF NOT EXISTS torrent_links (
  infohash_id INT NOT NULL,
  item_id INT NOT NULL UNIQUE
);
CREATE INDEX IF NOT EXISTS torrent_links_idx ON torrent_links (item_id, infohash_id);

`

	// Just run an a bunch of statements
	// If everything is fine - return success so we won't get in there again
	if _, err = db.Exec(sql); err == nil {
		*previousVersion = version
		success = true
	}

	return
}
