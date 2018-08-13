// +build arm

package providers

import (
	"runtime"
	"time"
)

func providerTimeout() time.Duration {
	if runtime.NumCPU() == 1 {
		return 40 * time.Second
	}
	return 30 * time.Second
}
