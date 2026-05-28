package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	notion "github.com/niftynei/go-notion"
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
func CreateHotel(n *types.Notion, in HotelInput) (string, error) {
	if in.ConfRef == "" {
		return "", fmt.Errorf("CreateHotel: ConfRef is required")
	}
	props := hotelWriteProps(in, true)
	parent := notion.NewDatabaseParent(n.Config.HotelsDb)
	page, err := n.Client.CreatePage(context.Background(), parent, props)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

// UpdateHotel patches an existing Hotel row. Empty fields are left
// untouched on update so a partial form post doesn't accidentally
// blank a field the admin didn't intend to clear.
func UpdateHotel(ctx *config.AppContext, hotelID string, in HotelInput) error {
	props := hotelWriteProps(in, false)
	if len(props) == 0 {
		return nil
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), hotelID, props)
	return err
}

// ArchiveHotel soft-deletes a Hotel row (Notion archive — recoverable
// from the trash for 30 days). Goes through raw HTTP PATCH because
// the go-notion library doesn't expose the `archived` flag on its
// UpdatePageProperties wrapper, mirroring DeleteConfTalk.
func ArchiveHotel(ctx *config.AppContext, hotelID string) error {
	body, err := json.Marshal(map[string]interface{}{"archived": true})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("PATCH",
		"https://api.notion.com/v1/pages/"+hotelID,
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ctx.Notion.Config.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Notion-Version", "2022-06-28")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var errResp map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("notion archive hotel %s: %v", hotelID, errResp)
	}
	return nil
}

// RefreshHotelsCache forces the next FetchHotelsCached call to fetch
// fresh data from Notion. Called after every CRUD op so the
// /{conf}/admin/hotels page reflects edits immediately.
func RefreshHotelsCache() {
	queueRefresh(JobHotels)
}

func hotelWriteProps(in HotelInput, isCreate bool) map[string]*notion.PropertyValue {
	props := map[string]*notion.PropertyValue{
		// Order is the only field always written. Zero is a real
		// valid display rank — not a "leave it alone" sentinel.
		"Order": numberValue(float64(in.Order)),
	}
	if isCreate {
		// Title and conf-relation are required only at create
		// time. On update they're optional (admin might just be
		// changing the URL or the order).
		props["Name"] = titleValue(in.Name)
		props["conf"] = relationValue([]string{in.ConfRef})
	} else if in.Name != "" {
		props["Name"] = titleValue(in.Name)
	}
	if in.URL != "" || !isCreate {
		props["URL"] = notion.NewURLPropertyValue(in.URL)
	}
	if in.Img != "" || !isCreate {
		props["Img"] = richTextValue(in.Img)
	}
	if in.Type != "" || !isCreate {
		props["Type"] = richTextValue(in.Type)
	}
	if in.Desc != "" || !isCreate {
		props["Desc"] = richTextValue(in.Desc)
	}
	return props
}
