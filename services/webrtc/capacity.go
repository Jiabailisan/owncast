package webrtc

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// CapacitySnapshot is the public shape of the capacity gate for the admin API
// and the Live Stream Status page.
type CapacitySnapshot struct {
	Used            int    `json:"used"`
	Limit           int    `json:"limit"`
	Reserved        int    `json:"reserved"`
	MaxViewersPerIP int    `json:"maxViewersPerIP"`
	RejectedTotal   uint64 `json:"rejectedTotal"`
}

type reservation struct {
	id        string
	ip        string
	expiresAt time.Time
}

// capacityGate enforces the global and per-IP viewer limits. A slot is first
// held as a short-lived reservation while the SDP offer/answer exchange is in
// flight (so abandoned offers cannot exhaust the limit), then converted into an
// active session slot. All methods are safe for concurrent use.
type capacityGate struct {
	mu      sync.Mutex
	max     int
	perIP   int
	ttl     time.Duration
	active  map[string]string // session id -> client ip
	holding map[string]reservation
	now     func() time.Time
	reject  uint64
}

func newCapacityGate(maxViewers, perIPMax int, ttl time.Duration, now func() time.Time) *capacityGate {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = 10 * time.Second
	}

	return &capacityGate{
		max:     maxViewers,
		perIP:   perIPMax,
		ttl:     ttl,
		active:  map[string]string{},
		holding: map[string]reservation{},
		now:     now,
	}
}

func (c *capacityGate) setLimits(maxViewers, perIPMax int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.max = maxViewers
	c.perIP = perIPMax
}

// reserve allocates a slot for the duration of the signaling exchange.
func (c *capacityGate) reserve(ip string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	c.expireLocked(now)

	if c.max > 0 && len(c.active)+len(c.holding) >= c.max {
		c.reject++

		return "", ErrCapacity
	}
	if c.perIP > 0 && c.countByIPLocked(ip)+1 > c.perIP {
		c.reject++

		return "", ErrPerIPCapacity
	}

	id, err := newReservationID()
	if err != nil {
		return "", err
	}
	c.holding[id] = reservation{id: id, ip: ip, expiresAt: now.Add(c.ttl)}

	return id, nil
}

// activate converts a reservation into an active session slot.
func (c *capacityGate) activate(reservationID, sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	res, ok := c.holding[reservationID]
	if !ok {
		return ErrSessionNotFound
	}
	delete(c.holding, reservationID)
	c.active[sessionID] = res.ip

	return nil
}

func (c *capacityGate) releaseReservation(reservationID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.holding, reservationID)
}

func (c *capacityGate) releaseSession(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.active, sessionID)
}

func (c *capacityGate) expire(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.expireLocked(now)
}

func (c *capacityGate) expireLocked(now time.Time) int {
	expired := 0
	for id, res := range c.holding {
		if !now.Before(res.expiresAt) {
			delete(c.holding, id)
			expired++
		}
	}

	return expired
}

func (c *capacityGate) countByIPLocked(ip string) int {
	count := 0
	for _, activeIP := range c.active {
		if activeIP == ip {
			count++
		}
	}
	for _, res := range c.holding {
		if res.ip == ip {
			count++
		}
	}

	return count
}

func (c *capacityGate) snapshot() CapacitySnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	return CapacitySnapshot{
		Used:            len(c.active),
		Limit:           c.max,
		Reserved:        len(c.holding),
		MaxViewersPerIP: c.perIP,
		RejectedTotal:   c.reject,
	}
}

func newReservationID() (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf[:]), nil
}
