// Package auth replaces the legacy CheckPin (single shared secret in
// session storage) with a per-speaker role system backed by the
// Speakers DB's Roles multi-select column.
//
// Model:
//
//   - Each Speaker row has zero or more role tags in the Roles
//     multi-select. Each tag is "{scope}-{role}" where scope is a
//     conf tag ("vienna") or the literal "global", and role is one
//     of "admin" / "staff" / "volcoord".
//
//   - admin > volcoord and admin > staff at the same scope: a
//     vienna-admin can do anything a vienna-volcoord or vienna-staff
//     can do. staff and volcoord are orthogonal — neither covers
//     the other; users carrying both tags get the union.
//
//   - global-X grants the X role for every conf.
//
//   - Identity is established by clicking a magic link that carries
//     an HMAC of the user's email; the login handler stamps the
//     authed email into the session, so subsequent requests look up
//     the Speaker by that email and read fresh Roles from the cache
//     (revocation just-works on the next refresh tick).
//
// Handlers replace `helpers.CheckPin(...)` with
// `auth.RequireRole(w, r, ctx, auth.Spec{Conf: tag, Role: "admin"})`
// — non-nil identity returned means the request can proceed.
package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

// SessionEmailKey is the SCS session key under which the validated
// email lives once a magic-link click succeeds.
const SessionEmailKey = "auth_email"

// GlobalScope is the scope tag that means "every conf".
const GlobalScope = "global"

// RoleAdmin / RoleStaff / RoleVolcoord are the role names supported.
//
// Coverage hierarchy (see covers): admin > staff, admin > volcoord.
// staff and volcoord are independent — having one does NOT imply the
// other; a user with both gets the union of their permissions.
const (
	RoleAdmin    = "admin"
	RoleStaff    = "staff"
	RoleVolcoord = "volcoord"
)

// Role is one parsed entry from a Speaker's Roles multi-select.
type Role struct {
	Scope string // conf tag, or GlobalScope
	Name  string // RoleAdmin or RoleVolcoord
}

// Spec is the role requirement for a given handler. Conf is a conf
// tag the request is scoped to ("" means the request isn't tied to a
// specific conf, in which case only a global role can satisfy it).
// Role is the minimum role name required.
type Spec struct {
	Conf string
	Role string
}

// Identity is the authed user, resolved on each request.
type Identity struct {
	Email   string
	Speaker *types.Speaker
	Roles   []Role
}

// Satisfies returns true when this identity has at least one role
// covering the spec.
//
// Coverage rules:
//   - Scope: a global-scoped role covers any conf; a conf-scoped role
//     only covers spec.Conf when the conf matches. spec.Conf == ""
//     requires a global-scoped role (since there's no specific conf
//     to match against).
//   - Role: an admin role covers volcoord at the same scope. Other
//     roles match only their own name.
func (id *Identity) Satisfies(spec Spec) bool {
	if id == nil {
		return false
	}
	for _, r := range id.Roles {
		if !covers(r.Name, spec.Role) {
			continue
		}
		if r.Scope == GlobalScope {
			return true
		}
		if spec.Conf != "" && r.Scope == spec.Conf {
			return true
		}
	}
	return false
}

// AdminConfTags returns the set of conf tags this identity can admin
// (or volcoord). Used by the dashboard to surface "Admin" buttons on
// conf cards. globalAdmin / globalVolcoord break out the
// global-scoped roles for callers that need to show "every conf"
// affordances.
func (id *Identity) AdminConfTags() (confs []string, globalAdmin, globalVolcoord bool) {
	if id == nil {
		return nil, false, false
	}
	seen := make(map[string]bool)
	for _, r := range id.Roles {
		if r.Scope == GlobalScope {
			if r.Name == RoleAdmin {
				globalAdmin = true
			}
			if r.Name == RoleVolcoord {
				globalVolcoord = true
			}
			continue
		}
		if !seen[r.Scope] {
			seen[r.Scope] = true
			confs = append(confs, r.Scope)
		}
	}
	return confs, globalAdmin, globalVolcoord
}

// HasRoleForConf reports whether the identity holds the given role
// (or admin, which implies volcoord) for the given conf tag.
func (id *Identity) HasRoleForConf(confTag, role string) bool {
	return id.Satisfies(Spec{Conf: confTag, Role: role})
}

// IsGlobalAdmin is shorthand for the global-admin special role, used
// to gate the role-management panel on the dashboard.
func (id *Identity) IsGlobalAdmin() bool {
	return id.Satisfies(Spec{Role: RoleAdmin})
}

// covers checks whether `have` is enough for `want`. admin covers
// both volcoord and staff; otherwise names must match exactly. staff
// and volcoord do NOT cover each other — they're orthogonal slices
// of permissions, and a user needs both tags to get both.
func covers(have, want string) bool {
	if have == want {
		return true
	}
	if have == RoleAdmin && (want == RoleVolcoord || want == RoleStaff) {
		return true
	}
	return false
}

// ParseRoles turns the raw Notion multi-select tags ("vienna-admin",
// "global-volcoord", ...) into structured Role values. Tags that
// don't look like "<scope>-<role>" or carry an unknown role name are
// dropped silently — the source of truth is the Notion UI, and
// dropping unknown values fails closed.
func ParseRoles(tags []string) []Role {
	var out []Role
	for _, t := range tags {
		t = strings.TrimSpace(t)
		idx := strings.LastIndex(t, "-")
		if idx <= 0 || idx == len(t)-1 {
			continue
		}
		scope := t[:idx]
		name := t[idx+1:]
		if name != RoleAdmin && name != RoleVolcoord && name != RoleStaff {
			continue
		}
		if scope == "" {
			continue
		}
		out = append(out, Role{Scope: scope, Name: name})
	}
	return out
}

// LoginEmail stamps the authed email into the session. Called by the
// magic-link landing handler after the email HMAC checks out.
func LoginEmail(ctx *config.AppContext, r *http.Request, email string) error {
	if email == "" {
		return errors.New("LoginEmail: empty email")
	}
	if err := ctx.Session.RenewToken(r.Context()); err != nil {
		return fmt.Errorf("renew session: %w", err)
	}
	ctx.Session.Put(r.Context(), SessionEmailKey, email)
	return nil
}

// Logout drops the authed email from the session.
func Logout(ctx *config.AppContext, r *http.Request) {
	ctx.Session.Remove(r.Context(), SessionEmailKey)
}

// Resolve looks up the current request's Identity. Returns nil
// (no error) when there's no authed email in the session — callers
// should treat that as "not logged in." A non-nil error means the
// authed email exists but the lookup misfired.
func Resolve(r *http.Request, ctx *config.AppContext) (*Identity, error) {
	email := ctx.Session.GetString(r.Context(), SessionEmailKey)
	if email == "" {
		return nil, nil
	}
	speakers, err := getters.GetSpeakersByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("lookup speaker: %w", err)
	}
	if len(speakers) == 0 {
		// Authed email no longer matches a Speaker row — treat as
		// logged out so they re-enter their email and we either find
		// them or surface a real error.
		return nil, nil
	}
	sp := speakers[0]
	return &Identity{
		Email:   email,
		Speaker: sp,
		Roles:   ParseRoles(sp.Roles),
	}, nil
}

// RequireRole is the per-handler entry point. Resolves the identity,
// redirects unauth'd requests to /login?next=<current path>, returns
// 403 for authed-but-insufficient. Non-nil return means the handler
// can proceed.
func RequireRole(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, spec Spec) *Identity {
	id, err := Resolve(r, ctx)
	if err != nil {
		ctx.Err.Printf("auth resolve %s: %s", r.URL.Path, err)
	}
	if id == nil {
		next := r.URL.RequestURI()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
		return nil
	}
	if !id.Satisfies(spec) {
		ctx.Infos.Printf("auth deny %s for %s wants %+v has %+v", r.URL.Path, id.Email, spec, id.Roles)
		// Authed but insufficient — bounce to the dashboard with a
		// red error banner instead of a bare 403 page. The user
		// stays signed in; they just don't have permission for the
		// thing they tried to reach.
		msg := "You don't have access to that page. If you think you should, ask a global-admin to add the role."
		http.Redirect(w, r, "/dashboard?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return nil
	}
	return id
}

// RequireOptional is for pages that render different content for
// authed vs unauthed users. Returns nil + no error when not logged
// in; never redirects.
func RequireOptional(r *http.Request, ctx *config.AppContext) *Identity {
	id, err := Resolve(r, ctx)
	if err != nil {
		ctx.Err.Printf("auth resolve %s: %s", r.URL.Path, err)
		return nil
	}
	return id
}

// SafeNext returns next if it's a relative path rooted at "/" and not
// a protocol-relative URL ("//evil.example.com"). Anything else
// collapses to fallback.
func SafeNext(next, fallback string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return fallback
	}
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return fallback
	}
	return next
}

// MagicLink builds the URL the login email points at: /auth?em=&hr=&next=.
// Clicking it stamps the email into the session and redirects to
// the validated next path.
func MagicLink(ctx *config.AppContext, email, next string) string {
	u, err := url.Parse(ctx.Env.GetURI())
	if err != nil {
		return ""
	}
	u.Path = "/auth"
	q := u.Query()
	q.Set("em", base64.RawURLEncoding.EncodeToString([]byte(email)))
	q.Set("hr", base64.RawURLEncoding.EncodeToString([]byte(helpers.CreateEmailHMACTTL(ctx, email, helpers.LoginEmailLinkTTL))))
	if next != "" {
		q.Set("next", next)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// AuthRedirect handles the magic-link click. Validates the HMAC +
// email, stamps the session, then redirects to a sanitized `next`
// (default "/dashboard").
func AuthRedirect(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	q := r.URL.Query()
	encEmail := q.Get("em")
	encHMAC := q.Get("hr")
	if encEmail == "" || encHMAC == "" {
		redirectLoginError(w, r, "That login link is missing required information. Enter your email to get a fresh link.")
		return
	}
	emailB, err := base64.RawURLEncoding.DecodeString(encEmail)
	if err != nil {
		redirectLoginError(w, r, "That login link is malformed. Enter your email to get a fresh link.")
		return
	}
	hmacB, err := base64.RawURLEncoding.DecodeString(encHMAC)
	if err != nil {
		redirectLoginError(w, r, "That login link is malformed. Enter your email to get a fresh link.")
		return
	}
	email := string(emailB)
	if !helpers.VerifyEmailHMAC(ctx, string(hmacB), email) {
		redirectLoginError(w, r, "That login link has expired or is invalid. Enter your email to get a fresh link.")
		return
	}
	if err := LoginEmail(ctx, r, email); err != nil {
		ctx.Err.Printf("/auth login %s: %s", email, err)
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	dest := SafeNext(q.Get("next"), "/dashboard")
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func redirectLoginError(w http.ResponseWriter, r *http.Request, message string) {
	next := SafeNext(r.URL.Query().Get("next"), "/dashboard")
	dest := "/login?next=" + url.QueryEscape(next) + "&error=" + url.QueryEscape(message)
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
