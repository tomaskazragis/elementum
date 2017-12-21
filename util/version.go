package util

import (
	"fmt"
)

// Version ...
var Version string

// UserAgent ...
func UserAgent() string {
	return fmt.Sprintf("Elementum/%s", Version[1:len(Version)-1])
}
