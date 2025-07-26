package missives

import (
	"net/http"

	"btcpp-web/internal/config"
)

func checkPin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) bool {
	pin := ctx.Session.GetString(r.Context(), "pin")
	if pin == "" {
		w.Header().Set("x-missing-field", "pin")
		w.WriteHeader(http.StatusUnauthorized)
		ctx.Infos.Printf("401 login failed: %s", r.URL.Path)
		return false
	}
	return pin == ctx.Env.RegistryPin
}
