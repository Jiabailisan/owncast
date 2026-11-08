package admin

import (
	"errors"
	"net/http"

	webutils "github.com/owncast/owncast/webserver/utils"
)

var errAdminDisconnect = errors.New("inbound connection disconnected by admin")

// DisconnectInboundConnection will force-disconnect an inbound stream,
// regardless of the ingest protocol that is currently publishing.
func (a *Admin) DisconnectInboundConnection(w http.ResponseWriter, r *http.Request) {
	if !a.stream.GetStatus().Online {
		webutils.WriteSimpleResponse(w, false, "no inbound stream connected")
		return
	}

	disconnected := a.sessions.DisconnectCurrent(errAdminDisconnect)
	webutils.WriteSimpleResponse(w, disconnected, "inbound stream disconnected")
}
