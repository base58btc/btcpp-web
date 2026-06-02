package getters

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func createHotelPostgres(ctx *config.AppContext, in HotelInput) (string, error) {
	if ctx == nil || ctx.DB == nil {
		return "", fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if strings.TrimSpace(in.ConfRef) == "" {
		return "", fmt.Errorf("CreateHotel: ConfRef is required")
	}
	var hotelID string
	err := ctx.DB.QueryRow(context.Background(), `
		INSERT INTO hotels (
			conference_id, name, url, img_path, type, description, display_order
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		RETURNING id::text
	`, in.ConfRef, in.Name, in.URL, in.Img, in.Type, in.Desc, in.Order).Scan(&hotelID)
	if err != nil {
		return "", fmt.Errorf("insert hotel %q: %w", in.Name, err)
	}
	RefreshHotelsCache()
	return hotelID, nil
}

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

func updateHotelPostgres(ctx *config.AppContext, hotelID string, in HotelInput) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	if strings.TrimSpace(hotelID) == "" {
		return fmt.Errorf("UpdateHotel: hotelID is required")
	}
	setParts := []string{"display_order = $2", "url = $3", "img_path = $4", "type = $5", "description = $6"}
	args := []interface{}{hotelID, in.Order, in.URL, in.Img, in.Type, in.Desc}
	if in.Name != "" {
		args = append(args, in.Name)
		setParts = append(setParts, fmt.Sprintf("name = $%d", len(args)))
	}
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE hotels
		SET `+strings.Join(setParts, ", ")+`
		WHERE id = $1
	`, args...)
	if err != nil {
		return fmt.Errorf("update hotel %s: %w", hotelID, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("hotel %s not found", hotelID)
	}
	RefreshHotelsCache()
	return nil
}

func archiveHotelPostgres(ctx *config.AppContext, hotelID string) error {
	if ctx == nil || ctx.DB == nil {
		return fmt.Errorf("postgres backend selected but AppContext.DB is nil")
	}
	commandTag, err := ctx.DB.Exec(context.Background(), `
		UPDATE hotels
		SET archived_at = now()
		WHERE id = $1
	`, hotelID)
	if err != nil {
		return fmt.Errorf("archive hotel %s: %w", hotelID, err)
	}
	if commandTag.RowsAffected() == 0 {
		return fmt.Errorf("hotel %s not found", hotelID)
	}
	RefreshHotelsCache()
	return nil
}
