package handlers

import (
	"net/http"

	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
)

type GlobalAdminDashboardPage struct {
	FlashMessage string
	Year         uint
}

func GlobalAdminDashboard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/dashboard.tmpl", &GlobalAdminDashboardPage{
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	}); err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/admin template failed: %s", err)
	}
}
