package util

import (
	"context"
	// "crypto/sha1"
	"crypto/tls"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elgatito/elementum/config"
)

// type dnsCacheItem struct {
//   mu sync.Mutex
//   ip string
// }

var (
	dnsCacheResults sync.Map
	dnsCacheLocks   sync.Map

	dialer = &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 10 * time.Second,
		DualStack: true,
	}

	// Transport reflects transporting routines for default http.client
	Transport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if config.Get().UsePublicDNS {
				now := time.Now()
				addrs := strings.Split(addr, ":")
				if len(addrs) == 2 && len(addrs[0]) > 2 && strings.Index(addrs[0], ".") > -1 {
					ipTest := net.ParseIP(addrs[0])
					if ipTest == nil {
						ip := resolveAddr(addrs[0])
						log.Debugf("Resolved %s to %s in %s", addrs[0], ip, time.Since(now))
						if ip != "" {
							addr = ip + ":" + addrs[1]
						}
					}
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}

	// HTTPClient used to override default http.client
	HTTPClient = &http.Client{
		Transport: Transport,
		Timeout:   10 * time.Second,
	}
)

// This is very dump solution.
// We have a sync.Map with results for resolving IPs
// and a sync.Map with mutexes for each map.
// Mutexes are needed because torrent files are resolved concurrently and so
// DNS queries run concurrently as well, thus DNS hosts can ban for
// doing so many queries. So we wait until first one is finished.
// Possibly need to cleanup saved IPs after some time.
// Each request is going through this workflow:
// Check saved -> Query Google/Quad9 -> Check saved -> Query Opennic -> Save
func resolveAddr(host string) (ip string) {
	if cached, ok := dnsCacheResults.Load(host); ok {
		return cached.(string)
	}

	var mu *sync.Mutex
	if m, ok := dnsCacheLocks.Load(host); ok {
		mu = m.(*sync.Mutex)
	} else {
		mu = &sync.Mutex{}
		dnsCacheLocks.Store(host, mu)
	}

	mu.Lock()

	defer func() {
		mu.Unlock()
		if strings.HasPrefix(ip, "127.") {
			return
		}

		dnsCacheResults.Store(host, ip)
	}()

	if cached, ok := dnsCacheResults.Load(host); ok {
		return cached.(string)
	}

	ips, err := config.ResolverPublic.LookupHost(host)
	if err == nil && len(ips) > 0 {
		ip = ips[0].String()
		return
	}

	if cached, ok := dnsCacheResults.Load(host); ok {
		return cached.(string)
	}

	ips, err = config.ResolverOpennic.LookupHost(host)
	if err == nil && len(ips) > 0 {
		ip = ips[0].String()
		return
	}

	return
}
