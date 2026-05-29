package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
)

func siteStatsAttendeesPostgres(ctx *config.AppContext) (int, error) {
	if ctx == nil || ctx.DB == nil {
		return 0, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	var count int
	if err := ctx.DB.QueryRow(context.Background(), `
		SELECT count(*)
		FROM registrations
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count registrations: %w", err)
	}
	return count, nil
}
