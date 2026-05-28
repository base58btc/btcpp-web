package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func listHotelsPostgres(ctx *config.AppContext) ([]*types.Hotel, error) {
	if ctx == nil || ctx.DB == nil {
		return nil, fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	rows, err := ctx.DB.Query(context.Background(), `
		SELECT hotels.id::text, hotels.conference_id::text, hotels.name, hotels.url,
			hotels.img_path, hotels.type, hotels.description, hotels.display_order
		FROM hotels
		JOIN conferences ON conferences.id = hotels.conference_id
		WHERE hotels.archived_at IS NULL
		ORDER BY conferences.start_date NULLS LAST, hotels.display_order, hotels.name
	`)
	if err != nil {
		return nil, fmt.Errorf("query hotels: %w", err)
	}
	defer rows.Close()

	var hotels []*types.Hotel
	for rows.Next() {
		var hotel types.Hotel
		err := rows.Scan(
			&hotel.ID,
			&hotel.ConfRef,
			&hotel.Name,
			&hotel.URL,
			&hotel.Img,
			&hotel.Type,
			&hotel.Desc,
			&hotel.Order,
		)
		if err != nil {
			return nil, fmt.Errorf("scan hotel: %w", err)
		}
		hotels = append(hotels, &hotel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate hotels: %w", err)
	}
	return hotels, nil
}
