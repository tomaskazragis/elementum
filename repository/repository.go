package repository

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

func copyFile(from string, to string) error {
	input, err := os.Open(from)
	if err != nil {
		return err
	}
	defer input.Close()

	output, err := os.Create(to)
	if err != nil {
		return err
	}
	defer output.Close()
	io.Copy(output, input)
	return nil
}

func MakeElementumRepositoryAddon() error {
	addonId := "repository.elementum"
	addonName := "Elementum Repository"

	elementumHost := fmt.Sprintf("http://localhost:%d", config.ListenPort)
	addon := &xbmc.Addon{
		Id:           addonId,
		Name:         addonName,
		Version:      util.Version[2:len(util.Version) - 1],
		ProviderName: config.Get().Info.Author,
		Extensions: []*xbmc.AddonExtension{
			&xbmc.AddonExtension{
				Point: "xbmc.addon.repository",
				Name:  addonName,
				Info: &xbmc.AddonRepositoryInfo{
					Text:       elementumHost + "/repository/elgatito/plugin.video.elementum/addons.xml",
					Compressed: false,
				},
				Checksum: elementumHost + "/repository/elgatito/plugin.video.elementum/addons.xml.md5",
				Datadir: &xbmc.AddonRepositoryDataDir{
					Text: elementumHost + "/repository/elgatito/",
					Zip:  true,
				},
			},
			&xbmc.AddonExtension{
				Point: "xbmc.addon.metadata",
				Summaries: []*xbmc.AddonText{
					&xbmc.AddonText{
						Text: "GitHub repository for Elementum updates",
						Lang: "en",
					},
				},
				Platform: "all",
			},
		},
	}

	addonPath := filepath.Clean(filepath.Join(config.Get().Info.Path, "..", addonId))
	if err := os.MkdirAll(addonPath, 0777); err != nil {
		return err
	}

	if err := copyFile(filepath.Join(config.Get().Info.Path, "icon.png"), filepath.Join(addonPath, "icon.png")); err != nil {
		return err
	}

	if err := copyFile(filepath.Join(config.Get().Info.Path, "fanart.jpg"), filepath.Join(addonPath, "fanart.jpg")); err != nil {
		return err
	}

	addonXmlFile, err := os.Create(filepath.Join(addonPath, "addon.xml"))
	if err != nil {
		return err
	}
	defer addonXmlFile.Close()
	return xml.NewEncoder(addonXmlFile).Encode(addon)
}
