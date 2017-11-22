package repository

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	// "io/ioutil"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"path/filepath"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/xbmc"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/github"
	"github.com/op/go-logging"
)

const (
	githubUserContentURL = "https://raw.githubusercontent.com/%s/%s/%s"
	releaseChangelog     = "[B]%s[/B] - %s\n%s\n\n"
)

var (
	addonZipRE       = regexp.MustCompile(`[\w]+\.[\w]+(\.[\w]+)?-\d+\.\d+\.\d+(-[\w]+\.?\d+)?\.zip`)
	addonChangelogRE = regexp.MustCompile(`changelog.*\.txt`)
	log              = logging.MustGetLogger("repository")
)

func getGithubClient() *github.Client {
	return github.NewClient(nil)
}

func getLastRelease(user string, repository string) (string, string) {
	client := getGithubClient()
	releases, _, err := client.Repositories.ListReleases(context.TODO(), user, repository, nil)

	if err != nil || len(releases) == 0 {
		return "", "master"
	}

	lastRelease := releases[0]
	return *lastRelease.TagName, *lastRelease.TargetCommitish
}

func getReleaseByTag(user string, repository string, tagName string) *github.RepositoryRelease {
	client := getGithubClient()
	releases, _, err := client.Repositories.ListReleases(context.TODO(), user, repository, nil)
	if err != nil {
		return nil
	}

	for _, release := range releases {
		if *release.TagName == tagName {
			return release
		}
	}
	return nil
}

func getAddons(user string, repository string) (*xbmc.AddonList, error) {
	var addons []xbmc.Addon

	_, lastReleaseBranch := getLastRelease(user, "plugin.video.elementum")
	resp, err := http.Get(fmt.Sprintf(githubUserContentURL, user, "plugin.video.elementum", lastReleaseBranch) + "/addon.xml")
	if err == nil && resp.StatusCode != 200 {
		err = errors.New(resp.Status)
	}

	_, lastReleaseBranch = getLastRelease(user, "script.elementum.burst")
	respBurst, errBurst := http.Get(fmt.Sprintf(githubUserContentURL, user, "script.elementum.burst", lastReleaseBranch) + "/addon.xml")
	if errBurst == nil && respBurst.StatusCode != 200 {
		errBurst = errors.New(respBurst.Status)
	}

	if err == nil {
		addon := xbmc.Addon{}
		xml.NewDecoder(resp.Body).Decode(&addon)
		addons = append(addons, addon)
	}

	if errBurst == nil {
		burst := xbmc.Addon{}
		xml.NewDecoder(respBurst.Body).Decode(&burst)
		addons = append(addons, burst)
	}

	return &xbmc.AddonList{
		Addons: addons,
	}, nil
}

func GetAddonsXML(ctx *gin.Context) {
	user := ctx.Params.ByName("user")
	repository := ctx.Params.ByName("repository")
	addons, err := getAddons(user, repository)
	if err != nil {
		ctx.AbortWithError(404, errors.New("Unable to retrieve the remote's addons.xml file."))
	}
	ctx.XML(200, addons)
}

func GetAddonsXMLChecksum(ctx *gin.Context) {
	user := ctx.Params.ByName("user")
	repository := ctx.Params.ByName("repository")
	addons, err := getAddons(user, repository)
	if len(addons.Addons) > 0 {
		log.Infof("Last available release of %s: v%s", repository, addons.Addons[0].Version)
	}
	if len(addons.Addons) > 1 {
		log.Infof("Last available release of script.elementum.burst: v%s", addons.Addons[1].Version)
	}
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

	lastReleaseTag := ""
	if repository == "plugin.video.elementum" {
		lastReleaseTag, _ = getLastRelease(user, repository)
		if lastReleaseTag == "" {
			// Get last release from addons.xml on master
			log.Warning("Unable to find a last tag, using master.")
			addons, err := getAddons(user, repository)
			if err != nil || len(addons.Addons) < 1 || addons.Addons[0].Version == "" {
				ctx.AbortWithError(404, errors.New("Unable to retrieve the remote's addon.xml file."))
				return
			}
			lastReleaseTag = "v" + addons.Addons[0].Version
		}
	} else {
		addons, err := getAddons(user, repository)
		if err != nil || len(addons.Addons) < 2 || addons.Addons[1].Version == "" {
			ctx.AbortWithError(404, errors.New("Unable to retrieve the addon.xml file."))
			return
		}
		lastReleaseTag = "v" + addons.Addons[1].Version
	}

	switch filepath {
	case "addons.xml":
		GetAddonsXML(ctx)
		return
	case "addons.xml.md5":
		go writeChangelog(user, "plugin.video.elementum")
		go writeChangelog(user, "script.elementum.burst")
		GetAddonsXMLChecksum(ctx)
		return
	case "fanart.jpg":
		fallthrough
	case "icon.png":
		ctx.Redirect(302, fmt.Sprintf(githubUserContentURL+"/"+filepath, user, repository, lastReleaseTag))
		return
	}

	switch {
	case addonZipRE.MatchString(filepath):
		addonZip(ctx, user, repository, lastReleaseTag)
	case addonChangelogRE.MatchString(filepath):
		writeChangelog(user, repository)
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
	if release == nil {
		return
	}

	client := getGithubClient()
	assets, _, err := client.Repositories.ListReleaseAssets(context.TODO(), user, repository, *release.ID, nil)
	if err != nil {
		ctx.Err()
		return
	}

	platformStruct := xbmc.GetPlatform()
	platform := platformStruct.OS + "_" + platformStruct.Arch
	var assetAllPlatforms string
	for _, asset := range assets {
		if strings.HasSuffix(*asset.Name, platform+".zip") {
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
}

func fetchChangelog(user string, repository string) (changelog string) {
	log.Infof("Fetching add-on changelog for %s...", repository)
	client := getGithubClient()
	releases, _, err := client.Repositories.ListReleases(context.TODO(), user, repository, nil)

	if err != nil || len(releases) == 0 {
		return
	}

	changelog = repository + " changelog\n======\n\n"
	for _, release := range releases {
		changelog += fmt.Sprintf(releaseChangelog, release.GetTagName(), release.GetPublishedAt().Format("Jan 2 2006"), release.GetBody())
	}

	return
}

func writeChangelog(user string, repository string) error {
	changelog := fetchChangelog(user, repository)
	lines := strings.Split(changelog, "\n")
	path := filepath.Clean(filepath.Join(config.Get().Info.Path, "..", repository, "changelog.txt"))

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
