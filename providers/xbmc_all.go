// +build !arm

package providers

import "time"

func providerTimeout() time.Duration {
	return 30 * time.Second
}
