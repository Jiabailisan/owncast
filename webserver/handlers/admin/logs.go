package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/logging"
)

// GetLogs will return all logs.
func GetLogs(w http.ResponseWriter, r *http.Request) {
	logs := logging.Logger.AllEntries()
	response := make([]logsResponse, 0)

	for i := 0; i < len(logs); i++ {
		if logs[i] != nil {
			response = append(response, fromEntry(logs[i]))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Errorln(err)
	}
}

// GetWarnings will return only warning and error logs.
func GetWarnings(w http.ResponseWriter, r *http.Request) {
	logs := logging.Logger.WarningEntries()
	response := make([]logsResponse, 0)

	for i := 0; i < len(logs); i++ {
		logEntry := logs[i]
		if logEntry != nil {
			response = append(response, fromEntry(logEntry))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Errorln(err)
	}
}

type logsResponse struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
	Level   string    `json:"level"`
}

func fromEntry(e *log.Entry) logsResponse {
	return logsResponse{
		Message: formatEntryMessage(e),
		Level:   e.Level.String(),
		Time:    e.Time,
	}
}

// formatEntryMessage appends structured logrus fields (e.g. SRT remote IP,
// rtt_ms, packet_loss_pct, WebRTC read-ahead/variant fields) in deterministic
// key=value order so the Admin Logs page matches owncast.log instead of
// hiding every WithFields payload.
func formatEntryMessage(e *log.Entry) string {
	if e == nil {
		return ""
	}
	if len(e.Data) == 0 {
		return e.Message
	}

	keys := make([]string, 0, len(e.Data))
	for k := range e.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString(e.Message)
	for _, k := range keys {
		val := fmt.Sprint(e.Data[k])
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		if strings.ContainsAny(val, " \t\n\"") {
			fmt.Fprintf(&b, "%q", val)
		} else {
			b.WriteString(val)
		}
	}
	return b.String()
}
