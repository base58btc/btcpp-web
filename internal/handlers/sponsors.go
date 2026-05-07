package handlers

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
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
	Conf          *types.Conf
	Sponsorships  []*types.Sponsorship
	Orgs          []*types.Org
	FlashMessage  string
	Year          uint
}

func OrgList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	orgs, err := getters.ListOrgs(ctx.Notion)
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
			Org:   &types.Org{},
			IsNew: true,
			Year:  helpers.CurrentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page", http.StatusInternalServerError)
			ctx.Err.Printf("/admin/orgs/new template failed: %s", err.Error())
		}
		return
	}

	org, err := getters.GetOrg(ctx.Notion, ref)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "sponsors/detail.tmpl", &OrgDetailPage{
		Org:          org,
		FlashMessage: r.URL.Query().Get("flash"),
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

func OrgCreate(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	r.ParseForm()

	org := &types.Org{
		Name:      r.FormValue("Name"),
		Tagline:   r.FormValue("Tagline"),
		Email:     r.FormValue("Email"),
		Website:   r.FormValue("Website"),
		Twitter:   types.ParseTwitter(r.FormValue("Twitter")),
		Nostr:     r.FormValue("Nostr"),
		Matrix:    r.FormValue("Matrix"),
		LinkedIn:  r.FormValue("LinkedIn"),
		Instagram: r.FormValue("Instagram"),
		Youtube:   r.FormValue("Youtube"),
		Github:    r.FormValue("Github"),
		LogoLight: r.FormValue("LogoLight"),
		LogoDark:  r.FormValue("LogoDark"),
		Hiring:    r.FormValue("Hiring") == "on",
		Notes:     r.FormValue("Notes"),
	}

	if org.Name == "" {
		http.Error(w, "Org name is required", http.StatusBadRequest)
		return
	}

	_, err := getters.RegisterOrg(ctx.Notion, org)
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

	orgs, err := getters.ListOrgs(ctx.Notion)
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

	r.ParseForm()

	orgRef := r.FormValue("OrgRef")
	level := r.FormValue("Level")

	if orgRef == "" || level == "" {
		http.Error(w, "Org and level are required", http.StatusBadRequest)
		return
	}

	org, _ := getters.GetOrg(ctx.Notion, orgRef)

	sp := &types.Sponsorship{
		Org:    org,
		Confs:  []*types.Conf{conf},
		Level:  level,
		Status: "Pending",
	}

	err = getters.RegisterSponsorship(ctx.Notion, sp)
	if err != nil {
		ctx.Err.Printf("/%s/admin/sponsors/new failed: %s", conf.Tag, err.Error())
		http.Error(w, "Failed to create sponsorship", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/" + conf.Tag + "/admin/sponsors"+"?flash=Sponsorship+created", http.StatusFound)
}
