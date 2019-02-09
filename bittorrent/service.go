package bittorrent

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash"
	"github.com/dustin/go-humanize"
	"github.com/shirou/gopsutil/mem"
	"github.com/zeebo/bencode"

	lt "github.com/ElementumOrg/libtorrent-go"

	"github.com/elgatito/elementum/broadcast"
	"github.com/elgatito/elementum/config"
	"github.com/elgatito/elementum/database"
	"github.com/elgatito/elementum/diskusage"
	"github.com/elgatito/elementum/scrape"
	"github.com/elgatito/elementum/tmdb"
	"github.com/elgatito/elementum/util"
	"github.com/elgatito/elementum/xbmc"
)

// BTService ...
type BTService struct {
	config *config.Configuration
	mu     sync.Mutex

	Session      lt.Session
	PackSettings lt.SettingsPack

	InternalProxy *http.Server

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

	alertsBroadcaster *broadcast.Broadcaster
	Closing           *broadcast.Closer
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

		Torrents: map[string]*Torrent{},
		Players:  map[string]*BTPlayer{},

		Closing:           broadcast.NewCloser(),
		alertsBroadcaster: broadcast.NewBroadcaster(),
	}

	s.configure()
	s.startServices()

	go s.saveResumeDataConsumer()
	go s.saveResumeDataLoop()
	go s.alertsConsumer()
	go s.logAlerts()

	tmdb.CheckAPIKey()

	go s.loadTorrentFiles()
	go s.downloadProgress()

	return s
}

// Close ...
func (s *BTService) Close() {
	defer s.Closing.Close()

	now := time.Now()
	log.Info("Stopping BT Services...")
	s.stopServices()

	s.CloseSession()

	log.Infof("Closed service in %s", time.Since(now))
}

// CloseSession tries to close libtorrent session with a timeout,
// because it takes too much to close and Kodi hangs.
func (s *BTService) CloseSession() {
	log.Info("Closing Session")
	lt.DeleteSession(s.Session)
}

// func doWithTimeout(timeout time.Duration, doFunc func()) {
// 	timer := time.NewTimer(timeout)
// 	defer timer.Stop()

// 	select {
// 	case <-timer.C:
// 		break

// 	default:
// 		doFunc()
// 		return
// 	}

// 	// timeout handling
// 	log.Infof("Did not complete operation in time")
// }

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

	if config.Get().AntizapretEnabled {
		go scrape.PacParser.Update()
	}

	s.startServices()
	s.loadTorrentFiles()
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

	settings := lt.NewSettingsPack()
	s.Session = lt.NewSession(settings, int(lt.SessionHandleAddDefaultPlugins))

	log.Info("Applying session settings...")

	s.PeerID, s.UserAgent = util.GetUserAndPeer()
	log.Infof("UserAgent: %s, PeerID: %s", s.UserAgent, s.PeerID)
	settings.SetStr("user_agent", s.UserAgent)

	settings.SetInt("request_timeout", 2)
	settings.SetInt("peer_connect_timeout", 2)
	settings.SetBool("strict_end_game_mode", true)
	settings.SetBool("announce_to_all_trackers", true)
	settings.SetBool("announce_to_all_tiers", true)
	settings.SetInt("download_rate_limit", 0)
	settings.SetInt("upload_rate_limit", 0)
	settings.SetInt("choking_algorithm", 0)
	settings.SetInt("share_ratio_limit", 0)
	settings.SetInt("seed_time_ratio_limit", 0)
	settings.SetInt("seed_time_limit", 0)
	settings.SetInt("peer_tos", ipToSLowCost)
	settings.SetInt("torrent_connect_boost", 0)
	settings.SetBool("rate_limit_ip_overhead", true)
	settings.SetBool("no_atime_storage", true)
	settings.SetBool("announce_double_nat", true)
	settings.SetBool("prioritize_partial_pieces", false)
	settings.SetBool("free_torrent_hashes", true)
	settings.SetBool("use_parole_mode", true)
	settings.SetInt("seed_choking_algorithm", int(lt.SettingsPackFastestUpload))
	settings.SetBool("upnp_ignore_nonrouters", true)
	settings.SetBool("lazy_bitfields", true)
	settings.SetInt("stop_tracker_timeout", 1)
	settings.SetInt("auto_scrape_interval", 1200)
	settings.SetInt("auto_scrape_min_interval", 900)
	settings.SetBool("ignore_limits_on_local_network", true)
	settings.SetBool("rate_limit_utp", true)
	settings.SetInt("mixed_mode_algorithm", int(lt.SettingsPackPreferTcp))

	// For Android external storage / OS-mounted NAS setups
	if s.config.TunedStorage {
		settings.SetBool("use_read_cache", true)
		settings.SetBool("coalesce_reads", true)
		settings.SetBool("coalesce_writes", true)
		settings.SetInt("max_queued_disk_bytes", 10*1024*1024)
		settings.SetInt("cache_size", -1)
	}

	if s.config.ConnectionsLimit > 0 {
		settings.SetInt("connections_limit", s.config.ConnectionsLimit)
	} else {
		setPlatformSpecificSettings(settings)
	}

	if s.config.ConnTrackerLimitAuto || s.config.ConnTrackerLimit == 0 {
		settings.SetInt("connection_speed", 500)
	} else {
		settings.SetInt("connection_speed", s.config.ConnTrackerLimit)
	}

	if s.config.LimitAfterBuffering == false {
		if s.config.DownloadRateLimit > 0 {
			log.Infof("Rate limiting download to %s", humanize.Bytes(uint64(s.config.DownloadRateLimit)))
			settings.SetInt("download_rate_limit", s.config.DownloadRateLimit)
		}
		if s.config.UploadRateLimit > 0 {
			log.Infof("Rate limiting upload to %s", humanize.Bytes(uint64(s.config.UploadRateLimit)))
			// If we have an upload rate, use the nicer bittyrant choker
			settings.SetInt("upload_rate_limit", s.config.UploadRateLimit)
			settings.SetInt("choking_algorithm", int(lt.SettingsPackBittyrantChoker))
		}
	}
	if s.config.DisableUpload {
		s.Session.AddUploadExtension()
	}

	if !s.config.DisableUpload && s.config.ShareRatioLimit > 0 {
		settings.SetInt("share_ratio_limit", s.config.ShareRatioLimit)
	}
	if !s.config.DisableUpload && s.config.SeedTimeRatioLimit > 0 {
		settings.SetInt("seed_time_ratio_limit", s.config.SeedTimeRatioLimit)
	}
	if !s.config.DisableUpload && s.config.SeedTimeLimit > 0 {
		settings.SetInt("seed_time_limit", s.config.SeedTimeLimit)
	}

	log.Info("Applying encryption settings...")
	if s.config.EncryptionPolicy > 0 {
		policy := int(lt.SettingsPackPeDisabled)
		level := int(lt.SettingsPackPeBoth)
		preferRc4 := false

		if s.config.EncryptionPolicy == 2 {
			policy = int(lt.SettingsPackPeForced)
			level = int(lt.SettingsPackPeRc4)
			preferRc4 = true
		}

		settings.SetInt("out_enc_policy", policy)
		settings.SetInt("in_enc_policy", policy)
		settings.SetInt("allowed_enc_level", level)
		settings.SetBool("prefer_rc4", preferRc4)
	}

	if s.config.ProxyEnabled && config.Get().ProxyUseDownload && s.config.ProxyHost != "" {
		log.Info("Applying proxy settings...")
		proxyType := s.config.ProxyType + 1
		settings.SetInt("proxy_type", proxyType)
		settings.SetInt("proxy_port", s.config.ProxyPort)
		settings.SetStr("proxy_hostname", s.config.ProxyHost)
		settings.SetStr("proxy_username", s.config.ProxyLogin)
		settings.SetStr("proxy_password", s.config.ProxyPassword)
		settings.SetBool("proxy_tracker_connections", true)
		settings.SetBool("proxy_peer_connections", true)
		settings.SetBool("proxy_hostnames", true)
		settings.SetBool("force_proxy", true)
	}

	// Set alert_mask here so it also applies on reconfigure...
	settings.SetInt("alert_mask", int(
		lt.AlertStatusNotification|
			lt.AlertStorageNotification|
			lt.AlertErrorNotification))

	log.Infof("DownloadStorage: %s", Storages[s.config.DownloadStorage])
	if s.config.DownloadStorage == StorageMemory {
		needSize := s.config.BufferSize + int(EndBufferSize) + 8*1024*1024

		if config.Get().MemorySize < needSize {
			log.Noticef("Raising memory size (%d) to fit all the buffer (%d)", config.Get().MemorySize, needSize)
			config.Get().MemorySize = needSize
		}

		// lt.SetMemorySize(int64(config.Get().MemorySize))

		// Set Memory storage specific settings
		settings.SetBool("close_redundant_connections", false)
		settings.SetInt("share_ratio_limit", 0)
		settings.SetInt("seed_time_ratio_limit", 0)
		settings.SetInt("seed_time_limit", 0)
		settings.SetInt("active_downloads", -1)
		settings.SetInt("active_seeds", -1)
		settings.SetInt("active_limit", -1)
		settings.SetInt("active_tracker_limit", -1)
		settings.SetInt("active_dht_limit", -1)
		settings.SetInt("active_lsd_limit", -1)
		// settings.SetInt("unchoke_slots_limit", 0)

		// settings.SetBool("strict_end_game_mode", false)

		// settings.SetInt("disk_io_write_mode", 2)
		// settings.SetInt("disk_io_read_mode", 2)
		// settings.SetInt("cache_size", 0)
	}

	s.PackSettings = settings
	s.Session.GetHandle().ApplySettings(s.PackSettings)

	if !s.config.LimitAfterBuffering {
		s.RestoreLimits()
	}

}

func (s *BTService) startServices() {
	var listenPorts []string
	for p := s.config.ListenPortMin; p <= s.config.ListenPortMax; p++ {
		listenPorts = append(listenPorts, strconv.Itoa(p))
	}
	rand.Seed(time.Now().UTC().UnixNano())

	listenInterfaces := []string{"0.0.0.0"}
	if strings.TrimSpace(s.config.ListenInterfaces) != "" {
		listenInterfaces = strings.Split(strings.Replace(strings.TrimSpace(s.config.ListenInterfaces), " ", "", -1), ",")
	}

	listenInterfacesStrings := make([]string, 0)
	for _, listenInterface := range listenInterfaces {
		listenInterfacesStrings = append(listenInterfacesStrings, listenInterface+":"+listenPorts[rand.Intn(len(listenPorts))])
		if len(listenPorts) > 1 {
			listenInterfacesStrings = append(listenInterfacesStrings, listenInterface+":"+listenPorts[rand.Intn(len(listenPorts))])
		}
	}
	s.PackSettings.SetStr("listen_interfaces", strings.Join(listenInterfacesStrings, ","))

	if strings.TrimSpace(s.config.OutgoingInterfaces) != "" {
		s.PackSettings.SetStr("outgoing_interfaces", strings.Replace(strings.TrimSpace(s.config.OutgoingInterfaces), " ", "", -1))
	}

	log.Info("Starting LSD...")
	s.PackSettings.SetBool("enable_lsd", true)

	if s.config.DisableDHT == false {
		log.Info("Starting DHT...")
		s.PackSettings.SetStr("dht_bootstrap_nodes", strings.Join(dhtBootstrapNodes, ","))
		s.PackSettings.SetBool("enable_dht", true)
	}

	if s.config.DisableUPNP == false {
		log.Info("Starting UPNP...")
		s.PackSettings.SetBool("enable_upnp", true)

		log.Info("Starting NATPMP...")
		s.PackSettings.SetBool("enable_natpmp", true)
	}

	s.Session.GetHandle().ApplySettings(s.PackSettings)
}

func (s *BTService) stopServices() {
	if s.InternalProxy != nil {
		log.Infof("Stopping internal proxy")
		s.InternalProxy.Shutdown(nil)
	}

	// TODO: cleanup these messages after windows hang is fixed
	// Don't need to execute RPC calls when Kodi is closing
	if s.dialogProgressBG != nil {
		log.Infof("Closing existing Dialog")
		s.dialogProgressBG.Close()
	}
	s.dialogProgressBG = nil

	log.Infof("Cleaning up all DialogBG")
	xbmc.DialogProgressBGCleanup()

	log.Infof("Resetting RPC")
	xbmc.ResetRPC()

	log.Info("Stopping LSD...")
	s.PackSettings.SetBool("enable_lsd", false)

	if s.config.DisableDHT == false {
		log.Info("Stopping DHT...")
		s.PackSettings.SetBool("enable_dht", false)
	}

	if s.config.DisableUPNP == false {
		log.Info("Stopping UPNP...")
		s.PackSettings.SetBool("enable_upnp", false)

		log.Info("Stopping NATPMP...")
		s.PackSettings.SetBool("enable_natpmp", false)
	}

	s.Session.GetHandle().ApplySettings(s.PackSettings)
}

// CheckAvailableSpace ...
func (s *BTService) checkAvailableSpace(t *Torrent) bool {
	// For memory storage we don't need to check available space
	if s.config.DownloadStorage != StorageMemory {
		return true
	}

	diskStatus, err := diskusage.DiskUsage(config.Get().DownloadPath)
	if err != nil {
		log.Warningf("Unable to retrieve the free space for %s, continuing anyway...", config.Get().DownloadPath)
		return false
	}

	torrentInfo := t.th.TorrentFile()

	if torrentInfo == nil || torrentInfo.Swigcptr() == 0 {
		log.Warning("Missing torrent info to check available space.")
		return false
	}

	status := t.th.Status(uint(lt.TorrentHandleQueryAccurateDownloadCounters) | uint(lt.TorrentHandleQuerySavePath))
	totalSize := t.ti.TotalSize()
	totalDone := status.GetTotalDone()
	sizeLeft := totalSize - totalDone
	availableSpace := diskStatus.Free
	path := status.GetSavePath()

	log.Infof("Checking for sufficient space on %s...", path)
	log.Infof("Total size of download: %s", humanize.Bytes(uint64(totalSize)))
	log.Infof("All time download: %s", humanize.Bytes(uint64(status.GetAllTimeDownload())))
	log.Infof("Size total done: %s", humanize.Bytes(uint64(totalDone)))
	log.Infof("Size left to download: %s", humanize.Bytes(uint64(sizeLeft)))
	log.Infof("Available space: %s", humanize.Bytes(uint64(availableSpace)))

	if availableSpace < sizeLeft {
		log.Errorf("Unsufficient free space on %s. Has %d, needs %d.", path, diskStatus.Free, sizeLeft)
		xbmc.Notify("Elementum", "LOCALIZE[30207]", config.AddonIcon())

		log.Infof("Pausing torrent %s", t.th.Status(uint(lt.TorrentHandleQueryName)).GetName())
		t.th.AutoManaged(false)
		t.Pause()
		return false
	}

	return true
}

// AddTorrent ...
func (s *BTService) AddTorrent(uri string) (*Torrent, error) {
	log.Infof("Adding torrent from %s", uri)

	if s.config.DownloadStorage != StorageMemory && s.config.DownloadPath == "." {
		log.Warningf("Cannot add torrent since download path is not set")
		xbmc.Notify("Elementum", "LOCALIZE[30113]", config.AddonIcon())
		return nil, fmt.Errorf("Download path empty")
	}

	torrentParams := lt.NewAddTorrentParams()
	defer lt.DeleteAddTorrentParams(torrentParams)

	if s.config.DownloadStorage == StorageMemory {
		torrentParams.SetMemoryStorage(s.GetMemorySize())
	}

	var err error
	var torrentHandle lt.TorrentHandle
	var infoHash string

	if strings.HasPrefix(uri, "magnet:") {
		torrent := NewTorrentFile(uri)

		if torrent.IsMagnet() {
			torrent.Magnet()
			log.Infof("Parsed magnet: %s", torrent.URI)
			if err := torrent.IsValidMagnet(); err == nil {
				torrentParams.SetUrl(torrent.URI)
			} else {
				return nil, err
			}
		} else {
			torrent.Resolve()
		}

		infoHash = torrent.InfoHash
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

		info := lt.NewTorrentInfo(uri)
		defer lt.DeleteTorrentInfo(info)
		torrentParams.SetTorrentInfo(info)

		shaHash := info.InfoHash().ToString()
		infoHash = hex.EncodeToString([]byte(shaHash))
	}

	log.Infof("Setting save path to %s", s.config.DownloadPath)
	torrentParams.SetSavePath(s.config.DownloadPath)

	if s.config.DownloadStorage != StorageMemory {
		log.Infof("Checking for fast resume data in %s.fastresume", infoHash)
		fastResumeFile := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.fastresume", infoHash))
		if _, err := os.Stat(fastResumeFile); err == nil {
			log.Info("Found fast resume data")
			fastResumeData, err := ioutil.ReadFile(fastResumeFile)
			if err != nil {
				return nil, err
			}

			fastResumeVector := lt.NewStdVectorChar()
			defer lt.DeleteStdVectorChar(fastResumeVector)
			for _, c := range fastResumeData {
				fastResumeVector.Add(c)
			}
			torrentParams.SetResumeData(fastResumeVector)
		}
	}

	torrentHandle = s.Session.GetHandle().AddTorrent(torrentParams)

	log.Debugf("Making new torrent item with url = '%s'", uri)
	torrent := NewTorrent(s, torrentHandle, torrentHandle.TorrentFile(), uri)
	if s.config.ConnectionsLimit > 0 {
		torrentHandle.SetMaxConnections(s.config.ConnectionsLimit)
	}

	s.Torrents[torrent.infoHash] = torrent

	// go torrent.SaveMetainfo(s.config.TorrentsPath)
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

func (s *BTService) onStateChanged(stateAlert lt.StateChangedAlert) {
	switch stateAlert.GetState() {
	case lt.TorrentStatusDownloading:
		torrentHandle := stateAlert.GetHandle()
		torrentStatus := torrentHandle.Status(uint(lt.TorrentHandleQueryName))
		shaHash := torrentStatus.GetInfoHash().ToString()
		infoHash := hex.EncodeToString([]byte(shaHash))
		if spaceChecked, exists := s.SpaceChecked[infoHash]; exists {
			if spaceChecked == false {
				if t := s.GetTorrentByHash(infoHash); t != nil {
					s.checkAvailableSpace(t)
					delete(s.SpaceChecked, infoHash)
				}
			}
		}
	}
}

// GetTorrentByHash ...
func (s *BTService) GetTorrentByHash(hash string) *Torrent {
	if t, ok := s.Torrents[hash]; ok {
		return t
	}

	return nil
}

func (s *BTService) saveResumeDataLoop() {
	saveResumeWait := time.NewTicker(time.Duration(s.config.SessionSave) * time.Second)
	closer := s.Closing.Subscribe()
	defer saveResumeWait.Stop()
	defer closer.Close()

	for {
		select {
		case <-closer.Values:
			return
		case <-saveResumeWait.C:
			torrentsVector := s.Session.GetHandle().GetTorrents()
			torrentsVectorSize := int(torrentsVector.Size())

			for i := 0; i < torrentsVectorSize; i++ {
				torrentHandle := torrentsVector.Get(i)
				if torrentHandle.IsValid() == false {
					continue
				}

				status := torrentHandle.Status()
				if status.GetHasMetadata() == false || status.GetNeedSaveResume() == false {
					continue
				}

				torrentHandle.SaveResumeData(1)
			}
		}
	}
}

func (s *BTService) saveResumeDataConsumer() {
	alerts, alertsDone := s.Alerts()
	defer close(alertsDone)

	for {
		select {
		case alert, ok := <-alerts:
			if !ok { // was the alerts channel closed?
				return
			}
			switch alert.Type {
			case lt.MetadataReceivedAlertAlertType:
				metadataAlert := lt.SwigcptrMetadataReceivedAlert(alert.Pointer)
				torrentHandle := metadataAlert.GetHandle()
				torrentStatus := torrentHandle.Status(uint(lt.TorrentHandleQueryName))
				shaHash := torrentStatus.GetInfoHash().ToString()
				infoHash := hex.EncodeToString([]byte(shaHash))
				torrentFileName := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.torrent", infoHash))

				// Save .torrent
				log.Infof("Saving %s...", torrentFileName)
				torrentInfo := torrentHandle.TorrentFile()
				torrentFile := lt.NewCreateTorrent(torrentInfo)
				defer lt.DeleteCreateTorrent(torrentFile)
				torrentContent := torrentFile.Generate()
				bEncodedTorrent := []byte(lt.Bencode(torrentContent))
				ioutil.WriteFile(torrentFileName, bEncodedTorrent, 0644)

			case lt.StateChangedAlertAlertType:
				stateAlert := lt.SwigcptrStateChangedAlert(alert.Pointer)
				s.onStateChanged(stateAlert)

			case lt.SaveResumeDataAlertAlertType:
				bEncoded := []byte(lt.Bencode(alert.Entry))
				b := bytes.NewReader(bEncoded)
				dec := bencode.NewDecoder(b)
				var torrentFile *TorrentFileRaw
				if err := dec.Decode(&torrentFile); err != nil {
					log.Warningf("Resume data corrupted for %s, %d bytes received and failed to decode with: %s, skipping...", alert.Name, len(bEncoded), err.Error())
				} else {
					path := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.fastresume", alert.InfoHash))
					ioutil.WriteFile(path, bEncoded, 0644)
				}
			}
		}
	}
}

func (s *BTService) alertsConsumer() {
	closer := s.Closing.Subscribe()
	defer closer.Close()
	defer s.alertsBroadcaster.Close()

	ltOneSecond := lt.Seconds(ltAlertWaitTime)
	log.Info("Consuming alerts...")
	for {
		select {
		case <-closer.Values:
			log.Info("Closing all alert channels...")

			return
		default:
			if s.Session.GetHandle().WaitForAlert(ltOneSecond).Swigcptr() == 0 {
				continue
			}
			var alerts lt.StdVectorAlerts
			alerts = s.Session.GetHandle().PopAlerts()
			queueSize := alerts.Size()
			var name string
			var infoHash string
			var entry lt.Entry
			for i := 0; i < int(queueSize); i++ {
				ltAlert := alerts.Get(i)
				alertType := ltAlert.Type()
				alertPtr := ltAlert.Swigcptr()
				alertMessage := ltAlert.Message()
				switch alertType {
				case lt.SaveResumeDataAlertAlertType:
					saveResumeData := lt.SwigcptrSaveResumeDataAlert(alertPtr)
					torrentHandle := saveResumeData.GetHandle()
					torrentStatus := torrentHandle.Status(uint(lt.TorrentHandleQuerySavePath) | uint(lt.TorrentHandleQueryName))
					name = torrentStatus.GetName()
					shaHash := torrentStatus.GetInfoHash().ToString()
					infoHash = hex.EncodeToString([]byte(shaHash))
					entry = saveResumeData.ResumeData()
				case lt.ExternalIpAlertAlertType:
					splitMessage := strings.Split(alertMessage, ":")
					splitIP := strings.Split(splitMessage[len(splitMessage)-1], ".")
					alertMessage = strings.Join(splitMessage[:len(splitMessage)-1], ":") + splitIP[0] + ".XX.XX.XX"
				case lt.MetadataReceivedAlertAlertType:
					metadataAlert := lt.SwigcptrMetadataReceivedAlert(alertPtr)
					for _, t := range s.Torrents {
						if t.th != nil && metadataAlert.GetHandle().Equal(t.th) {
							t.onMetadataReceived()
						}
					}
				}

				alert := &Alert{
					Type:     alertType,
					Category: ltAlert.Category(),
					What:     ltAlert.What(),
					Message:  alertMessage,
					Pointer:  alertPtr,
					Name:     name,
					Entry:    entry,
					InfoHash: infoHash,
				}
				s.alertsBroadcaster.Broadcast(alert)
			}
		}
	}
}

// Alerts ...
func (s *BTService) Alerts() (<-chan *Alert, chan<- interface{}) {
	c, done := s.alertsBroadcaster.Listen()
	ac := make(chan *Alert)
	go func() {
		for v := range c {
			ac <- v.(*Alert)
		}
	}()
	return ac, done
}

func (s *BTService) logAlerts() {
	alerts, _ := s.Alerts()
	for alert := range alerts {
		// Skipping Tracker communication, Save_Resume, UDP errors
		// No need to spam logs.
		if alert.Category&int(lt.AlertTrackerNotification) != 0 || alert.Category&int(lt.SaveResumeDataAlertAlertType) != 0 || alert.Category&int(lt.UdpErrorAlertAlertType) != 0 {
			continue
		} else if alert.Category&int(lt.AlertErrorNotification) != 0 {
			log.Errorf("%s: %s", alert.What, alert.Message)
		} else if alert.Category&int(lt.AlertDebugNotification) != 0 {
			log.Debugf("%s: %s", alert.What, alert.Message)
		} else if alert.Category&int(lt.AlertPerformanceWarning) != 0 {
			log.Warningf("%s: %s", alert.What, alert.Message)
		} else {
			log.Noticef("%s: %s", alert.What, alert.Message)
		}
	}
}

func (s *BTService) loadTorrentFiles() {
	// Not loading previous torrents on start
	// Otherwise we can dig out all the memory and halt the device
	if s.config.DownloadStorage == StorageMemory || !s.config.AutoloadTorrents {
		return
	}

	pattern := filepath.Join(s.config.TorrentsPath, "*.torrent")
	files, _ := filepath.Glob(pattern)

	for _, torrentFile := range files {
		log.Infof("Loading torrent file %s", torrentFile)

		torrentParams := lt.NewAddTorrentParams()
		defer lt.DeleteAddTorrentParams(torrentParams)

		t, _ := s.AddTorrent(torrentFile)
		if t != nil {
			i := database.Get().GetBTItem(t.InfoHash())

			if i != nil {
				t.DBItem = i

				for _, p := range i.Files {
					for _, f := range t.files {
						if f.Path == p {
							t.ChosenFiles = append(t.ChosenFiles, f)
							t.DownloadFile(f)
						}
					}
				}
			}

			t.IsInitialized = true
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

			if s.Closing.IsSet() || s.Session == nil || s.Session.GetHandle() == nil {
				return
			}

			var totalDownloadRate float64
			var totalUploadRate float64
			var totalProgress int

			activeTorrents := make([]*activeTorrent, 0)
			torrentsVector := s.Session.GetHandle().GetTorrents()
			torrentsVectorSize := int(torrentsVector.Size())

			for i := 0; i < torrentsVectorSize; i++ {
				torrentHandle := torrentsVector.Get(i)
				if torrentHandle.IsValid() == false {
					continue
				}

				torrentStatus := torrentHandle.Status(uint(lt.TorrentHandleQueryName))
				status := StatusStrings[int(torrentStatus.GetState())]
				isPaused := torrentStatus.GetPaused()
				if torrentStatus.GetHasMetadata() == false || s.Session.GetHandle().IsPaused() {
					continue
				}

				downloadRate := float64(torrentStatus.GetDownloadRate()) / 1024
				uploadRate := float64(torrentStatus.GetUploadRate()) / 1024
				totalDownloadRate += downloadRate
				totalUploadRate += uploadRate

				torrentName := torrentStatus.GetName()
				progress := int(float64(torrentStatus.GetProgress()) * 100)

				if progress < 100 && !isPaused {
					activeTorrents = append(activeTorrents, &activeTorrent{
						torrentName:  torrentName,
						downloadRate: downloadRate,
						uploadRate:   uploadRate,
						progress:     progress,
					})
					totalProgress += progress
					continue
				}

				seedingTime := torrentStatus.GetSeedingTime()
				finishedTime := torrentStatus.GetFinishedTime()
				if progress == 100 && seedingTime == 0 {
					seedingTime = finishedTime
				}

				if config.Get().DownloadStorage != StorageMemory && s.config.SeedTimeLimit > 0 {
					if seedingTime >= s.config.SeedTimeLimit {
						if !isPaused {
							log.Warningf("Seeding time limit reached, pausing %s", torrentName)
							torrentHandle.AutoManaged(false)
							torrentHandle.Pause(1)
							isPaused = true
						}
						status = "Seeded"
					}
				}
				if config.Get().DownloadStorage != StorageMemory && s.config.SeedTimeRatioLimit > 0 {
					timeRatio := 0
					downloadTime := torrentStatus.GetActiveTime() - seedingTime
					if downloadTime > 1 {
						timeRatio = seedingTime * 100 / downloadTime
					}
					if timeRatio >= s.config.SeedTimeRatioLimit {
						if !isPaused {
							log.Warningf("Seeding time ratio reached, pausing %s", torrentName)
							torrentHandle.AutoManaged(false)
							torrentHandle.Pause(1)
							isPaused = true
						}
						status = "Seeded"
					}
				}
				if config.Get().DownloadStorage != StorageMemory && s.config.ShareRatioLimit > 0 {
					ratio := int64(0)
					allTimeDownload := torrentStatus.GetAllTimeDownload()
					if allTimeDownload > 0 {
						ratio = torrentStatus.GetAllTimeUpload() * 100 / allTimeDownload
					}
					if ratio >= int64(s.config.ShareRatioLimit) {
						if !isPaused {
							log.Warningf("Share ratio reached, pausing %s", torrentName)
							torrentHandle.AutoManaged(false)
							torrentHandle.Pause(1)
						}
						status = "Seeded"
					}
				}

				shaHash := torrentHandle.Status().GetInfoHash().ToString()
				infoHash := hex.EncodeToString([]byte(shaHash))

				if s.MarkedToMove != "" && infoHash == s.MarkedToMove {
					s.MarkedToMove = ""
					status = "Seeded"
				}

				//
				// Handle moving completed downloads
				//
				if !s.config.CompletedMove || status != "Seeded" || s.anyPlayerIsPlaying() {
					continue
				}
				if xbmc.PlayerIsPlaying() {
					continue
				}

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
					s.RemoveTorrent(s.GetTorrentByHash(infoHash), false)

					// Delete leftover .parts file if any
					partsFile := filepath.Join(config.Get().DownloadPath, fmt.Sprintf(".%s.parts", infoHash))
					os.Remove(partsFile)

					// Delete fast resume data
					fastResumeFile := filepath.Join(s.config.TorrentsPath, fmt.Sprintf("%s.fastresume", infoHash))
					if _, err := os.Stat(fastResumeFile); err == nil {
						log.Info("Deleting fast resume data at", fastResumeFile)
						if err := os.Remove(fastResumeFile); err != nil {
							log.Error(err)
							return err
						}
					}

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

					torrentInfo := torrentHandle.TorrentFile()
					for _, i := range item.Files {
						filePath := torrentInfo.Files().FilePath(i)
						fileName := filepath.Base(filePath)

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
					}
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
	settings := s.PackSettings
	settings.SetInt("download_rate_limit", i)

	s.Session.GetHandle().ApplySettings(settings)
}

// SetUploadLimit ...
func (s *BTService) SetUploadLimit(i int) {
	settings := s.PackSettings

	settings.SetInt("upload_rate_limit", i)
	s.Session.GetHandle().ApplySettings(settings)
}

// RestoreLimits ...
func (s *BTService) RestoreLimits() {
	if s.config.DownloadRateLimit > 0 {
		s.SetDownloadLimit(s.config.DownloadRateLimit)
		log.Infof("Rate limiting download to %s", humanize.Bytes(uint64(s.config.DownloadRateLimit)))
	} else {
		s.SetDownloadLimit(0)
	}

	if s.config.UploadRateLimit > 0 {
		s.SetUploadLimit(s.config.UploadRateLimit)
		log.Infof("Rate limiting upload to %s", humanize.Bytes(uint64(s.config.UploadRateLimit)))
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
	if b < EndBufferSize {
		return EndBufferSize
	}
	return b
}

// GetMemorySize ...
func (s *BTService) GetMemorySize() int64 {
	return int64(config.Get().MemorySize)
}

// GetStorageType ...
func (s *BTService) GetStorageType() int {
	return s.config.DownloadStorage
}

// PlayerStop ...
func (s *BTService) PlayerStop() {
	log.Debugf("PlayerStop")
}

// PlayerSeek ...
func (s *BTService) PlayerSeek() {
	log.Debugf("PlayerSeek")
}

// ClientInfo ...
func (s *BTService) ClientInfo(w io.Writer) {
	// TODO: Print any client info here
	// s.Client.WriteStatus(w)
	// s.ClientConfig.ConnTracker.PrintStatus(w)
}

// AttachPlayer adds Player instance to service
func (s *BTService) AttachPlayer(p *BTPlayer) {
	if p == nil || p.t == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Players[p.t.InfoHash()]; ok {
		return
	}

	s.Players[p.t.InfoHash()] = p
}

// DetachPlayer removes Player instance
func (s *BTService) DetachPlayer(p *BTPlayer) {
	if p == nil || p.t == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.Players, p.t.InfoHash())
}

// GetPlayer searches for player with desired TMDB id
func (s *BTService) GetPlayer(kodiID int, tmdbID int) *BTPlayer {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.Players {
		if p == nil || p.t == nil {
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
		if p == nil || p.t == nil {
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
		if p == nil || p.t == nil {
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

// GetMemoryStats returns total and free memory sizes for this OS
func (s *BTService) GetMemoryStats() (int64, int64) {
	v, _ := mem.VirtualMemory()
	return int64(v.Total), int64(v.Free)
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
