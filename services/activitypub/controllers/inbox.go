package controllers

import (
	"io"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/services/activitypub/apmodels"
)

// maxInboxBodyBytes bounds one federation inbox delivery. The inbox is
// unauthenticated and signature verification happens later in the worker, so the
// body must be capped here.
const maxInboxBodyBytes = 1 << 20

// InboxHandler handles inbound federated requests.
func (c *Controllers) InboxHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		c.acceptInboxRequest(w, r)
	} else {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (c *Controllers) acceptInboxRequest(w http.ResponseWriter, r *http.Request) {
	if !c.configRepository.GetFederationEnabled() {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	urlPathComponents := strings.Split(r.URL.Path, "/")
	var forLocalAccount string
	if len(urlPathComponents) == 5 {
		forLocalAccount = urlPathComponents[3]
	} else {
		log.Errorln("Unable to determine username from url path")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// The account this request is for must match the account name we have set
	// for federation.
	if forLocalAccount != c.configRepository.GetFederationUsername() {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// The inbox is unauthenticated and verification happens later in the worker,
	// so the body must be bounded here or one large POST can exhaust memory.
	r.Body = http.MaxBytesReader(w, r.Body, maxInboxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		log.Errorln("Unable to read inbox request payload", err)
		return
	}

	inboxRequest := apmodels.InboxRequest{Request: r, ForLocalAccount: forLocalAccount, Body: data}
	c.inbox.AddToQueue(inboxRequest)
	w.WriteHeader(http.StatusAccepted)
}
