package getters

import (
	"context"
	"strings"

	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func ListSpeakersNotion(n *types.Notion) ([]*types.Speaker, error) {
	var speakers []*types.Speaker

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.SpeakersDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			speaker := parseSpeaker(page.ID, page.Properties)
			speakers = append(speakers, speaker)
		}
	}

	return speakers, nil
}

func getSpeakersByEmailNotion(n *types.Notion, email string) ([]*types.Speaker, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, nil
	}
	if cached := cacheSpeakers; len(cached) > 0 {
		var hits []*types.Speaker
		for _, s := range cached {
			if s != nil && strings.EqualFold(strings.TrimSpace(s.Email), email) {
				s.Email = strings.TrimSpace(s.Email)
				hits = append(hits, s)
			}
		}
		if len(hits) > 0 {
			return hits, nil
		}
	}
	var speakers []*types.Speaker
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.SpeakersDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "Email",
				Text: &notion.TextFilterCondition{
					Contains: email,
				},
			},
		})
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		speaker := parseSpeaker(page.ID, page.Properties)
		if speaker != nil && strings.EqualFold(strings.TrimSpace(speaker.Email), email) {
			speakers = append(speakers, speaker)
		}
	}
	return speakers, nil
}

func createSpeakerNotion(n *types.Notion, in SpeakerInput) (string, error) {
	in = normalizeSpeakerInput(in)
	parent := notion.NewDatabaseParent(n.Config.SpeakersDb)
	page, err := n.Client.CreatePage(context.Background(), parent, speakerCreateProps(in))
	if err != nil {
		return "", err
	}
	CacheSpeakerInsert(speakerFromInput(page.ID, in))
	return page.ID, nil
}

func updateSpeakerNotion(n *types.Notion, speakerID string, up SpeakerUpdate) error {
	up = normalizeSpeakerUpdate(up)
	props := speakerUpdateProps(up)
	if len(props) == 0 {
		return nil
	}
	if _, err := n.Client.UpdatePageProperties(context.Background(), speakerID, props); err != nil {
		return err
	}
	patchCachedSpeaker(speakerID, up)
	return nil
}

func fetchSpeakerByIDNotion(n *types.Notion, speakerID string) (*types.Speaker, error) {
	speakerID = strings.TrimSpace(speakerID)
	if speakerID == "" {
		return nil, nil
	}
	for _, s := range cacheSpeakers {
		if s != nil && s.ID == speakerID {
			return s, nil
		}
	}
	page, err := n.Client.RetrievePage(context.Background(), speakerID)
	if err != nil {
		return nil, err
	}
	return parseSpeaker(page.ID, page.Properties), nil
}

func updateSpeakerRolesNotion(n *types.Notion, speakerID string, roles []string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), speakerID,
		map[string]*notion.PropertyValue{
			"Roles": multiSelectValue(roles),
		})
	if err != nil {
		return err
	}
	for _, s := range cacheSpeakers {
		if s != nil && s.ID == speakerID {
			s.Roles = append(s.Roles[:0], roles...)
			break
		}
	}
	return nil
}

func speakerCreateProps(in SpeakerInput) map[string]*notion.PropertyValue {
	props := map[string]*notion.PropertyValue{
		"Name":          titleValue(in.Name),
		"Email":         notion.NewEmailPropertyValue(in.Email),
		"AvailToHire":   checkboxValue(in.AvailToHire),
		"LookingToHire": checkboxValue(in.LookingToHire),
	}
	if in.Photo != "" {
		props["NormPhoto"] = richTextValue(in.Photo)
	}
	if in.Phone != "" {
		props["Phone"] = richTextValue(in.Phone)
	}
	if in.Signal != "" {
		props["Signal"] = richTextValue(in.Signal)
	}
	if in.Telegram != "" {
		props["Telegram"] = richTextValue(in.Telegram)
	}
	if in.Twitter != "" {
		props["Twitter"] = richTextValue(in.Twitter)
	}
	if in.Nostr != "" {
		props["npub"] = richTextValue(in.Nostr)
	}
	if in.Github != "" {
		props["Github"] = notion.NewURLPropertyValue(in.Github)
	}
	if in.Instagram != "" {
		props["Instagram"] = richTextValue(in.Instagram)
	}
	if in.LinkedIn != "" {
		props["LinkedIn"] = richTextValue(in.LinkedIn)
	}
	if in.Website != "" {
		props["Website"] = notion.NewURLPropertyValue(in.Website)
	}
	if in.Company != "" {
		props["Company"] = richTextValue(in.Company)
	}
	if in.OrgLogo != "" {
		props["OrgPhoto"] = richTextValue(in.OrgLogo)
	}
	if in.TShirt != "" {
		props["TShirt"] = selectValue(in.TShirt)
	}
	return props
}

func speakerUpdateProps(up SpeakerUpdate) map[string]*notion.PropertyValue {
	props := map[string]*notion.PropertyValue{}
	if up.Photo != "" {
		props["NormPhoto"] = richTextValue(up.Photo)
	}
	if up.Phone != "" {
		props["Phone"] = richTextValue(up.Phone)
	}
	if up.Signal != "" {
		props["Signal"] = richTextValue(up.Signal)
	}
	if up.Telegram != "" {
		props["Telegram"] = richTextValue(up.Telegram)
	}
	if up.Twitter != "" {
		props["Twitter"] = richTextValue(up.Twitter)
	}
	if up.Nostr != "" {
		props["npub"] = richTextValue(up.Nostr)
	}
	if up.Github != "" {
		props["Github"] = notion.NewURLPropertyValue(up.Github)
	}
	if up.Instagram != "" {
		props["Instagram"] = richTextValue(up.Instagram)
	}
	if up.LinkedIn != "" {
		props["LinkedIn"] = richTextValue(up.LinkedIn)
	}
	if up.Website != "" {
		props["Website"] = notion.NewURLPropertyValue(up.Website)
	}
	if up.Company != "" {
		props["Company"] = richTextValue(up.Company)
	}
	if up.OrgLogo != "" {
		props["OrgPhoto"] = richTextValue(up.OrgLogo)
	}
	if up.TShirt != "" {
		props["TShirt"] = selectValue(up.TShirt)
	}
	return props
}
