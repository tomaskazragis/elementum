package util

import (
	"fmt"
)

var Version string

func UserAgent() string {
	return fmt.Sprintf("Elementum/%s", Version[1:len(Version) - 1])
}
