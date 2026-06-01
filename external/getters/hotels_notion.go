package getters

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func ListHotelsNotion(n *types.Notion) ([]*types.Hotel, error) {
	var hotels []*types.Hotel

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.HotelsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			hotel := parseHotel(page.ID, page.Properties)
			hotels = append(hotels, hotel)
		}
	}

	return hotels, nil
}

func createHotelNotion(n *types.Notion, in HotelInput) (string, error) {
	props := hotelWriteProps(in, true)
	parent := notion.NewDatabaseParent(n.Config.HotelsDb)
	page, err := n.Client.CreatePage(context.Background(), parent, props)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

func updateHotelNotion(ctx *config.AppContext, hotelID string, in HotelInput) error {
	props := hotelWriteProps(in, false)
	if len(props) == 0 {
		return nil
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), hotelID, props)
	return err
}

func archiveHotelNotion(ctx *config.AppContext, hotelID string) error {
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

func hotelWriteProps(in HotelInput, isCreate bool) map[string]*notion.PropertyValue {
	props := map[string]*notion.PropertyValue{
		// Order is the only field always written. Zero is a real
		// valid display rank, not a "leave it alone" sentinel.
		"Order": numberValue(float64(in.Order)),
	}
	if isCreate {
		// Title and conf-relation are required only at create
		// time. On update they're optional.
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
