package utils

import (
	"encoding/json"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/webserver/router/middleware"
)

type J map[string]interface{}

// InternalErrorHandler will return an error message as an HTTP response.
func InternalErrorHandler(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	writeJSON(w, J{"error": err.Error()})
}

// writeJSON encodes the payload once. An encode failure is logged and dropped:
// recursing into the error handler would retry the same broken write until the
// goroutine stack is exhausted, which kills the process.
func writeJSON(w http.ResponseWriter, payload any) {
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Debugln("unable to write response:", err)
	}
}

// BadRequestHandler will return an HTTP 500 as an HTTP response.
func BadRequestHandler(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}

	log.Debugln(err)

	w.WriteHeader(http.StatusBadRequest)
	writeJSON(w, J{"error": err.Error()})
}

// WriteSimpleResponse will return a message as a response.
func WriteSimpleResponse(w http.ResponseWriter, success bool, message string) {
	response := models.BaseAPIResponse{
		Success: success,
		Message: message,
	}

	writeSimpleResponse(w, response)
}

// WriteSimpleResponseWithCode will return a message and machine-readable error code as a response.
func WriteSimpleResponseWithCode(w http.ResponseWriter, success bool, message string, errorCode string) {
	response := models.BaseAPIResponse{
		Success:   success,
		Message:   message,
		ErrorCode: errorCode,
	}

	writeSimpleResponse(w, response)
}

func writeSimpleResponse(w http.ResponseWriter, response models.BaseAPIResponse) {
	w.Header().Set("Content-Type", "application/json")

	if response.Success {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusBadRequest)
	}

	writeJSON(w, response)
}

// WriteResponse will return an object as a JSON encoded uncacheable response.
func WriteResponse(w http.ResponseWriter, response interface{}) {
	w.Header().Set("Content-Type", "application/json")
	middleware.DisableCache(w)

	w.WriteHeader(http.StatusOK)

	writeJSON(w, response)
}

// WriteString will return a basic string and a status code to the client.
func WriteString(w http.ResponseWriter, text string, status int) error {
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(status)
	_, err := w.Write([]byte(text))
	return err
}
