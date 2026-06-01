package getters

import (
	"fmt"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// HotelInput is the shape every Hotel write goes through. Empty
// strings mean "leave the field unset" on create and "clear it" on
// update — Notion-side validation is loose, mirroring how
// ProposalInput is handled. Order is always written (zero is a
// real, valid display rank).
type HotelInput struct {
	Name    string
	URL     string
	Img     string // bare Spaces path, e.g. "vienna/hotels/abc.jpg"
	Type    string
	Desc    string
	Order   int
	ConfRef string // Conference page ID for the `conf` relation
}

func getHotels(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting hotels...")
	if UsePostgresBackend(ctx) {
		hotels, err = listHotelsPostgres(ctx)
	} else {
		hotels, err = ListHotelsNotion(ctx.Notion)
	}

	if err != nil {
		ctx.Err.Printf("error fetching hotels %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d hotels!", len(hotels))
		writeCache("hotels", hotels)
	}
}

/* This may return nil */
func FetchHotelsCached(ctx *config.AppContext) ([]*types.Hotel, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if hotels == nil || lastHotelFetch.Before(deadline) {
		lastHotelFetch = time.Now()
		queueRefresh(JobHotels)
	}

	return hotels, nil
}

func ListHotels(n *types.Notion) ([]*types.Hotel, error) {
	return ListHotelsNotion(n)
}

// CreateHotel inserts a new row into the Hotels DB and returns the
// new page ID. ConfRef is required (no orphan hotels); everything
// else is optional and gets written when non-empty.
func CreateHotel(ctx *config.AppContext, in HotelInput) (string, error) {
	if UsePostgresBackend(ctx) {
		return "", unsupportedPostgresBackend("CreateHotel")
	}
	if in.ConfRef == "" {
		return "", fmt.Errorf("CreateHotel: ConfRef is required")
	}
	return createHotelNotion(ctx.Notion, in)
}

// UpdateHotel patches an existing Hotel row. Empty fields are left
// untouched on update so a partial form post doesn't accidentally
// blank a field the admin didn't intend to clear.
func UpdateHotel(ctx *config.AppContext, hotelID string, in HotelInput) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("UpdateHotel")
	}
	return updateHotelNotion(ctx, hotelID, in)
}

// ArchiveHotel soft-deletes a Hotel row (Notion archive — recoverable
// from the trash for 30 days). Goes through raw HTTP PATCH because
// the go-notion library doesn't expose the `archived` flag on its
// UpdatePageProperties wrapper, mirroring DeleteConfTalk.
func ArchiveHotel(ctx *config.AppContext, hotelID string) error {
	if UsePostgresBackend(ctx) {
		return unsupportedPostgresBackend("ArchiveHotel")
	}
	return archiveHotelNotion(ctx, hotelID)
}

// RefreshHotelsCache forces the next FetchHotelsCached call to fetch
// fresh data from Notion. Called after every CRUD op so the
// /{conf}/admin/hotels page reflects edits immediately.
func RefreshHotelsCache() {
	queueRefresh(JobHotels)
}
