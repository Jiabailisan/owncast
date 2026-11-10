package handlers

import (
	"sync"
	"time"

	"github.com/owncast/owncast/models"
)

// hlsHotCache memoises the configuration lookups the HLS hot path needs. A
// segment request used to decode gob values under the datastore's global lock
// (S3 config, latency level) for values that only change when an admin saves
// them. They are refreshed at most once per second, far below the time an
// operator could notice a change.
type hlsHotCache struct {
	mu        sync.Mutex
	refreshed time.Time

	s3Enabled     bool
	segmentSecond int
}

var hlsCache hlsHotCache

const hlsCacheTTL = time.Second

type hlsConfigSource interface {
	GetS3Config() models.S3
	GetStreamLatencyLevel() models.LatencyLevel
}

// snapshot refreshes the cached values when they are older than the TTL and
// returns them. The lock is held only for the refresh, which happens at most
// once per second.
func (c *hlsHotCache) snapshot(source hlsConfigSource) (s3Enabled bool, segmentSecond int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.refreshed) >= hlsCacheTTL {
		c.s3Enabled = source.GetS3Config().Enabled
		c.segmentSecond = source.GetStreamLatencyLevel().SecondsPerSegment
		c.refreshed = time.Now()
	}

	return c.s3Enabled, c.segmentSecond
}

// cachedS3Enabled reports whether segments are served by external storage.
func (h *Handlers) cachedS3Enabled() bool {
	s3, _ := hlsCache.snapshot(h.configRepository)

	return s3
}

// cachedSegmentSeconds is the configured HLS segment duration, used to turn a
// segment download time into a bitrate estimate.
func (h *Handlers) cachedSegmentSeconds() int {
	_, seconds := hlsCache.snapshot(h.configRepository)
	if seconds <= 0 {
		return 2
	}

	return seconds
}
