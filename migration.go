package main

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/elgatito/elementum/bittorrent"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/repository"
	"github.com/elgatito/elementum/xbmc"
)

// Migrate ...
func Migrate() bool {
	migrateDB()

	firstRun := filepath.Join(config.Get().Info.Path, ".firstrun")
	if _, err := os.Stat(firstRun); err == nil {
		return false
	}
	file, _ := os.Create(firstRun)
	defer file.Close()

	log.Info("Preparing for first run...")

	log.Info("Creating Elementum repository add-on...")
	if err := repository.MakeElementumRepositoryAddon(); err != nil {
		log.Errorf("Unable to create repository add-on: %s", err)
	} else {
		xbmc.UpdateLocalAddons()
		for _, addon := range xbmc.GetAddons("xbmc.addon.repository", "unknown", "all", []string{"name", "version", "enabled"}).Addons {
			if addon.ID == "repository.elementum" && addon.Enabled == true {
				log.Info("Found enabled Elementum repository add-on")
				return false
			}
		}
		log.Info("Elementum repository not installed, installing...")
		xbmc.InstallAddon("repository.elementum")
		xbmc.SetAddonEnabled("repository.elementum", true)
		xbmc.UpdateLocalAddons()
		xbmc.UpdateAddonRepos()
	}

	return true
}

func migrateDB() bool {
	firstRun := filepath.Join(config.Get().Info.Path, ".dbfirstrun")
	if _, err := os.Stat(firstRun); err == nil {
		return false
	}
	file, _ := os.Create(firstRun)
	defer file.Close()

	log.Info("Migrating old bolt DB to Sqlite ...")

	newDB := database.Get()
	oldDB, err := database.NewBoltDB()
	if err != nil {
		return false
	}

	for _, t := range []string{"", "movies", "shows"} {
		list := []string{}
		if err := oldDB.GetObject(database.HistoryBucket, "list"+t, &list); err != nil {
			continue
		}
		for i := len(list) - 1; i >= 0; i-- {
			newDB.AddSearchHistory(t, list[i])
		}
	}

	oldDB.Seek(database.TorrentHistoryBucket, "", func(k, v []byte) {
		_, err := strconv.Atoi(string(k))
		if err != nil || len(v) <= 0 {
			return
		}

		torrent := &bittorrent.TorrentFile{}
		err = torrent.LoadFromBytes(v)
		if err != nil {
			return
		}

		if torrent.InfoHash != "" {
			newDB.AddTorrentHistory(string(k), torrent.InfoHash, v)
		}
	})
	return true
}
