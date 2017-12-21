package util

import (
	"net/http"
	"strconv"
	"time"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("ratelimiter")

// RateLimiter ...
type RateLimiter struct {
	rateTicker   *time.Ticker
	rateLimiter  chan bool
	parallelChan chan bool
	burstRate    int
	coolDown     int
}

// NewRateLimiter ...
func NewRateLimiter(burstRate int, burstTimeSpan time.Duration, parallelCount int) *RateLimiter {
	limiter := &RateLimiter{
		rateTicker:   time.NewTicker(burstTimeSpan),
		rateLimiter:  make(chan bool, burstRate),
		parallelChan: make(chan bool, parallelCount),
		burstRate:    burstRate,
	}
	go func() {
		for _ = range limiter.rateTicker.C {
			// log.Debugf("Rate limiter ticking (%d / %d)...", burstRate, parallelCount)
			if limiter.coolDown == 0 {
				limiter.Reset()
				// log.Debugf("Resetting (%d / %d)...", burstRate, parallelCount)
			} else {
				time.Sleep(time.Duration(limiter.coolDown) * time.Second)
				limiter.coolDown = 0
				// log.Debugf("Cooldown tick after %ds (%d / %d)...", limiter.coolDown, burstRate, parallelCount)
			}
		}
	}()
	return limiter
}

// Enter ...
func (rl *RateLimiter) Enter() {
	rl.parallelChan <- true
	rl.rateLimiter <- true
}

// Leave ...
func (rl *RateLimiter) Leave() {
	<-rl.parallelChan
}

// Call ...
func (rl *RateLimiter) Call(f func()) {
	rl.Enter()
	defer rl.Leave()
	if rl.coolDown > 0 {
		// Already cooling down, wait up
		time.Sleep(time.Duration(rl.coolDown) * time.Second)
	}
	f()
}

// Reset ...
func (rl *RateLimiter) Reset() {
outer:
	for i := 0; i < rl.burstRate; i++ {
		select {
		case <-rl.rateLimiter:
		default:
			break outer
		}
	}
}

// CoolDown ...
func (rl *RateLimiter) CoolDown(headers http.Header) {
	if len(headers) > 0 {
		if retryAfter, exists := headers["Retry-After"]; exists {
			if retryAfter != nil {
				coolDown, err := strconv.Atoi(retryAfter[0])
				if err == nil && coolDown > 0 {
					coolDown = rl.coolDown + coolDown + 1
					log.Debugf("Cooling down for %d second(s)...", coolDown)
					rl.coolDown = coolDown
					return
				}
			}
		}
	}
	defaultCoolDown := rl.coolDown + 3
	log.Debugf("No Retry-After header found, cooling down for %d seconds...", defaultCoolDown)
	rl.coolDown = defaultCoolDown
}

// Close ...
func (rl *RateLimiter) Close() {
	rl.rateTicker.Stop()
}
