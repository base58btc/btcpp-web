package handlers

import (
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

type OrgListPage struct {
	Orgs         []*types.Org
	FlashMessage string
	Year         uint
}

type OrgDetailPage struct {
	Org          *types.Org
	IsNew        bool
	FlashMessage string
	SpacesReady  bool
	Year         uint
}

type OrgNewPage struct {
	// ReturnTo is a same-site relative path the form re-submits as a
	// hidden field; OrgCreate redirects there after a successful save
	// so the admin lands back on the page they came from.
	ReturnTo     string
	FlashMessage string
	Year         uint
}

type SponsorshipsPage struct {
	Conf         *types.Conf
	Sponsorships []*types.Sponsorship
	Orgs         []*types.Org
	FlashMessage string
	Year         uint
}

func OrgList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	orgs, err := getters.ListOrgs(ctx)
	if err != nil {
		http.Error(w, "Unable to load orgs", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/orgs failed: %s", err.Error())
		return
	}

	sort.SliceStable(orgs, func(i, j int) bool {
		return orgs[i].Name < orgs[j].Name
	})

	err = ctx.TemplateCache.ExecuteTemplate(w, "sponsors/orgs.tmpl", &OrgListPage{
		Orgs:         orgs,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/orgs template failed: %s", err.Error())
	}
}

func OrgDetail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	params := mux.Vars(r)
	ref := params["ref"]

	if ref == "new" {
		err := ctx.TemplateCache.ExecuteTemplate(w, "sponsors/detail.tmpl", &OrgDetailPage{
			Org:         &types.Org{},
			IsNew:       true,
			SpacesReady: spaces.IsConfigured(),
			Year:        helpers.CurrentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page", http.StatusInternalServerError)
			ctx.Err.Printf("/admin/orgs/new template failed: %s", err.Error())
		}
		return
	}

	org, err := getters.GetOrg(ctx, ref)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "sponsors/detail.tmpl", &OrgDetailPage{
		Org:          org,
		FlashMessage: r.URL.Query().Get("flash"),
		SpacesReady:  spaces.IsConfigured(),
		Year:         helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/orgs/%s template failed: %s", ref, err.Error())
	}
}

// OrgNew renders the GET form for creating a new Org. Optional `return`
// query param (caller-supplied URL, must be relative to the site) tells
// OrgCreate where to redirect after a successful create — we round-trip
// it as a hidden form field so the POST handler can consume it.
func OrgNew(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	page := &OrgNewPage{
		ReturnTo:     safeReturnTo(r.URL.Query().Get("return")),
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "sponsors/org_new.tmpl", page); err != nil {
		ctx.Err.Printf("/admin/orgs/new render: %s", err)
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
	}
}

// OrgLogoUpload accepts a multipart `file` upload from the org form's
// inline "Upload a file" affordance, mirrors it to Spaces under
// sponsors/{shortID}{ext}, and returns the public URL as JSON
// `{url: "..."}` so the page JS can drop it into the URL input.
//
// Idempotent on identical file content via the shortID +
// spaces.Exists short-circuit, mirroring mirrorOrgLogoToSpaces.
// Gated to global-admin since /admin/orgs/* is.
func OrgLogoUpload(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	limitRequestBody(w, r, maxMultipartBodyBytes)
	raw, contentType, ext, err := readMultipartFile(r, "file")
	if err != nil {
		http.Error(w, "missing or unreadable file", http.StatusBadRequest)
		return
	}
	if !spaces.IsConfigured() {
		http.Error(w, "spaces not configured", http.StatusInternalServerError)
		return
	}
	shortID := imgproc.ShortID(raw)
	key := "sponsors/" + shortID + ext
	if !spaces.Exists(key) {
		if _, err := spaces.Upload(key, raw, contentType, ""); err != nil {
			ctx.Err.Printf("/admin/orgs/upload-logo: %s", err)
			http.Error(w, "upload failed", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": spaces.PublicURL(key)})
}

func OrgCreate(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	org := &types.Org{
		Name:      strings.TrimSpace(r.FormValue("Name")),
		Tagline:   strings.TrimSpace(r.FormValue("Tagline")),
		Email:     strings.TrimSpace(r.FormValue("Email")),
		Website:   strings.TrimSpace(r.FormValue("Website")),
		Twitter:   types.ParseTwitter(r.FormValue("Twitter")),
		Nostr:     strings.TrimSpace(r.FormValue("Nostr")),
		Matrix:    strings.TrimSpace(r.FormValue("Matrix")),
		LinkedIn:  strings.TrimSpace(r.FormValue("LinkedIn")),
		Instagram: strings.TrimSpace(r.FormValue("Instagram")),
		Youtube:   strings.TrimSpace(r.FormValue("Youtube")),
		Github:    strings.TrimSpace(r.FormValue("Github")),
		LogoLight: strings.TrimSpace(r.FormValue("LogoLight")),
		LogoDark:  strings.TrimSpace(r.FormValue("LogoDark")),
		Hiring:    r.FormValue("Hiring") == "on",
		Notes:     strings.TrimSpace(r.FormValue("Notes")),
	}
	trimOrg(org)

	if org.Name == "" {
		http.Error(w, "Org name is required", http.StatusBadRequest)
		return
	}

	_, err := getters.RegisterOrg(ctx, org)
	if err != nil {
		ctx.Err.Printf("/admin/orgs/new failed: %s", err.Error())
		http.Error(w, "Failed to create org", http.StatusInternalServerError)
		return
	}

	dest := safeReturnTo(r.FormValue("return"))
	if dest == "" {
		dest = "/admin/orgs"
	}
	dest = appendFlash(dest, "Org "+org.Name+" created")
	http.Redirect(w, r, dest, http.StatusFound)
}

func OrgSave(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	ref := strings.TrimSpace(mux.Vars(r)["ref"])
	if ref == "" {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	org := &types.Org{
		Ref:       ref,
		Name:      strings.TrimSpace(r.FormValue("Name")),
		Tagline:   strings.TrimSpace(r.FormValue("Tagline")),
		Email:     strings.TrimSpace(r.FormValue("Email")),
		Website:   strings.TrimSpace(r.FormValue("Website")),
		Twitter:   types.ParseTwitter(r.FormValue("Twitter")),
		Nostr:     strings.TrimSpace(r.FormValue("Nostr")),
		Matrix:    strings.TrimSpace(r.FormValue("Matrix")),
		LinkedIn:  strings.TrimSpace(r.FormValue("LinkedIn")),
		Instagram: strings.TrimSpace(r.FormValue("Instagram")),
		Youtube:   strings.TrimSpace(r.FormValue("Youtube")),
		Github:    strings.TrimSpace(r.FormValue("Github")),
		LogoLight: strings.TrimSpace(r.FormValue("LogoLight")),
		LogoDark:  strings.TrimSpace(r.FormValue("LogoDark")),
		Hiring:    r.FormValue("Hiring") == "on",
		Notes:     strings.TrimSpace(r.FormValue("Notes")),
	}
	trimOrg(org)

	if org.Name == "" {
		http.Error(w, "Org name is required", http.StatusBadRequest)
		return
	}

	if err := getters.UpdateOrgDetails(ctx, org); err != nil {
		ctx.Err.Printf("/admin/orgs/%s save failed: %s", ref, err)
		http.Error(w, "Failed to save org", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/orgs/"+url.PathEscape(ref)+"?flash="+url.QueryEscape("Org "+org.Name+" saved"), http.StatusFound)
}

// safeReturnTo accepts only same-site relative paths so the redirect
// can't be hijacked into an open-redirect against another origin.
func safeReturnTo(raw string) string {
	if raw == "" {
		return ""
	}
	// Must start with / and not //, must not contain a scheme.
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return ""
	}
	if strings.Contains(raw, ":") {
		return ""
	}
	return raw
}

// appendFlash adds a ?flash=… param to a URL, preserving any existing
// query string. Used so the redirect target's flash banner picks up.
func appendFlash(rawURL, msg string) string {
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + "flash=" + url.QueryEscape(msg)
}

func SponsorshipsList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	sponsorships, err := getters.ListSponsorships(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load sponsorships", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/sponsors failed: %s", conf.Tag, err.Error())
		return
	}

	orgs, err := getters.ListOrgs(ctx)
	if err != nil {
		http.Error(w, "Unable to load orgs", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/sponsors failed to load orgs: %s", conf.Tag, err.Error())
		return
	}

	sort.SliceStable(orgs, func(i, j int) bool {
		return orgs[i].Name < orgs[j].Name
	})

	err = ctx.TemplateCache.ExecuteTemplate(w, "sponsors/events.tmpl", &SponsorshipsPage{
		Conf:         conf,
		Sponsorships: sponsorships,
		Orgs:         orgs,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/sponsors template failed: %s", conf.Tag, err.Error())
	}
}

func SponsorshipCreate(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	orgRef := strings.TrimSpace(r.FormValue("OrgRef"))
	level := strings.TrimSpace(r.FormValue("Level"))

	if orgRef == "" || level == "" {
		http.Error(w, "Org and level are required", http.StatusBadRequest)
		return
	}

	org, _ := getters.GetOrg(ctx, orgRef)

	sp := &types.Sponsorship{
		Org:    org,
		Confs:  []*types.Conf{conf},
		Level:  level,
		Status: "Pending",
	}

	err = getters.RegisterSponsorship(ctx, sp)
	if err != nil {
		ctx.Err.Printf("/%s/admin/sponsors/new failed: %s", conf.Tag, err.Error())
		http.Error(w, "Failed to create sponsorship", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/"+conf.Tag+"/admin/sponsors"+"?flash=Sponsorship+created", http.StatusFound)
}
