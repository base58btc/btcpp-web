package config

import (
	"html/template"
	"log"

	"btcpp-web/internal/types"
	"github.com/alexedwards/scs/v2"
)

/* application configuration settings */
type AppContext struct {
	Env    *types.EnvConfig
	Notion *types.Notion

	InProduction  bool
	Err           *log.Logger
	Infos         *log.Logger
	Session       *scs.SessionManager
	TemplateCache *template.Template
	EmailCache    map[string]*template.Template
}
