package metrics

import (
	"time"

	"github.com/nakabonne/tstorage"
	log "github.com/sirupsen/logrus"
)

func (s *Service) getStorage() tstorage.Storage {
	s.storageMu.Lock()
	defer s.storageMu.Unlock()
	return s.storage
}

func (s *Service) startViewerCollectionMetrics() {
	st, err := tstorage.NewStorage(
		tstorage.WithTimestampPrecision(tstorage.Seconds),
		tstorage.WithDataPath("./data/metrics"),
	)
	if err != nil {
		log.Errorln("unable to open metrics time-series storage:", err)
		return
	}

	s.storageMu.Lock()
	s.storage = st
	s.storageMu.Unlock()

	defer func() {
		s.storageMu.Lock()
		if s.storage == st {
			s.storage = nil
			s.storageMu.Unlock()
			_ = st.Close()
		} else {
			s.storageMu.Unlock()
		}
	}()

	s.collectViewerCount()

	ticker := time.NewTicker(viewerMetricsPollingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.collectViewerCount()
			s.collectChatClientCount()
		case <-s.stopCh:
			return
		}
	}
}

func (s *Service) collectViewerCount() {
	// Don't collect metrics for viewers if there's no stream active.
	status := s.stream.GetStatus()
	if !status.Online {
		s.activeViewerCount.Set(0)
		return
	}

	count := status.ViewerCount

	// Save active viewer count to our Prometheus collector.
	s.activeViewerCount.Set(float64(count))

	st := s.getStorage()
	if st == nil {
		return
	}
	// Insert active viewer count into our on-disk time series storage.
	if err := st.InsertRows([]tstorage.Row{
		{
			Metric:    activeViewerCountKey,
			DataPoint: tstorage.DataPoint{Timestamp: time.Now().Unix(), Value: float64(count)},
		},
	}); err != nil {
		log.Errorln(err)
	}
}

func (s *Service) collectChatClientCount() {
	count := len(s.chat.GetClients())
	s.activeChatClientCount.Set(float64(count))

	// Total message count
	cmc := s.chatMessageRepository.GetMessagesCount()
	// Insert message count into Prometheus collector.
	s.currentChatMessageCount.Set(float64(cmc))

	// Total user count
	uc := s.userRepository.GetUsersCount()
	// Insert user count into Prometheus collector.
	s.chatUserCount.Set(float64(uc))

	st := s.getStorage()
	if st == nil {
		return
	}
	// Insert active chat user count into our on-disk time series storage.
	if err := st.InsertRows([]tstorage.Row{
		{
			Metric:    activeChatClientCountKey,
			DataPoint: tstorage.DataPoint{Timestamp: time.Now().Unix(), Value: float64(count)},
		},
	}); err != nil {
		log.Errorln(err)
	}
}

// GetViewersOverTime will return a window of viewer counts over time.
func (s *Service) GetViewersOverTime(start, end time.Time) []TimestampedValue {
	st := s.getStorage()
	if st == nil {
		return []TimestampedValue{}
	}
	p, err := st.Select(activeViewerCountKey, nil, start.Unix(), end.Unix())
	if err != nil && err != tstorage.ErrNoDataPoints {
		log.Errorln(err)
	}
	datapoints := makeTimestampedValuesFromDatapoints(p)

	return datapoints
}

// GetChatClientCountOverTime will return a window of connected chat clients over time.
func (s *Service) GetChatClientCountOverTime(start, end time.Time) []TimestampedValue {
	st := s.getStorage()
	if st == nil {
		return []TimestampedValue{}
	}
	p, err := st.Select(activeChatClientCountKey, nil, start.Unix(), end.Unix())
	if err != nil && err != tstorage.ErrNoDataPoints {
		log.Errorln(err)
	}
	datapoints := makeTimestampedValuesFromDatapoints(p)

	return datapoints
}
