package bittorrent

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash"
	"github.com/dustin/go-humanize"
	"github.com/sanity-io/litter"
	"golang.org/x/time/rate"

	"github.com/anacrolix/dht"
	"github.com/anacrolix/missinggo/conntrack"
	gotorrent "github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/iplist"
	"github.com/anacrolix/torrent/storage"

	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/diskusage"
	"github.com/elgatito/elementum/scrape"
	estorage "github.com/elgatito/elementum/storage"
	memory "github.com/elgatito/elementum/storage/memory"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

// BTService ...
type BTService struct {
	config *config.Configuration
	mu     sync.Mutex

	InternalProxy *http.Server

	Client       *gotorrent.Client
	ClientConfig *gotorrent.ClientConfig

	PieceCompletion storage.PieceCompletion
	DefaultStorage  estorage.ElementumStorage

	DownloadLimiter *rate.Limiter
	UploadLimiter   *rate.Limiter

	Players  map[string]*BTPlayer
	Torrents map[string]*Torrent

	UserAgent   string
	PeerID      string
	ListenIP    string
	ListenIPv6  string
	ListenPort  int
	DisableIPv6 bool

	dialogProgressBG *xbmc.DialogProgressBG

	SpaceChecked map[string]bool
	MarkedToMove string
}

type activeTorrent struct {
	torrentName  string
	downloadRate float64
	uploadRate   float64
	progress     int
}

// NewBTService ...
func NewBTService() *BTService {
	s := &BTService{
		config: config.Get(),

		SpaceChecked: make(map[string]bool, 0),
		MarkedToMove: "",

		Torrents: map[string]*Torrent{},
		Players:  map[string]*BTPlayer{},

		// TODO: cleanup when limiting is finished
		DownloadLimiter: rate.NewLimiter(rate.Inf, 2<<16),
		UploadLimiter:   rate.NewLimiter(rate.Inf, 2<<16),
	}

	s.configure()

	tmdb.CheckAPIKey()

	go s.loadTorrentFiles()
	go s.downloadProgress()

	return s
}

// Close ...
func (s *BTService) Close(shutdown bool) {
	log.Info("Stopping BT Services...")
	if !shutdown {
		s.stopServices()
	}

	log.Info("Closing Client")
	if s.Client != nil {
		s.Client.Close()
		s.Client = nil
	}
}

// Reconfigure fired every time addon configuration has changed
// and Kodi sent a notification about that.
// Should reassemble Service configuration and restart everything.
// For non-memory storage it should also load old torrent files.
func (s *BTService) Reconfigure() {
	s.stopServices()

	config.Reload()
	scrape.Reload()

	s.config = config.Get()
	s.configure()

	go s.loadTorrentFiles()
}

func (s *BTService) configure() {
	log.Info("Configuring client...")

	if s.config.InternalProxyEnabled {
		log.Infof("Starting internal proxy")
		s.InternalProxy = scrape.StartProxy()
	}

	if _, err := os.Stat(s.config.TorrentsPath); os.IsNotExist(err) {
		if err := os.Mkdir(s.config.TorrentsPath, 0755); err != nil {
			log.Error("Unable to create Torrents folder")
		}
	}

	if completion, errPC := storage.NewBoltPieceCompletion(s.config.ProfilePath); errPC == nil {
		s.PieceCompletion = completion
	} else {
		log.Warningf("Cannot initialize BoltPieceCompletion: %#v", errPC)
		s.PieceCompletion = storage.NewMapPieceCompletion()
	}

	var err error
	s.ListenIP, s.ListenIPv6, s.ListenPort, s.DisableIPv6, err = util.GetListenAddr(s.config.ListenAutoDetectIP, s.config.ListenAutoDetectPort, s.config.ListenInterfaces, s.config.ListenPortMin, s.config.ListenPortMax)
	if err != nil {
		log.Error(err)
		os.Exit(1)
	}

	log.Infof("ListenIP=%s, ListenIPv6=%s, ListenPort=%d, DisableIPv6=%v", s.ListenIP, s.ListenIPv6, s.ListenPort, s.DisableIPv6)

	blocklist, _ := iplist.MMapPackedFile(filepath.Join(config.Get().Info.Path, "resources", "misc", "pack-iplist"))

	s.PeerID, s.UserAgent = util.GetUserAndPeer()
	log.Infof("UserAgent: %s, PeerID: %s", s.UserAgent, s.PeerID)

	if s.config.ConnectionsLimit == 0 {
		setPlatformSpecificSettings(s.config)
	}

	if s.config.DownloadRateLimit == 0 {
		s.DownloadLimiter = rate.NewLimiter(rate.Inf, 0)
	}
	if s.config.UploadRateLimit == 0 {
		s.UploadLimiter = rate.NewLimiter(rate.Inf, 0)
	}

	log.Infof("DownloadStorage: %s", estorage.Storages[s.config.DownloadStorage])
	if s.config.DownloadStorage == estorage.StorageMemory {
		memSize := int64(config.Get().MemorySize)
		needSize := int64(s.config.BufferSize) + endBufferSize + 6*1024*1024

		if memSize < needSize {
			log.Noticef("Raising memory size (%d) to fit all the buffer (%d)", memSize, needSize)
			memSize = needSize
		}

		s.DefaultStorage = memory.NewMemoryStorage(memSize)
	} else if s.config.DownloadStorage == estorage.StorageFat32 {
		s.DefaultStorage = estorage.NewFat32Storage(config.Get().DownloadPath)
	} else if s.config.DownloadStorage == estorage.StorageMMap {
		s.DefaultStorage = estorage.NewMMapStorage(config.Get().DownloadPath, s.PieceCompletion)
	} else {
		s.DefaultStorage = estorage.NewFileStorage(config.Get().DownloadPath, s.PieceCompletion)
	}
	s.DefaultStorage.SetReadaheadSize(s.GetBufferSize())

	s.ClientConfig = gotorrent.NewDefaultClientConfig()

	s.ClientConfig.DataDir = config.Get().DownloadPath
	s.ClientConfig.DisableIPv6 = s.DisableIPv6
	s.ClientConfig.ListenHost = s.GetListenIP
	s.ClientConfig.ListenPort = s.ListenPort

	s.ClientConfig.Debug = false

	s.ClientConfig.DisableTCP = s.config.DisableTCP
	s.ClientConfig.DisableUTP = s.config.DisableUTP

	if config.Get().ProxyUseDownload {
		s.ClientConfig.ProxyURL = s.config.ProxyURL
	}
	if config.Get().ProxyUseTracker {
		if fixedURL, err := url.Parse(s.config.ProxyURL); err == nil {
			s.ClientConfig.HTTPProxy = http.ProxyURL(fixedURL)
		}
	}

	if s.config.ProxyURL != "" {
		s.ClientConfig.DisableUTP = true
		log.Info("Disabling UTP because of enabled proxy and not working UDP proxying")
	}

	s.ClientConfig.NoDefaultPortForwarding = s.config.DisableUPNP

	s.ClientConfig.NoDHT = s.config.DisableDHT
	s.ClientConfig.DhtStartingNodes = dht.GlobalBootstrapAddrs

	s.ClientConfig.Seed = s.config.SeedTimeLimit > 0
	s.ClientConfig.NoUpload = s.config.DisableUpload

	s.ClientConfig.EncryptionPolicy = gotorrent.EncryptionPolicy{
		DisableEncryption: s.config.EncryptionPolicy == 1,
		ForceEncryption:   s.config.EncryptionPolicy == 2,
	}

	s.ClientConfig.DownloadRateLimiter = s.DownloadLimiter
	s.ClientConfig.UploadRateLimiter = s.UploadLimiter

	s.ClientConfig.Bep20 = s.PeerID
	s.ClientConfig.PeerID = util.PeerIDRandom(s.PeerID)

	s.ClientConfig.HTTPUserAgent = s.UserAgent

	// Modify default Connections settings
	s.ClientConfig.EstablishedConnsPerTorrent = s.config.ConnectionsLimit
	s.ClientConfig.TorrentPeersHighWater = max(s.config.ConnectionsLimit*10, 3000)
	s.ClientConfig.HalfOpenConnsPerTorrent = max(int(s.config.ConnectionsLimit/2), 50)

	// Modify ConnTracker default values
	if s.config.ConnTrackerLimitAuto || s.config.ConnTrackerLimit == 0 {
		s.ClientConfig.ConnTracker.SetMaxEntries(s.config.ConnectionsLimit * 15)
	} else {
		s.ClientConfig.ConnTracker.SetMaxEntries(max(s.config.ConnTrackerLimit, 10))
	}

	s.ClientConfig.ConnTracker.Timeout = func(e conntrack.Entry) time.Duration {
		return 10 * time.Second
	}

	if !s.config.LimitAfterBuffering {
		s.RestoreLimits()
	}

	log.Debugf("BitClient config: %s", litter.Sdump(s.ClientConfig))

	s.ClientConfig.IPBlocklist = blocklist
	s.ClientConfig.DefaultStorage = s.DefaultStorage

	if s.Client, err = gotorrent.NewClient(s.ClientConfig); err != nil {
		// If client can't be created - we should panic
		log.Errorf("Error creating bit client: %#v", err)

		// Maybe we should use Dialog() to show a windows
		xbmc.Notify("Elementum", "LOCALIZE[30354]", config.AddonIcon())
		os.Exit(1)
	}
	log.Infof("Client created successfully")
	// TODO: can't dump Client because of blocklist array
	// log.Debugf("Created bit client: %#v", s.Client)
	for _, addr := range s.Client.ListenAddrs() {
		log.Debugf("Client listening on %s: %s", addr.Network(), addr.String())
	}

	// Setting it here to avoid spamming the log file
	// s.Client.SetIPBlockList(blocklist)
}

func (s *BTService) stopServices() {
	if s.InternalProxy != nil {
		log.Infof("Stopping internal proxy")
		s.InternalProxy.Shutdown(nil)
	}

	// TODO: cleanup these messages after windows hang is fixed
	// Don't need to execute RPC calls when Kodi is closing
	if s.dialogProgressBG != nil {
		log.Debugf("Closing existing Dialog")
		s.dialogProgressBG.Close()
	}
	s.dialogProgressBG = nil

	log.Debugf("Cleaning up all DialogBG")
	xbmc.DialogProgressBGCleanup()

	log.Debugf("Resetting RPC")
	xbmc.ResetRPC()

	if s.PieceCompletion != nil {
		if errClose := s.PieceCompletion.Close(); errClose != nil {
			log.Debugf("Cannot close piece completion: %#v", errClose)
		}
	}
	if s.Client != nil {

		log.Debugf("Closing Client")
		s.Client.Close()
		s.Client = nil
	}
}

// CheckAvailableSpace ...
func (s *BTService) CheckAvailableSpace(torrent *Torrent) bool {
	// For memory storage we don't need to check available space
	if s.config.DownloadStorage != estorage.StorageMemory {
		return true
	}

	diskStatus, err := diskusage.DiskUsage(config.Get().DownloadPath)
	if err != nil {
		log.Warningf("Unable to retrieve the free space for %s, continuing anyway...", config.Get().DownloadPath)
		return false
	}

	if torrent == nil || torrent.Info() == nil {
		log.Warning("Missing torrent info to check available space.")
		return false
	}

	totalSize := torrent.BytesCompleted() + torrent.BytesMissing()
	totalDone := torrent.BytesCompleted()
	sizeLeft := torrent.BytesMissing()
	availableSpace := diskStatus.Free
	path := s.ClientConfig.DataDir

	if torrent.IsRarArchive {
		sizeLeft = sizeLeft * 2
	}

	log.Infof("Checking for sufficient space on %s...", path)
	log.Infof("Total size of download: %s", humanize.Bytes(uint64(totalSize)))
	log.Infof("All time download: %s", humanize.Bytes(uint64(torrent.BytesCompleted())))
	log.Infof("Size total done: %s", humanize.Bytes(uint64(totalDone)))
	if torrent.IsRarArchive {
		log.Infof("Size left to download (x2 to extract): %s", humanize.Bytes(uint64(sizeLeft)))
	} else {
		log.Infof("Size left to download: %s", humanize.Bytes(uint64(sizeLeft)))
	}
	log.Infof("Available space: %s", humanize.Bytes(uint64(availableSpace)))

	if availableSpace < sizeLeft {
		log.Errorf("Unsufficient free space on %s. Has %d, needs %d.", path, diskStatus.Free, sizeLeft)
		xbmc.Notify("Elementum", "LOCALIZE[30207]", config.AddonIcon())

		torrent.Pause()
		return false
	}

	return true
}

// AddTorrent ...
func (s *BTService) AddTorrent(uri string) (*Torrent, error) {
	log.Infof("Adding torrent from %s", uri)

	if s.config.DownloadStorage != estorage.StorageMemory && s.config.DownloadPath == "." {
		xbmc.Notify("Elementum", "LOCALIZE[30113]", config.AddonIcon())
		return nil, fmt.Errorf("Download path empty")
	}

	var err error
	var torrentHandle *gotorrent.Torrent
	if strings.HasPrefix(uri, "magnet:") {
		if torrentHandle, err = s.Client.AddMagnet(uri); err != nil {
			return nil, err
		} else if torrentHandle == nil {
			return nil, errors.New("Could not add torrent")
		}
		uri = ""
	} else {
		if strings.HasPrefix(uri, "http") {
			torrent := NewTorrentFile(uri)

			if err = torrent.Resolve(); err != nil {
				log.Warningf("Could not resolve torrent %s: %#v", uri, err)
				return nil, err
			}
			uri = torrent.URI
		}

		log.Debugf("Adding torrent: %#v", uri)
		if torrentHandle, err = s.Client.AddTorrentFromFile(uri); err != nil {
			log.Warningf("Could not add torrent %s: %#v", uri, err)
			return nil, err
		} else if torrentHandle == nil {
			return nil, errors.New("Could not add torrent")
		}

	}

	log.Debugf("Making new torrent item with url = '%s'", uri)
	torrent := NewTorrent(s, torrentHandle, uri)
	if s.config.ConnectionsLimit > 0 {
		torrentHandle.SetMaxEstablishedConns(s.config.ConnectionsLimit)
	}

	s.Torrents[torrent.infoHash] = torrent

	go torrent.SaveMetainfo(s.config.TorrentsPath)
	go torrent.Watch()

	return torrent, nil
}

// RemoveTorrent ...
func (s *BTService) RemoveTorrent(torrent *Torrent, removeFiles bool) bool {
	log.Debugf("Removing torrent: %s", torrent.Name())
	if torrent == nil {
		return false
	}

	defer func() {
		database.Get().DeleteBTItem(torrent.InfoHash())
	}()

	if t, ok := s.Torrents[torrent.infoHash]; ok {
		delete(s.Torrents, torrent.infoHash)
		t.Drop(removeFiles)
		return true
	}

	return false
}

func (s *BTService) loadTorrentFiles() {
	// Not loading previous torrents on start
	// Otherwise we can dig out all the memory and halt the device
	if s.config.DownloadStorage == estorage.StorageMemory || !s.config.AutoloadTorrents {
		return
	}

	pattern := filepath.Join(s.config.TorrentsPath, "*.torrent")
	files, _ := filepath.Glob(pattern)

	for _, torrentFile := range files {
		log.Infof("Loading torrent file %s", torrentFile)

		var err error
		var torrentHandle *gotorrent.Torrent
		if torrentHandle, err = s.Client.AddTorrentFromFile(torrentFile); err != nil || torrentHandle == nil {
			log.Errorf("Error adding torrent file for %s", torrentFile)
			if _, err := os.Stat(torrentFile); err == nil {
				if err := os.Remove(torrentFile); err != nil {
					log.Error(err)
				}
			}

			continue
		}

		t, _ := s.AddTorrent(torrentFile)
		if t != nil {
			i := database.Get().GetBTItem(t.InfoHash())

			if i != nil {
				t.DBItem = i

				for _, p := range i.Files {
					for _, f := range t.Torrent.Files() {
						if f.Path() == p {
							t.ChosenFiles = append(t.ChosenFiles, f)
							f.Download()
						}
					}
				}
			}
		}
	}
}

func (s *BTService) downloadProgress() {
	rotateTicker := time.NewTicker(5 * time.Second)
	defer rotateTicker.Stop()

	pathChecked := make(map[string]bool)
	warnedMissing := make(map[string]bool)

	showNext := 0
	for {
		select {
		case <-rotateTicker.C:
			// TODO: there should be a check whether service is in Pause state
			// if !s.config.DisableBgProgress && s.dialogProgressBG != nil {
			// 	s.dialogProgressBG.Close()
			// 	s.dialogProgressBG = nil
			// 	continue
			// }

			var totalDownloadRate int64
			var totalUploadRate int64
			var totalProgress int

			activeTorrents := make([]*activeTorrent, 0)

			for i, torrentHandle := range s.Torrents {
				if torrentHandle == nil {
					continue
				}

				torrentName := torrentHandle.Info().Name
				progress := int(torrentHandle.GetProgress())
				status := torrentHandle.GetState()

				totalDownloadRate += torrentHandle.DownloadRate
				totalUploadRate += torrentHandle.UploadRate

				if progress < 100 && status != StatusPaused {
					activeTorrents = append(activeTorrents, &activeTorrent{
						torrentName:  torrentName,
						downloadRate: float64(torrentHandle.DownloadRate),
						uploadRate:   float64(torrentHandle.UploadRate),
						progress:     progress,
					})
					totalProgress += progress
					continue
				}

				if s.MarkedToMove != "" && i == s.MarkedToMove {
					s.MarkedToMove = ""
					status = StatusSeeding
				}

				//
				// Handle moving completed downloads
				//
				if !s.config.CompletedMove || status != StatusSeeding || s.anyPlayerIsPlaying() {
					continue
				}
				if xbmc.PlayerIsPlaying() {
					continue
				}

				infoHash := torrentHandle.InfoHash()
				if _, exists := warnedMissing[infoHash]; exists {
					continue
				}

				func() error {
					item := database.Get().GetBTItem(infoHash)
					if item == nil {
						warnedMissing[infoHash] = true
						return fmt.Errorf("Torrent not found with infohash: %s", infoHash)
					}

					errMsg := fmt.Sprintf("Missing item type to move files to completed folder for %s", torrentName)
					if item.Type == "" {
						log.Error(errMsg)
						return errors.New(errMsg)
					}
					log.Warning(torrentName, "finished seeding, moving files...")

					// Check paths are valid and writable, and only once
					if _, exists := pathChecked[item.Type]; !exists {
						if item.Type == "movie" {
							if err := config.IsWritablePath(s.config.CompletedMoviesPath); err != nil {
								warnedMissing[infoHash] = true
								pathChecked[item.Type] = true
								log.Error(err)
								return err
							}
							pathChecked[item.Type] = true
						} else {
							if err := config.IsWritablePath(s.config.CompletedShowsPath); err != nil {
								warnedMissing[infoHash] = true
								pathChecked[item.Type] = true
								log.Error(err)
								return err
							}
							pathChecked[item.Type] = true
						}
					}

					log.Info("Removing the torrent without deleting files after Completed move ...")
					s.RemoveTorrent(torrentHandle, false)

					// Delete torrent file
					torrentFile := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))
					if _, err := os.Stat(torrentFile); err == nil {
						log.Info("Deleting torrent file at ", torrentFile)
						if err := os.Remove(torrentFile); err != nil {
							log.Error(err)
							return err
						}
					}

					if len(item.Files) <= 0 {
						return errors.New("No files saved for BTItem")
					}

					// TODO: change logic to move all selected files, not just one
					filePath := ""
					fileName := ""
					for _, p := range item.Files {
						for _, f := range torrentHandle.Files() {
							if f.Path() == p {
								filePath = f.Path()
								fileName = filepath.Base(filePath)
							}
						}
					}

					if filePath == "" {
						return fmt.Errorf("Cannot find saved files: %#v", item.Files)
					}

					extracted := ""
					re := regexp.MustCompile("(?i).*\\.rar")
					if re.MatchString(fileName) {
						extractedPath := filepath.Join(s.config.DownloadPath, filepath.Dir(filePath), "extracted")
						files, err := ioutil.ReadDir(extractedPath)
						if err != nil {
							return err
						}
						if len(files) == 1 {
							extracted = files[0].Name()
						} else {
							for _, file := range files {
								fileNameCurrent := file.Name()
								re := regexp.MustCompile("(?i).*\\.(mkv|mp4|mov|avi)")
								if re.MatchString(fileNameCurrent) {
									extracted = fileNameCurrent
									break
								}
							}
						}
						if extracted != "" {
							filePath = filepath.Join(filepath.Dir(filePath), "extracted", extracted)
						} else {
							return errors.New("No extracted file to move")
						}
					}

					var dstPath string
					if item.Type == "movie" {
						dstPath = filepath.Dir(s.config.CompletedMoviesPath)
					} else {
						dstPath = filepath.Dir(s.config.CompletedShowsPath)
						if item.ShowID > 0 {
							show := tmdb.GetShow(item.ShowID, config.Get().Language)
							if show != nil {
								showPath := util.ToFileName(fmt.Sprintf("%s (%s)", show.Name, strings.Split(show.FirstAirDate, "-")[0]))
								seasonPath := filepath.Join(showPath, fmt.Sprintf("Season %d", item.Season))
								if item.Season == 0 {
									seasonPath = filepath.Join(showPath, "Specials")
								}
								dstPath = filepath.Join(dstPath, seasonPath)
								os.MkdirAll(dstPath, 0755)
							}
						}
					}

					go func() {
						log.Infof("Moving %s to %s", fileName, dstPath)
						srcPath := filepath.Join(s.config.DownloadPath, filePath)
						if dst, err := util.Move(srcPath, dstPath); err != nil {
							log.Error(err)
						} else {
							// Remove leftover folders
							if dirPath := filepath.Dir(filePath); dirPath != "." {
								os.RemoveAll(filepath.Dir(srcPath))
								if extracted != "" {
									parentPath := filepath.Clean(filepath.Join(filepath.Dir(srcPath), ".."))
									if parentPath != "." && parentPath != s.config.DownloadPath {
										os.RemoveAll(parentPath)
									}
								}
							}
							log.Warning(fileName, "moved to", dst)

							log.Infof("Marking %s for removal from library and database...", torrentName)
							database.Get().UpdateStatusBTItem(infoHash, Remove)
						}
					}()
					return nil
				}()
			}

			totalActive := len(activeTorrents)
			if totalActive > 0 {
				showProgress := totalProgress / totalActive
				showTorrent := fmt.Sprintf("Total - D/L: %s - U/L: %s", humanize.Bytes(uint64(totalDownloadRate))+"/s", humanize.Bytes(uint64(totalUploadRate))+"/s")
				if showNext >= totalActive {
					showNext = 0
				} else {
					showProgress = activeTorrents[showNext].progress
					torrentName := activeTorrents[showNext].torrentName
					if len(torrentName) > 30 {
						torrentName = torrentName[:30] + "..."
					}
					showTorrent = fmt.Sprintf("%s - %s - %s", torrentName, humanize.Bytes(uint64(activeTorrents[showNext].downloadRate))+"/s", humanize.Bytes(uint64(activeTorrents[showNext].uploadRate))+"/s")
					showNext++
				}
				if !s.config.DisableBgProgress && (!s.config.DisableBgProgressPlayback || !s.anyPlayerIsPlaying()) {
					if s.dialogProgressBG == nil {
						s.dialogProgressBG = xbmc.NewDialogProgressBG("Elementum", "")
					}
					if s.dialogProgressBG != nil {
						s.dialogProgressBG.Update(showProgress, "Elementum", showTorrent)
					}
				}
			} else if (!s.config.DisableBgProgress || (s.config.DisableBgProgressPlayback && s.anyPlayerIsPlaying())) && s.dialogProgressBG != nil {
				s.dialogProgressBG.Close()
				s.dialogProgressBG = nil
			}
		}
	}
}

// SetDownloadLimit ...
func (s *BTService) SetDownloadLimit(i int) {
	if i == 0 {
		s.DownloadLimiter.SetLimit(rate.Inf)
	} else {
		s.DownloadLimiter.SetLimit(rate.Limit(i))
	}
}

// SetUploadLimit ...
func (s *BTService) SetUploadLimit(i int) {
	if i == 0 {
		s.UploadLimiter.SetLimit(rate.Inf)
	} else {
		s.UploadLimiter.SetLimit(rate.Limit(i))
	}
}

// RestoreLimits ...
func (s *BTService) RestoreLimits() {
	if s.config.DownloadRateLimit > 0 {
		s.SetDownloadLimit(s.config.DownloadRateLimit)
		log.Infof("Rate limiting download to %dkB/s", s.config.DownloadRateLimit/1024)
	} else {
		s.SetDownloadLimit(0)
	}

	if s.config.UploadRateLimit > 0 {
		s.SetUploadLimit(s.config.UploadRateLimit)
		log.Infof("Rate limiting upload to %dkB/s", s.config.UploadRateLimit/1024)
	} else {
		s.SetUploadLimit(0)
	}
}

// SetBufferingLimits ...
func (s *BTService) SetBufferingLimits() {
	if s.config.LimitAfterBuffering {
		s.SetDownloadLimit(0)
		log.Info("Resetting rate limited download for buffering")
	}
}

// GetSeedTime ...
func (s *BTService) GetSeedTime() int64 {
	if s.config.DisableUpload {
		return 0
	}

	return int64(s.config.SeedTimeLimit)
}

// GetBufferSize ...
func (s *BTService) GetBufferSize() int64 {
	b := int64(s.config.BufferSize)
	if b < endBufferSize {
		return endBufferSize
	}
	return b
}

// GetMemorySize ...
func (s *BTService) GetMemorySize() int64 {
	return int64(s.config.MemorySize)
}

// GetStorageType ...
func (s *BTService) GetStorageType() int {
	return s.config.DownloadStorage
}

// PlayerStop ...
func (s *BTService) PlayerStop() {
	log.Debugf("PlayerStop")

	// if s.config.DownloadStorage == estorage.StorageMemory {
	// 	s.DefaultStorage.Close()
	// }
}

// PlayerSeek ...
func (s *BTService) PlayerSeek() {
	log.Debugf("PlayerSeek")

	// for _, t := range s.Torrents {
	// 	go t.SeekEvent()
	// }
}

// ClientInfo ...
func (s *BTService) ClientInfo(w io.Writer) {
	s.Client.WriteStatus(w)
	s.ClientConfig.ConnTracker.PrintStatus(w)
}

// AttachPlayer adds Player instance to service
func (s *BTService) AttachPlayer(p *BTPlayer) {
	if p == nil || p.Torrent == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Players[p.Torrent.InfoHash()]; ok {
		return
	}

	s.Players[p.Torrent.InfoHash()] = p
}

// DetachPlayer removes Player instance
func (s *BTService) DetachPlayer(p *BTPlayer) {
	if p == nil || p.Torrent == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.Players, p.Torrent.InfoHash())
}

// GetPlayer searches for player with desired TMDB id
func (s *BTService) GetPlayer(kodiID int, tmdbID int) *BTPlayer {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.Players {
		if p == nil || p.Torrent == nil {
			continue
		}

		if (tmdbID != 0 && p.p.TMDBId == tmdbID) || (kodiID != 0 && p.p.KodiID == kodiID) {
			return p
		}
	}

	return nil
}

func (s *BTService) anyPlayerIsPlaying() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.Players {
		if p == nil || p.Torrent == nil {
			continue
		}

		if p.p.Playing {
			return true
		}
	}

	return false
}

// GetActivePlayer searches for player that is Playing anything
func (s *BTService) GetActivePlayer() *BTPlayer {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.Players {
		if p == nil || p.Torrent == nil {
			continue
		}

		if p.p.Playing {
			return p
		}
	}

	return nil
}

// HasTorrentByID checks whether there is active torrent for queried tmdb id
func (s *BTService) HasTorrentByID(tmdbID int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.Torrents {
		if t == nil || t.DBItem == nil {
			continue
		}

		if t.DBItem.ID == tmdbID {
			return t.InfoHash()
		}
	}

	return ""
}

// HasTorrentByQuery checks whether there is active torrent with searches query
func (s *BTService) HasTorrentByQuery(query string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.Torrents {
		if t == nil || t.DBItem == nil {
			continue
		}

		if t.DBItem.Query == query {
			return t.InfoHash()
		}
	}

	return ""
}

// HasTorrentBySeason checks whether there is active torrent for queried season
func (s *BTService) HasTorrentBySeason(tmdbID int, season int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.Torrents {
		if t == nil || t.DBItem == nil {
			continue
		}

		if t.DBItem.ShowID == tmdbID && t.DBItem.Season == season {
			return t.InfoHash()
		}
	}

	return ""
}

// HasTorrentByEpisode checks whether there is active torrent for queried episode
func (s *BTService) HasTorrentByEpisode(tmdbID int, season, episode int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.Torrents {
		if t == nil || t.DBItem == nil {
			continue
		}

		if t.DBItem.ShowID == tmdbID && t.DBItem.Season == season && t.DBItem.Episode == episode {
			return t.InfoHash()
		}
	}

	return ""
}

// HasTorrentByName checks whether there is active torrent for queried name
func (s *BTService) HasTorrentByName(query string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.Torrents {
		if t == nil {
			continue
		}

		if strings.Contains(t.Name(), query) {
			return t.InfoHash()
		}
	}

	return ""
}

// GetTorrentByFakeID checks whether there is active torrent with fake id
func (s *BTService) GetTorrentByFakeID(query string) *Torrent {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.Torrents {
		if t == nil || t.DBItem == nil {
			continue
		}

		id := strconv.FormatUint(xxhash.Sum64String(t.DBItem.Query), 10)
		if id == query {
			return t
		}
	}

	return nil
}

// GetListenIP returns calculated IP for TCP/TCP6
func (s *BTService) GetListenIP(network string) string {
	if strings.Contains(network, "6") {
		return s.ListenIPv6
	}
	return s.ListenIP
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
