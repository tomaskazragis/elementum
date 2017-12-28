package util

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
)

// Version ...
var Version string

// UserAgent ...
func UserAgent() string {
	return fmt.Sprintf("Elementum/%s", Version[1:len(Version)-1])
}

// PeerID return default PeerID
func PeerID() string {
	return "-GT0001-"
}

// PeerIDRandom generates random peer id
func PeerIDRandom(peer string) string {
	return peer + getToken(20-len(peer))
}

func getToken(length int) string {
	randomBytes := make([]byte, 32)
	_, err := rand.Read(randomBytes)
	if err != nil {
		panic(err)
	}
	return base32.StdEncoding.EncodeToString(randomBytes)[:length]
}
