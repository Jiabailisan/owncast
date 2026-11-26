package stream

import (
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/storage"
)

// setupStorage picks an HLS storage backend based on the S3 config and
// wires it into the HLS handler.
func (s *Service) setupStorage() error {
	s3Config := s.configRepository.GetS3Config()

	var backend models.StorageProvider
	if s3Config.Enabled {
		backend = storage.NewS3Storage(s.configRepository)
	} else {
		backend = storage.NewLocalStorage(s.configRepository)
	}

	if err := backend.Setup(); err != nil {
		return err
	}

	s.lifecycleMu.Lock()
	s.storage = backend
	s.handler.Storage = backend
	s.lifecycleMu.Unlock()

	return nil
}
