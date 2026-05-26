package main

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
)

func validateHotelRows(hotels []*types.Hotel, confTagByRef map[string]string) error {
	for _, hotel := range hotels {
		if hotel == nil {
			continue
		}
		if strings.TrimSpace(hotel.Name) == "" {
			return fmt.Errorf("hotel %q has empty Name", hotel.ID)
		}
		if confTagByRef[hotel.ConfRef] == "" {
			return fmt.Errorf("hotel %q has unresolved conf ref", hotel.ID)
		}
	}
	return nil
}

func importHotelRows(ctx context.Context, pool *pgxpool.Pool, hotels []*types.Hotel, confTagByRef map[string]string) error {
	for _, hotel := range hotels {
		if hotel == nil {
			continue
		}
		confTag := confTagByRef[hotel.ConfRef]
		if _, err := pool.Exec(ctx, `
			INSERT INTO hotels (
				conference_id, name, url, img_path, type, description, display_order
			)
			SELECT c.id, $2, $3, $4, $5, $6, $7
			FROM conferences c
			WHERE c.tag = $1
		`, confTag, strings.TrimSpace(hotel.Name), hotel.URL, hotel.Img, hotel.Type, hotel.Desc, hotel.Order); err != nil {
			return fmt.Errorf("insert hotel %q: %w", hotel.ID, err)
		}
	}
	return nil
}

func validateHotels(ctx context.Context, pool *pgxpool.Pool, hotels []*types.Hotel) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hotels`).Scan(&count); err != nil {
		return fmt.Errorf("count hotels: %w", err)
	}
	if count < len(hotels) {
		return fmt.Errorf("postgres hotel count %d is less than Notion count %d", count, len(hotels))
	}
	return nil
}
