package webrtc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestH264NALTypesSingleNAL(t *testing.T) {
	assert.Equal(t, []byte{nalTypeSPS}, h264NALTypes([]byte{0x67, 0x42, 0x00, 0x1f}))
	assert.Equal(t, []byte{nalTypeIDR}, h264NALTypes([]byte{0x65, 0x88}))
	assert.Equal(t, []byte{nalTypePPS}, h264NALTypes([]byte{0x68, 0xce}))
}

func TestH264NALTypesSTAPA(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x00, 0x1f}
	pps := []byte{0x68, 0xce, 0x3c, 0x80}
	payload := []byte{nalTypeSTAPA, 0, byte(len(sps))}
	payload = append(payload, sps...)
	payload = append(payload, 0, byte(len(pps)))
	payload = append(payload, pps...)

	assert.Equal(t, []byte{nalTypeSPS, nalTypePPS}, h264NALTypes(payload))

	gotSPS, gotPPS := h264ParameterSets(payload)
	assert.Equal(t, sps, gotSPS)
	assert.Equal(t, pps, gotPPS)
}

func TestH264NALTypesFUA(t *testing.T) {
	// FU-A start fragment carrying an IDR slice.
	assert.Equal(t, []byte{nalTypeIDR}, h264NALTypes([]byte{0x7c, 0x85, 0x88, 0x84}))
	// FU-A continuation fragment is not a new NAL unit.
	assert.Nil(t, h264NALTypes([]byte{0x7c, 0x05, 0x88, 0x84}))
}

func TestH264ParameterSetsSingleNAL(t *testing.T) {
	sps := []byte{0x67, 0x42, 0x00, 0x1f, 0x8c}
	gotSPS, gotPPS := h264ParameterSets(sps)
	require.NotNil(t, gotSPS)
	assert.Nil(t, gotPPS)
}
