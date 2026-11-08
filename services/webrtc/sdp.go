package webrtc

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/owncast/owncast/models"
	"github.com/pion/sdp/v3"
	"github.com/pion/turn/v5"
	"github.com/pion/webrtc/v4"
)

const (
	turnCredentialTTL   = 5 * time.Minute
	trickleContentType  = "application/trickle-ice-sdpfrag"
	h264CodecPrefix     = "h264/"
	opusCodecPrefix     = "opus/"
	rtcpFeedbackAttrKey = "rtcp-fb"
	rtpmapAttrKey       = "rtpmap"
	fmtpAttrKey         = "fmtp"
	midAttrPrefix       = "a=mid:"
	candidateAttrPrefix = "a=candidate:"
	mediaLinePrefix     = "m="
)

// prunedOffer is the result of narrowing a browser offer to the single H.264
// and Opus payload types the server can deliver. Pruning is required because
// Pion's answerer adopts the offered payload types (and fmtp values) and the
// shared tracks are bound to one codec pair.
type prunedOffer struct {
	sdp              string
	videoPayloadType string
	audioPayloadType string
}

// pruneOffer parses a WHEP offer and keeps exactly one H.264 payload type
// (packetization-mode 1 preferred, profile matching the configured encoder
// profile preferred) and one Opus payload type. Everything the server cannot
// deliver (VP8/VP9/AV1/H265/RTX/G722/PCMU/PCMA/...) is removed before Pion sees
// the offer, so negotiation cannot select a codec Owncast can never produce.
func pruneOffer(raw string, cfg models.WebRTCConfig) (prunedOffer, error) {
	parsed := &sdp.SessionDescription{}
	if err := parsed.UnmarshalString(raw); err != nil {
		return prunedOffer{}, fmt.Errorf("%w: parse sdp: %v", ErrBadOffer, err)
	}

	videoPT, audioPT := "", ""
	mediaKeep := make([]bool, len(parsed.MediaDescriptions))

	for index, media := range parsed.MediaDescriptions {
		switch strings.ToLower(media.MediaName.Media) {
		case "video":
			videoPT = chooseH264PayloadType(media, cfg)
			mediaKeep[index] = true
			pruneMediaFormats(media, map[string]bool{videoPT: true})
		case "audio":
			audioPT = chooseOpusPayloadType(media)
			mediaKeep[index] = true
			pruneMediaFormats(media, map[string]bool{audioPT: true})
		default:
			// WHEP viewers only offer video/audio; other m-lines (data) are
			// rejected rather than answered.
			mediaKeep[index] = false
		}
	}

	if videoPT == "" {
		return prunedOffer{}, fmt.Errorf("%w: offer has no h264 video", ErrUnsupportedMedia)
	}
	if audioPT == "" {
		return prunedOffer{}, fmt.Errorf("%w: offer has no opus audio", ErrUnsupportedMedia)
	}

	kept := parsed.MediaDescriptions[:0]
	for index, media := range parsed.MediaDescriptions {
		if mediaKeep[index] {
			kept = append(kept, media)
		}
	}
	parsed.MediaDescriptions = kept

	encoded, err := parsed.Marshal()
	if err != nil {
		return prunedOffer{}, fmt.Errorf("%w: marshal pruned sdp: %v", ErrBadOffer, err)
	}

	return prunedOffer{sdp: string(encoded), videoPayloadType: videoPT, audioPayloadType: audioPT}, nil
}

func chooseH264PayloadType(media *sdp.MediaDescription, cfg models.WebRTCConfig) string {
	candidates := make([]codecCandidate, 0, 4)
	for _, attribute := range media.Attributes {
		if attribute.Key != rtpmapAttrKey {
			continue
		}
		fields := strings.Fields(attribute.Value)
		if len(fields) < 2 || !strings.HasPrefix(strings.ToLower(fields[1]), h264CodecPrefix) {
			continue
		}
		candidates = append(candidates, codecCandidate{payloadType: fields[0]})
	}
	if len(candidates) == 0 {
		return ""
	}

	// Attach fmtp details.
	for index := range candidates {
		fmtpLine := fmtpForPayloadType(media, candidates[index].payloadType)
		candidates[index].packetizationMode = fmtpParameter(fmtpLine, "packetization-mode")
		candidates[index].profileLevelID = fmtpParameter(fmtpLine, "profile-level-id")
	}

	wantProfile := h264ProfileIDC(cfg.H264Profile)

	// Preferred order: matching profile with packetization-mode 1, then any
	// packetization-mode 1, then matching profile, then anything H.264.
	type preference struct {
		score int
		pt    string
	}
	best := preference{score: -1}
	for _, candidate := range candidates {
		score := 0
		if candidate.packetizationMode == "1" {
			score += 2
		}
		if profileIDMatches(candidate.profileLevelID, wantProfile) {
			score += 1
		}
		if score > best.score {
			best = preference{score: score, pt: candidate.payloadType}
		}
	}

	return best.pt
}

func chooseOpusPayloadType(media *sdp.MediaDescription) string {
	for _, attribute := range media.Attributes {
		if attribute.Key != rtpmapAttrKey {
			continue
		}
		fields := strings.Fields(attribute.Value)
		if len(fields) < 2 || !strings.HasPrefix(strings.ToLower(fields[1]), opusCodecPrefix) {
			continue
		}

		return fields[0]
	}

	return ""
}

type codecCandidate struct {
	payloadType       string
	packetizationMode string
	profileLevelID    string
}

func fmtpForPayloadType(media *sdp.MediaDescription, payloadType string) string {
	for _, attribute := range media.Attributes {
		if attribute.Key != fmtpAttrKey {
			continue
		}
		fields := strings.SplitN(attribute.Value, " ", 2)
		if len(fields) == 2 && fields[0] == payloadType {
			return fields[1]
		}
	}

	return ""
}

func fmtpParameter(fmtpLine, key string) string {
	for _, part := range strings.Split(fmtpLine, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.EqualFold(kv[0], key) {
			return strings.TrimSpace(kv[1])
		}
	}

	return ""
}

// profileIDMatches compares only the profile_idc and constraint bytes of a
// profile-level-id, mirroring Pion's own H.264 fmtp matching rule.
func profileIDMatches(profileLevelID string, profileIDC byte) bool {
	if len(profileLevelID) < 4 {
		return false
	}
	profileByte, ok := hexByte(profileLevelID[0:2])
	if !ok {
		return false
	}

	return profileByte == profileIDC
}

func hexByte(pair string) (byte, bool) {
	if len(pair) != 2 {
		return 0, false
	}
	var value byte
	for i := 0; i < 2; i++ {
		value <<= 4
		switch c := pair[i]; {
		case c >= '0' && c <= '9':
			value |= c - '0'
		case c >= 'a' && c <= 'f':
			value |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			value |= c - 'A' + 10
		default:
			return 0, false
		}
	}

	return value, true
}

// pruneMediaFormats keeps only the given payload types in the media formats
// list and removes the codec attributes that refer to the dropped types.
func pruneMediaFormats(media *sdp.MediaDescription, keep map[string]bool) {
	formats := make([]string, 0, len(media.MediaName.Formats))
	for _, format := range media.MediaName.Formats {
		if keep[format] {
			formats = append(formats, format)
		}
	}
	media.MediaName.Formats = formats

	attributes := make([]sdp.Attribute, 0, len(media.Attributes))
	for _, attribute := range media.Attributes {
		if !keepMediaAttribute(attribute, keep) {
			continue
		}
		attributes = append(attributes, attribute)
	}
	media.Attributes = attributes
}

func keepMediaAttribute(attribute sdp.Attribute, keep map[string]bool) bool {
	switch attribute.Key {
	case rtpmapAttrKey, fmtpAttrKey, rtcpFeedbackAttrKey:
		fields := strings.Fields(attribute.Value)
		if len(fields) == 0 {
			return true
		}
		if fields[0] == "*" {
			return true
		}

		return keep[fields[0]]
	case "rid", "simulcast":
		// Owncast delivers one encoded stream; simulcast offers would add
		// media sections/streams the server cannot produce.
		return false
	default:
		return true
	}
}

// parseTrickleFragment parses an application/trickle-ice-sdpfrag body into
// ICECandidateInit values. The fragment format follows RFC 8840: session-level
// a=ice-ufrag/a=ice-pwd plus media sections with a=mid and a=candidate lines.
func parseTrickleFragment(raw string) ([]webrtc.ICECandidateInit, error) {
	lines := strings.Split(raw, "\n")

	var (
		candidates []webrtc.ICECandidateInit
		mid        string
		mediaIndex = -1
	)

	for _, line := range lines {
		line = strings.TrimSuffix(strings.TrimSpace(line), "\r")
		switch {
		case strings.HasPrefix(line, mediaLinePrefix):
			mediaIndex++
		case strings.HasPrefix(line, midAttrPrefix):
			mid = strings.TrimPrefix(line, midAttrPrefix)
		case strings.HasPrefix(line, candidateAttrPrefix):
			candidate := strings.TrimPrefix(line, "a=")
			if strings.TrimSpace(candidate) == "" {
				continue
			}
			init := webrtc.ICECandidateInit{Candidate: candidate}
			if mid != "" {
				midValue := mid
				init.SDPMid = &midValue
			}
			if mediaIndex >= 0 {
				index := uint16(mediaIndex) //nolint:gosec // m-line index is tiny
				init.SDPMLineIndex = &index
			}
			candidates = append(candidates, init)
		}
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no candidates in trickle fragment", ErrBadOffer)
	}

	return candidates, nil
}

// buildLinkHeaders renders the RFC 9725 ice-server Link headers for the WHEP
// 201 response. TURN entries backed by a static secret get time-limited REST
// credentials generated for this session.
func buildLinkHeaders(cfg models.WebRTCConfig) []string {
	entries := viewerICEEntries(cfg)
	headers := make([]string, 0, len(entries))

	for _, entry := range entries {
		if strings.TrimSpace(entry.URL) == "" {
			continue
		}
		switch {
		case entry.StaticSecret != "":
			username, credential, err := turn.GenerateLongTermTURNRESTCredentials(entry.StaticSecret, "viewer", turnCredentialTTL)
			if err != nil {
				continue
			}
			headers = append(headers, fmt.Sprintf(
				"<%s>; rel=%q; username=%q; credential=%q; credential-type=%q",
				entry.URL, "ice-server", username, credential, "password"))
		case entry.Username != "" || entry.Credential != "":
			headers = append(headers, fmt.Sprintf(
				"<%s>; rel=%q; username=%q; credential=%q",
				entry.URL, "ice-server", entry.Username, entry.Credential))
		default:
			headers = append(headers, fmt.Sprintf("<%s>; rel=%q", entry.URL, "ice-server"))
		}
	}

	return headers
}

// PublicICEServer is one entry of the additive /api/config webrtc.iceServers
// array consumed by the viewer frontend.
type PublicICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// PublicICEServers returns the viewer-facing ICE server list for /api/config.
func (s *Service) PublicICEServers() []PublicICEServer {
	cfg := s.Config()
	entries := viewerICEEntries(cfg)
	servers := make([]PublicICEServer, 0, len(entries))

	for _, entry := range entries {
		if strings.TrimSpace(entry.URL) == "" {
			continue
		}
		server := PublicICEServer{URLs: []string{entry.URL}}
		switch {
		case entry.StaticSecret != "":
			username, credential, err := turn.GenerateLongTermTURNRESTCredentials(entry.StaticSecret, "viewer", turnCredentialTTL)
			if err != nil {
				continue
			}
			server.Username = username
			server.Credential = credential
		default:
			server.Username = entry.Username
			server.Credential = entry.Credential
		}
		servers = append(servers, server)
	}

	return servers
}

// allowedOriginHeaderFor returns the CORS origin header value for a request.
func allowedOriginHeaderFor(r *http.Request, cfg models.WebRTCConfig) string {
	return allowedOriginHeader(r, cfg)
}
