package repository

import (
	"os"
	"fmt"
	"bufio"
	"errors"
	"regexp"
	"strings"
	"net/http"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"path/filepath"

	"github.com/op/go-logging"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/github"
	"github.com/scakemyer/quasar/config"
	"github.com/scakemyer/quasar/xbmc"
)

const (
	githubUserContentURL = "https://raw.githubusercontent.com/%s/%s/%s"
	backupRepositoryURL  = "https://offshoregit.com/%s/%s"
	releaseChangelog     = "[B]%s[/B] - %s\n%s\n\n"
)

var (
	addonZipRE       = regexp.MustCompile(`[\w]+\.[\w]+(\.[\w]+)?-\d+\.\d+\.\d+(-[\w]+\.?\d+)?\.zip`)
	addonChangelogRE = regexp.MustCompile(`changelog.*\.txt`)
	log              = logging.MustGetLogger("repository")
)

func getLastRelease(user string, repository string) (string, string) {
	client := github.NewClient(nil)
	releases, _, _ := client.Repositories.ListReleases(user, repository, nil)
	if len(releases) > 0 {
		lastRelease := releases[0]
		return *lastRelease.TagName, *lastRelease.TargetCommitish
	}
	return "", "master"
}

func getReleaseByTag(user string, repository string, tagName string) *github.RepositoryRelease {
	client := github.NewClient(nil)
	releases, _, _ := client.Repositories.ListReleases(user, repository, nil)
	for _, release := range releases {
		if *release.TagName == tagName {
			return release
		}
	}
	return nil
}

func getAddons(user string, repository string) (*xbmc.AddonList, error) {
	_, lastReleaseBranch := getLastRelease(user, repository)
	resp, err := http.Get(fmt.Sprintf(githubUserContentURL, user, repository, lastReleaseBranch) + "/addon.xml")
	if err == nil && resp.StatusCode != 200 {
		err = errors.New(resp.Status)
	}
	if err != nil {
		log.Warning("Unable to retrieve the addon.xml file, checking backup repository...")
		resp, err = http.Get(fmt.Sprintf(backupRepositoryURL, user, repository) + "/addon.xml")
		if err == nil && resp.StatusCode != 200 {
			err = errors.New(resp.Status)
		}
		if err != nil {
			log.Error("Unable to retrieve the backup's addon.xml file.")
			return nil, err
		}
	}
	addon := xbmc.Addon{}
	xml.NewDecoder(resp.Body).Decode(&addon)
	return &xbmc.AddonList{
		Addons: []xbmc.Addon{addon},
	}, nil
}

func GetAddonsXML(ctx *gin.Context) {
	user := ctx.Params.ByName("user")
	repository := ctx.Params.ByName("repository")
	addons, err := getAddons(user, repository)
	if err != nil {
		ctx.AbortWithError(404, errors.New("Unable to retrieve the remote's addon.xml file."))
	}
	ctx.XML(200, addons)
}

func GetAddonsXMLChecksum(ctx *gin.Context) {
	user := ctx.Params.ByName("user")
	repository := ctx.Params.ByName("repository")
	addons, err := getAddons(user, repository)
	if err != nil {
		ctx.Error(errors.New("Unable to retrieve the remote's addon.xml file."))
	}
	hasher := md5.New()
	xml.NewEncoder(hasher).Encode(addons)
	ctx.String(200, hex.EncodeToString(hasher.Sum(nil)))
}

func GetAddonFiles(ctx *gin.Context) {
	user := ctx.Params.ByName("user")
	repository := ctx.Params.ByName("repository")
	filepath := ctx.Params.ByName("filepath")[1:] // strip the leading "/"

	lastReleaseTag, lastReleaseBranch := getLastRelease(user, repository)
	if lastReleaseTag == "" {
		// Get last release from addons.xml on master
		log.Warning("Unable to find a last tag, using master.")
		addons, err := getAddons(user, repository)
		if err != nil {
			ctx.AbortWithError(404, errors.New("Unable to retrieve the remote's addon.xml file."))
			return
		}
		lastReleaseTag = "v" + addons.Addons[0].Version
	}
	log.Infof("Last release: %s on %s", lastReleaseTag, lastReleaseBranch)

	switch filepath {
	case "addons.xml":
		GetAddonsXML(ctx)
		return
	case "addons.xml.md5":
		GetAddonsXMLChecksum(ctx)
		writeChangelog(user, repository)
		return
	case "fanart.jpg":
		fallthrough
	case "icon.png":
		ctx.Redirect(302, fmt.Sprintf(githubUserContentURL + "/" + filepath, user, repository, lastReleaseTag))
		return
	}

	switch {
	case addonZipRE.MatchString(filepath):
		addonZip(ctx, user, repository, lastReleaseTag)
	case addonChangelogRE.MatchString(filepath):
		addonChangelog(ctx, user, repository)
	default:
		ctx.AbortWithError(404, errors.New(filepath))
	}
}

func GetAddonFilesHead(ctx *gin.Context) {
	ctx.String(200, "")
}

func addonZip(ctx *gin.Context, user string, repository string, lastReleaseTag string) {
	release := getReleaseByTag(user, repository, lastReleaseTag)
	// if there a release with an asset that matches a addon zip, use it
	if release != nil {
		client := github.NewClient(nil)
		assets, _, _ := client.Repositories.ListReleaseAssets(user, repository, *release.ID, nil)
		platformStruct := xbmc.GetPlatform()
		platform := platformStruct.OS + "_" + platformStruct.Arch
		var assetAllPlatforms string
		for _, asset := range assets {
			if strings.HasSuffix(*asset.Name, platform + ".zip") {
				assetPlatform := *asset.BrowserDownloadURL
				log.Infof("Using release asset for %s: %s", platform, assetPlatform)
				ctx.Redirect(302, assetPlatform)
				return
			}
			if addonZipRE.MatchString(*asset.Name) {
				assetAllPlatforms = *asset.BrowserDownloadURL
				log.Infof("Found all platforms release asset: %s", assetAllPlatforms)
				continue
			}
		}
		if assetAllPlatforms != "" {
			log.Infof("Using release asset for all platforms: %s", assetAllPlatforms)
			ctx.Redirect(302, assetAllPlatforms)
			return
		}
	} else {
		log.Warning("Release not found on main repository, using backup...")
		platformStruct := xbmc.GetPlatform()
		platform := platformStruct.OS + "_" + platformStruct.Arch
		backupRepoPath := fmt.Sprintf(backupRepositoryURL, user, repository)
		addonFile := fmt.Sprintf("%s-%s.%s.zip", repository, lastReleaseTag[1:], platform)
		assetPlatform := fmt.Sprintf("%s/%s/%s", backupRepoPath, lastReleaseTag, addonFile)
		log.Infof("Backup release asset: %s", assetPlatform)
		ctx.Redirect(302, assetPlatform)
		return
	}
}

func fetchChangelog(user string, repository string) string {
	log.Info("Fetching add-on changelog...")
	client := github.NewClient(nil)
	releases, _, _ := client.Repositories.ListReleases(user, repository, nil)
	changelog := "Quasar changelog\n======\n\n"
	for _, release := range releases {
		changelog += fmt.Sprintf(releaseChangelog, *release.TagName, release.PublishedAt.Format("Jan 2 2006"), *release.Body)
	}
	return changelog
}

func writeChangelog(user string, repository string) error {
	changelog := fetchChangelog(user, repository)
	lines := strings.Split(changelog, "\n")
	path := filepath.Clean(filepath.Join(config.Get().Info.Path, "changelog.txt"))

  file, err := os.Create(path)
  if err != nil {
    return err
  }
  defer file.Close()

  w := bufio.NewWriter(file)
  for _, line := range lines {
    fmt.Fprintln(w, line)
  }
  return w.Flush()
}

func addonChangelog(ctx *gin.Context, user string, repository string) {
	changelog := fetchChangelog(user, repository)
	ctx.String(200, changelog)
}
