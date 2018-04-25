package util

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/elgatito/elementum/config"
)

// LocalIP ...
func LocalIP() (net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 != nil && (v4[0] == 192 || v4[0] == 172 || v4[0] == 10) {
				return v4, nil
			}
		}
	}
	return nil, errors.New("cannot find local IP address")
}

// GetHTTPHost ...
func GetHTTPHost() string {
	hostname := "localhost"
	// if localIP, err := LocalIP(); err == nil {
	// 	hostname = localIP.String()
	// }
	return fmt.Sprintf("http://%s:%d", hostname, config.ListenPort)
}

// GetListenAddr parsing configuration setted for interfaces and port range
// and returning IP, IPv6, and port
func GetListenAddr(confAutoIP bool, confAutoPort bool, confInterfaces string, confPortMin int, confPortMax int) (listenIP, listenIPv6 string, listenPort int, disableIPv6 bool) {
	if confAutoIP {
		confInterfaces = ""
	}
	if confAutoPort {
		confPortMin = 0
		confPortMax = 0
	}

	listenIPs := []string{}
	listenIPv6s := []string{}
	if strings.TrimSpace(confInterfaces) != "" {
		for _, iName := range strings.Split(strings.Replace(strings.TrimSpace(confInterfaces), " ", "", -1), ",") {
			// Check whether value in interfaces string is already an IP value
			if addr := net.ParseIP(iName); addr != nil {
				listenIPs = append(listenIPs, addr.To4().String())
				continue
			}

			i, err := net.InterfaceByName(iName)
			// Maybe we need to raise an error that interface not available?
			if err != nil {
				continue
			}

			if addrs, aErr := i.Addrs(); aErr == nil && len(addrs) > 0 {
				for _, addr := range addrs {
					var ip net.IP
					switch v := addr.(type) {
					case *net.IPNet:
						ip = v.IP
					case *net.IPAddr:
						ip = v.IP
					}

					v6 := ip.To16()
					v4 := ip.To4()

					if v6 != nil && v4 == nil {
						listenIPv6s = append(listenIPv6s, v6.String()+"%"+iName)
					}
					if v4 != nil {
						listenIPs = append(listenIPs, v4.String())
					}
				}
			}
		}
	}
	if len(listenIPs) == 0 {
		listenIPs = append(listenIPs, "")
	}
	if len(listenIPv6s) == 0 {
		listenIPv6s = append(listenIPv6s, "")
	}

loopPorts:
	for p := confPortMax; p >= confPortMin; p-- {
		for _, ip := range listenIPs {
			addr := ip + ":" + strconv.Itoa(p)
			if !testPortUsed("tcp", addr) && !testPortUsed("udp", ":::"+strconv.Itoa(p)) {
				listenIP = ip
				listenPort = p
				break loopPorts
			}
		}
	}

	if len(listenIPv6s) != 0 {
		for _, ip := range listenIPv6s {
			addr := ip + ":" + strconv.Itoa(listenPort)
			if !testPortUsed("tcp6", addr) {
				listenIPv6 = ip
				break
			}
		}
	}

	if (listenIP != "" && listenIPv6 == "") || testPortUsed("tcp6", listenIPv6+":"+strconv.Itoa(listenPort)) {
		disableIPv6 = true
	}

	return
}

// testPortUsed tries to do simple net connection to detect if it's available.
// Not using net.Listen, because it does not always really Close the socket
// (see socket reuse settings)
func testPortUsed(network string, addr string) bool {
	conn, err := net.DialTimeout(network, addr, 100*time.Millisecond)
	if conn != nil && err == nil {
		conn.Close()
		return true
	}
	return false
}
