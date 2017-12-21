package main

import (
	"os"
	"path/filepath"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/repository"
	"github.com/elgatito/elementum/xbmc"
)

// Migrate ...
func Migrate() bool {
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
