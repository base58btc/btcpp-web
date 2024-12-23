package config

import (
	"html/template"
	"log"

	"github.com/alexedwards/scs/v2"
	"github.com/base58btc/btcpp-web/internal/types"
)

/* application configuration settings */
type AppContext struct {
	Env    *types.EnvConfig
	Notion *types.Notion

	InProduction  bool
	Err           *log.Logger
	Infos         *log.Logger
	Session       *scs.SessionManager
	TemplateCache map[string]*template.Template
	Confs         []*types.Conf
}
