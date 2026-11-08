package models

import "time"

// IngestStatus is the protocol-neutral snapshot of the currently connected
// publisher. Every metric is nullable when the protocol or library cannot
// report it; consumers must render "N/A" rather than zero.
type IngestStatus struct {
	Protocol         string     `json:"protocol"`
	SessionID        string     `json:"sessionID"`
	StreamKeyID      string     `json:"streamKeyID"`
	RemoteAddr       string     `json:"remoteAddr"`
	StartTime        time.Time  `json:"startTime"`
	UptimeSeconds    float64    `json:"uptimeSeconds"`
	BitrateKbps      *float64   `json:"bitrateKbps"`
	PacketLossPct    *float64   `json:"packetLossPct"`
	RTTMs            *float64   `json:"rttMs"`
	Retransmitted    *int64     `json:"retransmittedPackets"`
	DroppedPackets   *int64     `json:"droppedPackets"`
	Extra            map[string]float64 `json:"detail,omitempty"`
	Source           *Broadcaster `json:"source,omitempty"`
}
