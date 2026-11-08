package webrtc

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapacityGlobalLimitAndRelease(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	gate := newCapacityGate(2, 0, 10*time.Second, clock)

	first, err := gate.reserve("203.0.113.1")
	require.NoError(t, err)
	second, err := gate.reserve("203.0.113.2")
	require.NoError(t, err)

	_, err = gate.reserve("203.0.113.3")
	require.ErrorIs(t, err, ErrCapacity)

	require.NoError(t, gate.activate(first, "session-1"))
	require.NoError(t, gate.activate(second, "session-2"))

	snapshot := gate.snapshot()
	assert.Equal(t, 2, snapshot.Used)
	assert.Equal(t, 0, snapshot.Reserved)

	gate.releaseSession("session-1")
	_, err = gate.reserve("203.0.113.3")
	require.NoError(t, err)
}

func TestCapacityPerIPLimit(t *testing.T) {
	gate := newCapacityGate(10, 1, 10*time.Second, time.Now)

	first, err := gate.reserve("198.51.100.7")
	require.NoError(t, err)
	require.NoError(t, gate.activate(first, "session-1"))

	_, err = gate.reserve("198.51.100.7")
	require.ErrorIs(t, err, ErrPerIPCapacity)

	// A different IP is unaffected.
	_, err = gate.reserve("198.51.100.8")
	require.NoError(t, err)
}

func TestCapacityReservationExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	clock := func() time.Time { return now }
	gate := newCapacityGate(1, 0, 10*time.Second, clock)

	_, err := gate.reserve("203.0.113.9")
	require.NoError(t, err)
	_, err = gate.reserve("203.0.113.10")
	require.ErrorIs(t, err, ErrCapacity)

	// Past the reservation TTL the abandoned offer no longer holds a slot.
	now = now.Add(11 * time.Second)
	_, err = gate.reserve("203.0.113.10")
	require.NoError(t, err)
}

func TestCapacityReleaseReservation(t *testing.T) {
	gate := newCapacityGate(1, 0, 10*time.Second, time.Now)

	reservation, err := gate.reserve("203.0.113.11")
	require.NoError(t, err)
	gate.releaseReservation(reservation)

	_, err = gate.reserve("203.0.113.11")
	require.NoError(t, err)
}

func TestCapacityUnlimited(t *testing.T) {
	gate := newCapacityGate(0, 0, 10*time.Second, time.Now)
	for i := 0; i < 50; i++ {
		_, err := gate.reserve("203.0.113.12")
		require.NoError(t, err)
	}
	assert.Equal(t, 50, gate.snapshot().Reserved)
}

func TestCapacityConcurrentReserve(t *testing.T) {
	gate := newCapacityGate(5, 0, time.Minute, time.Now)

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		granted int
	)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := gate.reserve("203.0.113.13"); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 5, granted)
}

func TestCapacityActivateUnknownReservation(t *testing.T) {
	gate := newCapacityGate(1, 0, time.Minute, time.Now)
	require.ErrorIs(t, gate.activate("does-not-exist", "session"), ErrSessionNotFound)
}
