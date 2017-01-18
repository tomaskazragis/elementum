package main

import (
	"os"
	"strings"
	"path/filepath"

	"github.com/scakemyer/quasar/xbmc"
	"github.com/scakemyer/quasar/config"
	"github.com/scakemyer/quasar/repository"
)

type Addon struct {
	ID      string
	Name    string
	Version string
	Enabled bool
}

func Migrate() {
	firstRun := filepath.Join(config.Get().Info.Path, ".firstrun")
	if _, err := os.Stat(firstRun); err == nil {
		return
	}
	file, _ := os.Create(firstRun)
	defer file.Close()

	log.Info("Preparing for first run...")

	log.Info("Creating Quasar repository add-on")
	if err := repository.MakeQuasarRepositoryAddon(); err != nil {
		log.Errorf("Unable to create repository add-on: %s", err)
	}

	go func() {
		// Check for enabled providers and Quasar Burst
		hasBurst := false
		enabledProviders := make([]Addon, 0)
		for _, addon := range xbmc.GetAddons("xbmc.python.script", "executable", "all", []string{"name", "version", "enabled"}).Addons {
			if strings.HasPrefix(addon.ID, "script.quasar.") {
				if addon.ID == "script.quasar.burst" && addon.Enabled == true {
					hasBurst = true
				}
				enabledProviders = append(enabledProviders, Addon{
					ID: addon.ID,
					Name: addon.Name,
					Version: addon.Version,
					Enabled: addon.Enabled,
				})
			}
		}
		if !hasBurst {
			if xbmc.DialogConfirm("Quasar", "LOCALIZE[30271]") {
				xbmc.PlayURL("plugin://script.quasar.burst/")
				for _, addon := range xbmc.GetAddons("xbmc.python.script", "executable", "all", []string{"name", "version", "enabled"}).Addons {
					if addon.ID == "script.quasar.burst" && addon.Enabled == true {
						hasBurst = true
					}
				}
				if hasBurst {
					for _, addon := range enabledProviders {
						xbmc.SetAddonEnabled(addon.ID, false)
					}
					xbmc.Notify("Quasar", "LOCALIZE[30272]", config.AddonIcon())
				} else {
					xbmc.Dialog("Quasar", "LOCALIZE[30273]")
				}
			}
		}
	}()
}
