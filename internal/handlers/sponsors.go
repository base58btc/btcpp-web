package handlers

import (
	"net/http"
	"sort"

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

type SponsorshipsPage struct {
	Conf          *types.Conf
	Sponsorships  []*types.Sponsorship
	Orgs          []*types.Org
	FlashMessage  string
	Year          uint
}

func OrgList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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

func OrgCreate(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	r.ParseForm()

	org := &types.Org{
		Name:      r.FormValue("Name"),
		Email:     r.FormValue("Email"),
		Website:   r.FormValue("Website"),
		Twitter:   r.FormValue("Twitter"),
		Nostr:     r.FormValue("Nostr"),
		Matrix:    r.FormValue("Matrix"),
		LinkedIn:  r.FormValue("LinkedIn"),
		Instagram: r.FormValue("Instagram"),
		Youtube:   r.FormValue("Youtube"),
		Github:      r.FormValue("Github"),
		Notes:       r.FormValue("Notes"),
	}

	if org.Name == "" {
		http.Error(w, "Org name is required", http.StatusBadRequest)
		return
	}

	err := getters.RegisterOrg(ctx.Notion, org)
	if err != nil {
		ctx.Err.Printf("/admin/orgs/new failed: %s", err.Error())
		http.Error(w, "Failed to create org", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/orgs?flash=Org+created", http.StatusFound)
}

func SponsorshipsList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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
		ctx.Err.Printf("/admin/sponsors/%s failed: %s", conf.Tag, err.Error())
		return
	}

	orgs, err := getters.ListOrgs(ctx.Notion)
	if err != nil {
		http.Error(w, "Unable to load orgs", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/sponsors/%s failed to load orgs: %s", conf.Tag, err.Error())
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
		ctx.Err.Printf("/admin/sponsors/%s template failed: %s", conf.Tag, err.Error())
	}
}

func SponsorshipCreate(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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
		ctx.Err.Printf("/admin/sponsors/%s/new failed: %s", conf.Tag, err.Error())
		http.Error(w, "Failed to create sponsorship", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/sponsors/"+conf.Tag+"?flash=Sponsorship+created", http.StatusFound)
}
