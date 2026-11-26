package middleware

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/owncast/owncast/models"
	"github.com/owncast/owncast/utils"
)

type userContextKey struct{}

// UserFromRequest returns the moderator attached by
// RequireUserModerationScopeAccesstoken, or nil for other authentication paths.
func UserFromRequest(r *http.Request) *models.User {
	user, _ := r.Context().Value(userContextKey{}).(*models.User)
	return user
}

// ExternalAccessTokenHandlerFunc is a function that is called after validing access.
type ExternalAccessTokenHandlerFunc func(models.ExternalAPIUser, http.ResponseWriter, *http.Request)

// UserAccessTokenHandlerFunc is a function that is called after validing user access.
type UserAccessTokenHandlerFunc func(models.User, http.ResponseWriter, *http.Request)

// adminAuthRealm is the WWW-Authenticate realm string used by every admin
// auth challenge in Owncast. Anything that gates on admin Basic Auth (the
// main admin API, the plugin management API, plugin admin pages) must
// challenge with this exact realm so the browser shares one credential
// cache across all of them.
const adminAuthRealm = "Owncast Authenticated Request"

const (
	adminCSRFHeader        = "X-Owncast-CSRF-Protection"
	developmentAdminOrigin = "http://localhost:3000"
)

// isAdminBasicAuth checks HTTP Basic Auth credentials against the configured
// admin password.
func (m *Middleware) isAdminBasicAuth(r *http.Request) bool {
	if m.configRepository == nil {
		return false
	}
	if user, pass, ok := r.BasicAuth(); ok {
		if subtle.ConstantTimeCompare([]byte(user), []byte("admin")) == 1 &&
			utils.CompareHash(m.configRepository.GetAdminPassword(), pass) == nil {
			return true
		}
	}
	return false
}

// IsAdminRequest reports whether r carries valid admin credentials. The
// Owncast admin UI sends Basic Auth on every API call; embedded contexts
// that cannot inject a custom Authorization header (notably plugin admin
// iframes under /plugins/) authenticate via the admin session cookie instead.
func (m *Middleware) IsAdminRequest(r *http.Request) bool {
	if m.isAdminBasicAuth(r) {
		return true
	}
	// Strictly allowlist plugin routes for the iframe session cookie so
	// same-origin plugin or /public/ scripts can never authenticate to
	// /api/admin/* or /admin/* without Basic Auth credentials.
	if !strings.HasPrefix(path.Clean(r.URL.Path), "/plugins/") {
		return false
	}
	return m.hasValidAdminSessionCookie(r)
}

// RequireAdminAuth wraps a handler requiring HTTP basic auth for it using
// the admin password as the password and a hardcoded "admin" for username.
//
// As a side effect, a valid Basic Auth request also primes an admin
// session cookie. Embedded contexts that can't inject the Authorization
// header (notably plugin admin iframes) authenticate via that cookie on
// their next same-origin request, so the user isn't prompted by the
// browser's native Basic Auth dialog.
func isAdminOriginAllowed(r *http.Request) bool {
	originValue := r.Header.Get("Origin")
	if originValue == "" {
		return true
	}

	origin, err := url.Parse(originValue)
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil ||
		origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}

	if originValue == developmentAdminOrigin {
		return isDevelopmentAdminOrigin(r)
	}

	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}

	return strings.EqualFold(origin.Scheme, scheme) && strings.EqualFold(origin.Host, r.Host)
}

func isDevelopmentAdminOrigin(r *http.Request) bool {
	if r.Header.Get("Origin") != developmentAdminOrigin {
		return false
	}

	hostname := r.Host
	if host, _, err := net.SplitHostPort(r.Host); err == nil {
		hostname = host
	}
	hostname = strings.Trim(hostname, "[]")
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func requiresCSRFHeader(r *http.Request) bool {
	return r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions &&
		r.Header.Get("Origin") == "" && r.Header.Get("Authorization") == ""
}

func (m *Middleware) RequireAdminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isDevelopmentAdminOrigin(r) {
			w.Header().Set("Access-Control-Allow-Origin", developmentAdminOrigin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept, Authorization, "+adminCSRFHeader)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

		if !isAdminOriginAllowed(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if !m.IsAdminRequest(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+adminAuthRealm+`"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			log.Debugln("Failed admin authentication")
			return
		}

		if requiresCSRFHeader(r) && r.Header.Get(adminCSRFHeader) != "1" {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		// Only prime the /plugins/-scoped session cookie on admin entry / plugin
		// routes so periodic /api/admin/* status polls (which intentionally do not
		// send the /plugins/ cookie back) do not mint a fresh session every 2s.
		if shouldPrimeAdminSessionCookie(r.URL.Path) {
			m.ensureAdminSessionCookie(w, r)
		}

		handler(w, r)
	}
}

func accessDenied(w http.ResponseWriter) {
	w.WriteHeader(http.StatusUnauthorized) //nolint
	w.Write([]byte("unauthorized"))        //nolint
}

func logInvalidAccessToken(r *http.Request, scope string) {
	log.Warnf(
		"Invalid access token from %s for %s %s (required scope: %s)",
		utils.GetIPAddressFromRequest(r),
		r.Method,
		r.URL.Path,
		scope,
	)
}

// RequireExternalAPIAccessToken will validate a 3rd party access token.
func (m *Middleware) RequireExternalAPIAccessToken(scope string, handler ExternalAccessTokenHandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// We should accept 3rd party preflight OPTIONS requests.
		if r.Method == "OPTIONS" {
			// All OPTIONS requests should have a wildcard CORS header.
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		authHeader := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			token = authHeader[len("bearer "):]
		}

		if token == "" {
			logInvalidAccessToken(r, scope)
			accessDenied(w)
			return
		}

		integration, err := m.userRepository.GetExternalAPIUserForAccessTokenAndScope(token, scope)
		if integration == nil || err != nil {
			logInvalidAccessToken(r, scope)
			accessDenied(w)
			return
		}

		// All auth'ed 3rd party requests should have a wildcard CORS header.
		w.Header().Set("Access-Control-Allow-Origin", "*")

		handler(*integration, w, r)

		if err := m.userRepository.SetExternalAPIUserAccessTokenAsUsed(token); err != nil {
			log.Debugln("token not found when updating last_used timestamp")
		}
	})
}

// RequireUserAccessToken will validate a provided user's access token and make sure the associated user is enabled.
// Not to be used for validating 3rd party access.
func (m *Middleware) RequireUserAccessToken(handler UserAccessTokenHandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessToken := r.URL.Query().Get("accessToken")
		if accessToken == "" {
			accessDenied(w)
			return
		}

		ipAddress := utils.GetIPAddressFromRequest(r)
		// Check if this client's IP address is banned.
		if blocked, err := m.authRepository.IsIPAddressBanned(ipAddress); blocked {
			log.Debugln("Client ip address has been blocked. Rejecting.")
			accessDenied(w)
			return
		} else if err != nil {
			log.Errorln("error determining if IP address is blocked: ", err)
		}

		// A user is required to use the websocket
		user := m.userRepository.GetUserByToken(accessToken)
		if user == nil || !user.IsEnabled() {
			accessDenied(w)
			return
		}

		handler(*user, w, r)
	})
}

// RequireUserModerationScopeAccesstoken will validate a provided user's access token and make sure the associated user is enabled
// and has "MODERATOR" scope assigned to the user.
func (m *Middleware) RequireUserModerationScopeAccesstoken(handler http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessToken := r.URL.Query().Get("accessToken")
		if accessToken == "" {
			accessDenied(w)
			return
		}
		// A user is required to use the websocket
		user := m.userRepository.GetUserByToken(accessToken)
		if user == nil || !user.IsEnabled() || !user.IsModerator() {
			accessDenied(w)
			return
		}

		// Keep the authenticated moderator attached to the request so handlers
		// can attribute moderation events without re-reading untrusted request
		// parameters.
		r = r.WithContext(context.WithValue(r.Context(), userContextKey{}, user))
		handler(w, r)
	})
}
