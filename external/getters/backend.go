package getters

import (
	"fmt"
	"strings"

	"btcpp-web/internal/config"
)

const (
	dataBackendNotion   = "notion"
	dataBackendPostgres = "postgres"
)

func usePostgresBackend(ctx *config.AppContext) bool {
	if ctx == nil || ctx.Env == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(ctx.Env.DataBackend), dataBackendPostgres)
}

func unsupportedPostgresBackend(name string) error {
	return fmt.Errorf("postgres backend not implemented for %s", name)
}
