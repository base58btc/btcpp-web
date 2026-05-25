package config

import (
	htmltemplate "html/template"
	"log"
	texttemplate "text/template"

	"btcpp-web/internal/types"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

/* application configuration settings */
type AppContext struct {
	Env    *types.EnvConfig
	Notion *types.Notion
	DB     *pgxpool.Pool

	InProduction  bool
	Err           *log.Logger
	Infos         *log.Logger
	Session       *scs.SessionManager
	TemplateCache *htmltemplate.Template
	EmailCache    map[string]*texttemplate.Template
}
