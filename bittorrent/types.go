package bittorrent

const (
	movieType   = "movie"
	showType    = "show"
	episodeType = "episode"
)

const (
	// StorageFile ...
	StorageFile int = iota
	// StorageMemory ...
	StorageMemory
)

const (
	// StatusQueued ...
	StatusQueued = iota
	// StatusChecking ...
	StatusChecking
	// StatusFinding ...
	StatusFinding
	// StatusPaused ...
	StatusPaused
	// StatusBuffering ...
	StatusBuffering
	// StatusDownloading ...
	StatusDownloading
	// StatusFinished ...
	StatusFinished
	// StatusSeeding ...
	StatusSeeding
	// StatusAllocating ...
	StatusAllocating
	// StatusStalled ...
	StatusStalled
)

// StatusStrings ...
var StatusStrings = []string{
	"Queued",
	"Checking",
	"Finding",
	"Buffering",
	"Finished",
	"Seeding",
	"Allocating",
	"Stalled",
}

const (
	// Remove ...
	Remove = iota
	// Active ...
	Active
)

const (
	ipToSDefault     = iota
	ipToSLowDelay    = 1 << iota
	ipToSReliability = 1 << iota
	ipToSThroughput  = 1 << iota
	ipToSLowCost     = 1 << iota
)

var dhtBootstrapNodes = []string{
	"router.bittorrent.com",
	"router.utorrent.com",
	"dht.transmissionbt.com",
	"dht.aelitis.com", // Vuze
}

// DefaultTrackers ...
var DefaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://tracker.coppersurfer.tk:6969/announce",
	"udp://tracker.leechers-paradise.org:6969/announce",
	"udp://tracker.openbittorrent.com:80/announce",
	"udp://public.popcorn-tracker.org:6969/announce",
	"udp://explodie.org:6969",
}

const (
	ltAlertWaitTime = 1 // 1 second
)
