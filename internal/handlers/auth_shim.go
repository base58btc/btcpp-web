package handlers

import (
	"net/http"

	"btcpp-web/internal/auth"
	"btcpp-web/internal/config"

	"github.com/gorilla/mux"
)

// requireConfAdmin gates a per-conf admin route on the request's
// {conf} mux var. Returns nil identity (with response already
// written) when access is denied — caller should `return` immediately.
//
// Replaces the legacy `helpers.CheckPin(...)` pattern; the role
// check now considers the user's Speakers DB Roles column rather
// than a single shared PIN in the session.
func requireConfAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) *auth.Identity {
	return auth.RequireRole(w, r, ctx, auth.Spec{
		Conf: mux.Vars(r)["conf"],
		Role: auth.RoleAdmin,
	})
}

// requireConfVolcoord gates a per-conf volunteer-admin route on the
// request's {conf} mux var. admin role implies volcoord, so a
// vienna-admin can also access vienna-volcoord paths.
func requireConfVolcoord(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) *auth.Identity {
	return auth.RequireRole(w, r, ctx, auth.Spec{
		Conf: mux.Vars(r)["conf"],
		Role: auth.RoleVolcoord,
	})
}

// requireConfStaff gates a per-conf staff route on the request's
// {conf} mux var. admin covers staff, so a vienna-admin can access
// any vienna-staff path. staff is the read-mostly tier — pages
// gated here surface info (run-of-show, speakers, registrations,
// hotels, downloads) but mutating actions like email blasts and
// calendar fan-outs stay behind requireConfAdmin.
func requireConfStaff(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) *auth.Identity {
	return auth.RequireRole(w, r, ctx, auth.Spec{
		Conf: mux.Vars(r)["conf"],
		Role: auth.RoleStaff,
	})
}

// requireGlobalAdmin gates a route that isn't scoped to a single
// conf (org list, missives DB, etc). Only a global-admin satisfies.
func requireGlobalAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) *auth.Identity {
	return auth.RequireRole(w, r, ctx, auth.Spec{Role: auth.RoleAdmin})
}
