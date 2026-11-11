package webrtc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/owncast/owncast/models"
	pionice "github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/cc"
	"github.com/pion/interceptor/pkg/gcc"
	"github.com/pion/interceptor/pkg/stats"
	pionlogging "github.com/pion/logging"
	"github.com/pion/webrtc/v4"
	log "github.com/sirupsen/logrus"
)

const tcpMuxReadBufferSize = 1500

// engine owns the Pion API object: the SettingEngine (ICE/DTLS transport
// policy), the MediaEngine (H.264 + Opus only) and the interceptor registry
// (NACK, RTCP reports, TWCC, optional GCC). It also owns the single-port mux
// sockets that are shared by every PeerConnection.
type engine struct {
	cfg        models.WebRTCConfig
	api        *webrtc.API
	iceServers []webrtc.ICEServer
	closers    []io.Closer

	// createMu serialises PeerConnection creation so the interceptor factory
	// callbacks (which fire inside NewPeerConnection and carry an opaque
	// PeerConnection id) can be correlated with the returned PeerConnection.
	createMu sync.Mutex
	mu       sync.Mutex

	pendingStats      []stats.Getter
	pendingEstimators []cc.BandwidthEstimator
	statsGetters      map[*webrtc.PeerConnection]stats.Getter
	estimators        map[*webrtc.PeerConnection]cc.BandwidthEstimator
}

func newEngine(ctx context.Context, cfg models.WebRTCConfig) (*engine, error) {
	cfg = NormalizeConfig(cfg)
	if err := ValidateConfig(cfg); err != nil {
		return nil, err
	}

	e := &engine{
		cfg:          cfg,
		iceServers:   iceServersForGathering(cfg),
		statsGetters: map[*webrtc.PeerConnection]stats.Getter{},
		estimators:   map[*webrtc.PeerConnection]cc.BandwidthEstimator{},
	}
	loggerFactory := newPionLoggerFactory()

	settingEngine, err := e.buildSettingEngine(ctx, loggerFactory)
	if err != nil {
		return nil, err
	}

	mediaEngine, err := buildMediaEngine(cfg)
	if err != nil {
		// The mux sockets are already bound at this point; without closing them a
		// later Start cannot rebind the port without restarting the process.
		e.closeSockets()
		return nil, err
	}

	interceptorRegistry, err := e.buildInterceptorRegistry(mediaEngine)
	if err != nil {
		e.closeSockets()
		return nil, err
	}

	e.api = webrtc.NewAPI(
		webrtc.WithSettingEngine(*settingEngine),
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

	return e, nil
}

func (e *engine) buildSettingEngine(ctx context.Context, loggerFactory pionlogging.LoggerFactory) (*webrtc.SettingEngine, error) {
	se := &webrtc.SettingEngine{LoggerFactory: loggerFactory}

	networkTypes, err := iceNetworkTypes(e.cfg)
	if err != nil {
		return nil, err
	}
	se.SetNetworkTypes(networkTypes)
	// A loopback bind address (used for local testing and same-host setups) only
	// produces loopback candidates, which Pion filters out by default. Without
	// this the SDP answer carries no candidates and viewers cannot connect.
	if bindAddr, parseErr := netip.ParseAddr(e.cfg.BindAddress); parseErr == nil && bindAddr.IsLoopback() {
		se.SetIncludeLoopbackCandidate(true)
	}
	se.SetICETimeouts(
		time.Duration(e.cfg.ICEDisconnectedTimeoutMs)*time.Millisecond,
		time.Duration(e.cfg.ICEFailedTimeoutMs)*time.Millisecond,
		time.Duration(e.cfg.ICEKeepaliveIntervalMs)*time.Millisecond,
	)
	if e.cfg.MDNS {
		se.SetICEMulticastDNSMode(pionice.MulticastDNSModeQueryAndGather)
	} else {
		se.SetICEMulticastDNSMode(pionice.MulticastDNSModeDisabled)
	}
	if len(e.cfg.InterfaceAllow) > 0 || len(e.cfg.InterfaceDeny) > 0 {
		se.SetInterfaceFilter(interfaceFilter(e.cfg))
	}
	if e.cfg.ICELite {
		se.SetLite(true)
	}
	// Bound STUN gathering so a slow/unreachable STUN server cannot push the
	// WHEP 201 answer past the client's patience (the handler waits at most
	// iceGatherTimeout for gathering to complete).
	if len(e.cfg.STUNServers) > 0 {
		se.SetSTUNGatherTimeout(3 * time.Second)
	}

	ips, err := advertisedIPs(ctx, e.cfg)
	if err != nil {
		log.Warnf("webrtc: could not resolve advertised IPs for %q: %v", e.cfg.Domain, err)
	}
	if len(ips) > 0 {
		if err := se.SetICEAddressRewriteRules(addressRewriteRules(ips)...); err != nil {
			return nil, fmt.Errorf("ice address rewrite rules: %w", err)
		}
	}

	if err := e.setupMuxes(se, loggerFactory); err != nil {
		e.closeSockets()

		return nil, err
	}

	return se, nil
}

func (e *engine) setupMuxes(se *webrtc.SettingEngine, loggerFactory pionlogging.LoggerFactory) error {
	muxLogger := loggerFactory.NewLogger("ice-mux")

	if e.cfg.UDPMode == udpModeRange {
		minPort := uint16(e.cfg.UDPPortMin) //nolint:gosec // validated 1024..65535
		maxPort := uint16(e.cfg.UDPPortMax) //nolint:gosec // validated 1024..65535
		if err := se.SetEphemeralUDPPortRange(minPort, maxPort); err != nil {
			return fmt.Errorf("set webrtc udp port range: %w", err)
		}
	} else {
		addr := net.JoinHostPort(e.cfg.BindAddress, strconv.Itoa(e.cfg.UDPPort))
		conn, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("bind webrtc udp mux %s: %w", addr, err)
		}
		se.SetICEUDPMux(webrtc.NewICEUDPMux(muxLogger, conn))
		e.closers = append(e.closers, conn)
	}

	if e.cfg.TCPEnabled {
		addr := net.JoinHostPort(e.cfg.BindAddress, strconv.Itoa(e.cfg.TCPPort))
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("bind webrtc tcp mux %s: %w", addr, err)
		}
		se.SetICETCPMux(webrtc.NewICETCPMux(muxLogger, listener, tcpMuxReadBufferSize))
		e.closers = append(e.closers, listener)
	}

	return nil
}

func buildMediaEngine(cfg models.WebRTCConfig) (*webrtc.MediaEngine, error) {
	h264Fmtp, err := canonicalH264Fmtp(cfg.H264Profile, cfg.H264Level)
	if err != nil {
		return nil, err
	}

	me := &webrtc.MediaEngine{}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeH264,
			ClockRate:   rtpClockRate,
			SDPFmtpLine: h264Fmtp,
		},
		PayloadType: videoPayloadType,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, fmt.Errorf("register h264 codec: %w", err)
	}
	if err := me.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:    webrtc.MimeTypeOpus,
			ClockRate:   opusClockRate,
			Channels:    opusChannels,
			SDPFmtpLine: canonicalOpusFmtp,
		},
		PayloadType: audioPayloadType,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, fmt.Errorf("register opus codec: %w", err)
	}

	return me, nil
}

func (e *engine) buildInterceptorRegistry(mediaEngine *webrtc.MediaEngine) (*interceptor.Registry, error) {
	registry := &interceptor.Registry{}

	// The congestion controller must be registered before the NACK responder so
	// retransmissions can be limited by the estimated target bitrate.
	if e.cfg.BandwidthEstimation {
		// The GCC interceptor ships a leaky-bucket pacer that rate-limits the
		// send path to the estimated bandwidth. On a LAN (0% loss) the estimate
		// starts around 1.2 Mbps and ramps slowly, so a 6 Mbps stream is paced
		// far below its encoded rate: frames arrive late, the viewer's jitter
		// buffer grows, and frames are dropped as "too late" (stutter) while
		// partial frames reach the decoder (artifacts). Owncast only throttles
		// when maxBitratePerViewer is configured, so keep the estimator for
		// reporting but never pace the media path.
		ccFactory, err := cc.NewInterceptor(func() (cc.BandwidthEstimator, error) {
			return gcc.NewSendSideBWE(gcc.SendSideBWEPacer(gcc.NewNoOpPacer()))
		})
		if err != nil {
			return nil, fmt.Errorf("cc interceptor: %w", err)
		}
		ccFactory.OnNewPeerConnection(func(_ string, estimator cc.BandwidthEstimator) {
			e.mu.Lock()
			e.pendingEstimators = append(e.pendingEstimators, estimator)
			e.mu.Unlock()
		})
		registry.Add(ccFactory)
	}

	statsFactory, err := stats.NewInterceptor()
	if err != nil {
		return nil, fmt.Errorf("stats interceptor: %w", err)
	}
	statsFactory.OnNewPeerConnection(func(_ string, getter stats.Getter) {
		e.mu.Lock()
		e.pendingStats = append(e.pendingStats, getter)
		e.mu.Unlock()
	})
	registry.Add(statsFactory)

	if e.cfg.NACK {
		if err := webrtc.ConfigureNack(mediaEngine, registry); err != nil {
			return nil, fmt.Errorf("nack interceptor: %w", err)
		}
	}
	if err := webrtc.ConfigureRTCPReports(registry); err != nil {
		return nil, fmt.Errorf("rtcp reports interceptor: %w", err)
	}
	if e.cfg.TWCC {
		if err := webrtc.ConfigureTWCCHeaderExtensionSender(mediaEngine, registry); err != nil {
			return nil, fmt.Errorf("twcc header extension: %w", err)
		}
		if err := webrtc.ConfigureTWCCSender(mediaEngine, registry); err != nil {
			return nil, fmt.Errorf("twcc feedback: %w", err)
		}
	}

	return registry, nil
}

// release forgets the per-viewer stats getter and bandwidth estimator recorded
// for a closed PeerConnection. Without it every viewer session ever created
// stays in these maps for the lifetime of the process.
func (e *engine) release(pc *webrtc.PeerConnection) {
	if e == nil || pc == nil {
		return
	}
	e.mu.Lock()
	delete(e.statsGetters, pc)
	delete(e.estimators, pc)
	e.mu.Unlock()
}

// newPeerConnection creates a PeerConnection and returns its per-viewer stats
// getter (nil when stats are disabled, which they never are in production).
func (e *engine) newPeerConnection() (*webrtc.PeerConnection, stats.Getter, cc.BandwidthEstimator, error) {
	e.createMu.Lock()
	defer e.createMu.Unlock()

	pc, err := e.api.NewPeerConnection(webrtc.Configuration{ICEServers: e.iceServers})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("new peer connection: %w", err)
	}

	e.mu.Lock()
	getter := popStatsGetter(e.pendingStats)
	e.pendingStats = e.pendingStats[:len(e.pendingStats)-boolToInt(getter != nil)]
	estimator := popEstimator(e.pendingEstimators)
	e.pendingEstimators = e.pendingEstimators[:len(e.pendingEstimators)-boolToInt(estimator != nil)]
	if getter != nil {
		e.statsGetters[pc] = getter
	}
	if estimator != nil {
		e.estimators[pc] = estimator
	}
	e.mu.Unlock()

	return pc, getter, estimator, nil
}

func (e *engine) closeSockets() {
	for _, closer := range e.closers {
		if err := closer.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Debugf("webrtc: closing mux socket: %v", err)
		}
	}
	e.closers = nil
}

// close releases the mux sockets. PeerConnections are closed by their sessions.
func (e *engine) close() {
	e.closeSockets()
}

func interfaceFilter(cfg models.WebRTCConfig) func(string) bool {
	allow := make(map[string]bool, len(cfg.InterfaceAllow))
	for _, name := range cfg.InterfaceAllow {
		allow[name] = true
	}
	deny := make(map[string]bool, len(cfg.InterfaceDeny))
	for _, name := range cfg.InterfaceDeny {
		deny[name] = true
	}

	return func(name string) bool {
		if deny[name] {
			return false
		}
		if len(allow) > 0 {
			return allow[name]
		}

		return true
	}
}

func popStatsGetter(pending []stats.Getter) stats.Getter {
	if len(pending) == 0 {
		return nil
	}

	return pending[len(pending)-1]
}

func popEstimator(pending []cc.BandwidthEstimator) cc.BandwidthEstimator {
	if len(pending) == 0 {
		return nil
	}

	return pending[len(pending)-1]
}

func boolToInt(v bool) int {
	if v {
		return 1
	}

	return 0
}
