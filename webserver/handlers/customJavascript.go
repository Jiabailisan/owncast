package handlers

import (
	"net/http"

	"github.com/owncast/owncast/config/settings"
)

// ServeCustomJavascript serves the admin's custom JavaScript followed
// by the concatenated JS contributed by every loaded plugin's
// manifest.scripts entries (each preceded by a `// plugin: <slug>
// ...` delimiter for devtools attribution). The viewer loads a single
// <script> tag pointing at this endpoint so plugins can extend the
// page without each plugin needing its own <script> tag.
func (h *Handlers) ServeCustomJavascript(w http.ResponseWriter, r *http.Request) {
	// A custom viewer frontend replaces the frontend entirely: Custom CSS and
	// Custom JavaScript are neither injected nor served (spec 4.6).
	if appearance := h.resolveAppearance(); appearance.Mode == settings.FrontendCustomFrontend {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	adminJS := h.configRepository.GetCustomJavascript()
	var body []byte
	if len(adminJS) > 0 {
		// Isolate admin custom JS so a runtime error in user-supplied script
		// cannot abort execution of plugin scripts appended afterwards.
		body = append(body, []byte("try {\n")...)
		body = append(body, []byte(adminJS)...)
		if adminJS[len(adminJS)-1] != '\n' {
			body = append(body, '\n')
		}
		body = append(body, []byte("} catch (err) { console.error('Custom JavaScript error:', err); }\n")...)
	}
	if h.pluginJSContent != nil {
		pluginJS := h.pluginJSContent()
		if len(pluginJS) > 0 {
			body = append(body, pluginJS...)
		}
	}
	_, _ = w.Write(body)
}
