package handlers

import (
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// adminPathRedirects 301-redirects every legacy admin path to its
// new home under /{conf}/{role}/...:
//
//	/admin/{conf}/...        →  /{conf}/admin/...
//	/admin/applicants/{conf}/...  →  /{conf}/admin/applicants/...
//	/admin/speakers/{conf}/...    →  /{conf}/admin/speakers/...
//	/admin/registrations/{conf}/...→ /{conf}/admin/registrations/...
//	/admin/sponsors/{conf}/...    →  /{conf}/admin/sponsors/...
//	/admin/social/{conf}/...      →  /{conf}/admin/social/...
//	/vols/admin/{conf}/...        →  /{conf}/volcoord/...
//
// Bookmarked URLs and email links from the previous URL scheme keep
// working transparently — gorilla/mux's path variables capture the
// {conf} segment and the trailing path; we splice in the new shape
// and 301 the browser. Method-preserving 308 would be more correct
// for POST, but every old-shape POST URL is one we control via
// templates which we're updating in-tree, so 301-only is fine.
func adminPathRedirects() []struct {
	old, new string
} {
	return []struct{ old, new string }{
		{"/admin/{conf}", "/{conf}/admin"},
		{"/admin/applicants/{conf}", "/{conf}/admin/applicants"},
		{"/admin/speakers/{conf}", "/{conf}/admin/speakers"},
		{"/admin/registrations/{conf}", "/{conf}/admin/registrations"},
		{"/admin/sponsors/{conf}", "/{conf}/admin/sponsors"},
		{"/admin/social/{conf}", "/{conf}/admin/social"},
		{"/vols/admin/{conf}", "/{conf}/volcoord"},
	}
}

// RegisterAdminRedirects wires the 301-redirect handlers onto r.
// Called from the main router setup once the new /{conf}/{role}/...
// routes have been registered, so the redirects are last-resort
// matches and don't accidentally swallow live routes.
func RegisterAdminRedirects(r *mux.Router) {
	for _, m := range adminPathRedirects() {
		oldPrefix := m.old
		newPrefix := m.new
		// Catch the bare prefix (e.g. /vols/admin/{conf}) plus any
		// trailing path. Two registrations because mux's
		// PathPrefix-with-variable doesn't always coexist nicely
		// with explicit-path matchers; explicit registrations are
		// predictable.
		r.HandleFunc(oldPrefix, redirectAdminPath(oldPrefix, newPrefix))
		r.HandleFunc(oldPrefix+"/{rest:.*}", redirectAdminPath(oldPrefix, newPrefix))
	}
}

// redirectAdminPath returns the http.HandlerFunc that 301s the old
// path to the new shape, preserving the {conf} segment + any
// trailing path + the query string.
func redirectAdminPath(oldPrefix, newPrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		confTag := vars["conf"]
		// Reconstruct the canonical old prefix with this conf tag
		// substituted in, then chop it off the front of r.URL.Path
		// to recover the trailing segment ("/review", "/shifts/X",
		// etc.).
		oldFilled := strings.Replace(oldPrefix, "{conf}", confTag, 1)
		rest := strings.TrimPrefix(r.URL.Path, oldFilled)
		dest := strings.Replace(newPrefix, "{conf}", confTag, 1) + rest
		if r.URL.RawQuery != "" {
			dest += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, dest, http.StatusMovedPermanently)
	}
}
