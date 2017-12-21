package util

import (
	"time"
)

// NowInt ...
func NowInt() int {
	return int(time.Now().Unix())
}

// NowPlusSecondsInt ..
func NowPlusSecondsInt(seconds int) int {
	return int(time.Now().Add(time.Duration(seconds) * time.Second).Unix())
}
