package getters

import (
	"context"

	"btcpp-web/internal/types"

	notion "github.com/niftynei/go-notion"
)

// SpeakerInput is the data needed to create a Speakers DB row from a TalkApp.
// All string fields are written as-is; empty strings produce empty Notion
// properties (which is fine for new records).
type SpeakerInput struct {
	Name          string
	Email         string
	Photo         string
	Phone         string
	Signal        string
	Telegram      string
	Twitter       string
	Nostr         string
	Github        string
	Instagram     string
	LinkedIn      string
	Website       string
	Company       string
	OrgLogo       string // bare filename — written to Notion property "OrgPhoto"
	AvailToHire   bool
	LookingToHire bool
	TShirt        string // Notion select, e.g. "MM" / "LM" — see validShirtCode
}

// SpeakerUpdate is a sparse update for an existing Speakers row. Empty strings
// mean "leave this field alone".
type SpeakerUpdate struct {
	Photo     string
	Phone     string
	Signal    string
	Telegram  string
	Twitter   string
	Nostr     string
	Github    string
	Instagram string
	LinkedIn  string
	Website   string
	Company   string
	OrgLogo   string
	TShirt    string
}

// GetSpeakersByEmail returns every Speaker page whose Email property matches
// `email` exactly. Caller is responsible for deciding what to do when 0, 1,
// or many are returned.
func GetSpeakersByEmail(n *types.Notion, email string) ([]*types.Speaker, error) {
	var speakers []*types.Speaker
	pages, _, _, err := n.Client.QueryDatabase(context.Background(),
		n.Config.SpeakersDb, notion.QueryDatabaseParam{
			Filter: &notion.Filter{
				Property: "Email",
				Text: &notion.TextFilterCondition{
					Equals: email,
				},
			},
		})
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		speakers = append(speakers, parseSpeaker(page.ID, page.Properties))
	}
	return speakers, nil
}

func CreateSpeaker(n *types.Notion, in SpeakerInput) (string, error) {
	parent := notion.NewDatabaseParent(n.Config.SpeakersDb)
	page, err := n.Client.CreatePage(context.Background(), parent, speakerCreateProps(in))
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

func UpdateSpeaker(n *types.Notion, speakerID string, up SpeakerUpdate) error {
	props := speakerUpdateProps(up)
	if len(props) == 0 {
		return nil
	}
	_, err := n.Client.UpdatePageProperties(context.Background(), speakerID, props)
	return err
}

// MergeUniqueTags returns existing followed by any additions not already in
// existing. Order-preserving dedupe — used for Conference multiselect merges.
func MergeUniqueTags(existing, additions []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	out := make([]string, 0, len(existing)+len(additions))
	for _, s := range existing {
		if _, ok := seen[s]; ok || s == "" {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range additions {
		if _, ok := seen[s]; ok || s == "" {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// --- internal property-builder helpers ---

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

func titleValue(content string) *notion.PropertyValue {
	return notion.NewTitlePropertyValue(richTextChunks(content)...)
}

func richTextValue(content string) *notion.PropertyValue {
	return notion.NewRichTextPropertyValue(richTextChunks(content)...)
}

// richTextChunks turns a string into one or more *notion.RichText entries.
// Empty input → empty slice (Notion rejects rich_text entries with absent
// content; an empty array is fine). Long input is split at rune boundaries
// at the per-block limit (notionRichTextLimit) so no single entry exceeds
// what the API will accept.
func richTextChunks(content string) []*notion.RichText {
	if content == "" {
		return nil
	}
	pieces := splitForNotion(content)
	out := make([]*notion.RichText, len(pieces))
	for i, p := range pieces {
		out[i] = &notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: p}}
	}
	return out
}

// notionRichTextLimit is the maximum number of characters Notion accepts in
// a single rich_text block.
const notionRichTextLimit = 2000

func splitForNotion(s string) []string {
	runes := []rune(s)
	if len(runes) <= notionRichTextLimit {
		return []string{s}
	}
	var out []string
	for len(runes) > notionRichTextLimit {
		out = append(out, string(runes[:notionRichTextLimit]))
		runes = runes[notionRichTextLimit:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

func selectValue(name string) *notion.PropertyValue {
	return &notion.PropertyValue{
		Type:   notion.PropertySelect,
		Select: &notion.SelectOption{Name: name},
	}
}

func checkboxValue(b bool) *notion.PropertyValue {
	return &notion.PropertyValue{
		Type:     notion.PropertyCheckbox,
		Checkbox: &b,
	}
}

func multiSelectValue(tags []string) *notion.PropertyValue {
	opts := make([]*notion.SelectOption, len(tags))
	for i, t := range tags {
		opts[i] = &notion.SelectOption{Name: t}
	}
	return &notion.PropertyValue{
		Type:        notion.PropertyMultiSelect,
		MultiSelect: &opts,
	}
}
