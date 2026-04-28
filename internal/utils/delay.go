package utils

import (
	"math/rand"
	"time"
)

// RandInt returns a random int in [min, max]
func RandInt(min, max int) int {
	if min >= max {
		return min
	}
	return min + rand.Intn(max-min+1)
}

// Sleep sleeps for a random duration between minMs and maxMs milliseconds
func Sleep(minMs, maxMs int) {
	if minMs > maxMs {
		minMs, maxMs = maxMs, minMs
	}
	ms := RandInt(minMs, maxMs)
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

// SleepSeconds sleeps for a random duration between min and max seconds
func SleepSeconds(min, max int) {
	s := RandInt(min, max)
	time.Sleep(time.Duration(s) * time.Second)
}
