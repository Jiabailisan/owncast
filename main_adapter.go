package main

import (
	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/services/stream"
	"github.com/owncast/owncast/services/webrtc"
)

// webRTCStreamAdapter adapts *webrtc.Service to the narrow stream.WebRTCProvider
// contract, keeping the Pion implementation out of the stream package.
type webRTCStreamAdapter struct {
	service *webrtc.Service
}

var _ stream.WebRTCProvider = webRTCStreamAdapter{}

// SetVariants implements stream.WebRTCProvider.
func (a webRTCStreamAdapter) SetVariants(variants []models.StreamOutputVariant) error {
	return a.service.SetVariants(variants)
}

// OpenMediaLeg implements stream.WebRTCProvider.
func (a webRTCStreamAdapter) OpenMediaLeg(variantIndex int) (stream.WebRTCMediaLeg, error) {
	leg, err := a.service.OpenMediaLeg(variantIndex)
	if err != nil {
		return nil, err
	}
	return leg, nil
}

// CloseMediaLeg implements stream.WebRTCProvider.
func (a webRTCStreamAdapter) CloseMediaLeg(variantIndex int) error {
	return a.service.CloseMediaLeg(variantIndex)
}
