package util

import (
	"time"
	"strconv"
	"net/http"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("ratelimiter")

type RateLimiter struct {
	rateTicker   *time.Ticker
	rateLimiter  chan bool
	parallelChan chan bool
	coolDown     *time.Ticker
	waiting      int

	BurstRate     int
	BurstTimeSpan time.Duration
	ParallelCount int
}

func NewRateLimiter(burstRate int, burstTimeSpan time.Duration, parallelCount int) *RateLimiter {
	limiter := &RateLimiter{
		rateTicker:   time.NewTicker(burstTimeSpan),
		rateLimiter:  make(chan bool, burstRate),
		parallelChan: make(chan bool, parallelCount),
		coolDown:     time.NewTicker(time.Duration(10)),
		waiting:      0,

		BurstRate:     burstRate,
		BurstTimeSpan: burstTimeSpan,
		ParallelCount: parallelCount,
	}
	limiter.coolDown.Stop()
	go func() {
		for _ = range limiter.rateTicker.C {
			limiter.Reset()
		}
	}()
	go func() {
		for _ = range limiter.coolDown.C {
			limiter.waiting = 0
			limiter.coolDown.Stop()
			limiter.rateTicker = time.NewTicker(burstTimeSpan)
		}
	}()
	return limiter
}

func (rl *RateLimiter) Enter() {
	rl.parallelChan <- true
	rl.rateLimiter <- true
}

func (rl *RateLimiter) Leave() {
	<-rl.parallelChan
}

func (rl *RateLimiter) Call(f func()) {
	rl.Enter()
	defer rl.Leave()
	if rl.waiting > 0 {
		time.Sleep(time.Duration(rl.waiting))
	}
	f()
}

func (rl *RateLimiter) Reset() {
outer:
	for i := 0; i < rl.BurstRate; i++ {
		select {
		case <-rl.rateLimiter:
		default:
			break outer
		}
	}
}

func (rl *RateLimiter) Wait(coolDown time.Duration) {
	rl.rateTicker.Stop()
	rl.coolDown = time.NewTicker(coolDown)
}

func (rl *RateLimiter) CoolDown(headers http.Header) {
	if len(headers) > 0 {
		if retryAfter, exists := headers["Retry-After"]; exists {
			if retryAfter != nil {
				coolDown, err := strconv.Atoi(retryAfter[0])
				if err == nil {
					log.Debugf("Cooling down for %d second(s)...", coolDown + 1)
					rl.waiting = coolDown
					rl.Wait(time.Duration(coolDown + 1))
					return
				}
			}
		}
	}
	rl.Wait(time.Duration(3))
}

func (rl *RateLimiter) Close() {
	rl.rateTicker.Stop()
	rl.coolDown.Stop()
}
