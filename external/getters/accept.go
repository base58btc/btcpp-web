package getters

import (
	"context"
	"strings"

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

// GetSpeakersByEmail returns every Speaker page whose Email property
// matches `email`. Caller is responsible for deciding what to do when 0,
// 1, or many are returned.
//
// The cache is the fast path, but on a cache miss for a specific email
// we ALSO fall through to a live Notion query before declaring "no
// such speaker." That's deliberate:
//
//   - the cache is rebuilt from a paginated ListSpeakers at boot, and
//     speakers created in the last few seconds may not show up in
//     Notion's index yet (eventual consistency);
//   - dev with `air` cycles the binary on every save, so a Speaker
//     created in process A may simply not be in process B's freshly
//     loaded cache;
//   - any caller that's about to *create* a speaker (Submit /
//     JoinProposal) needs an authoritative "does this email already
//     exist?" answer or it'll mint duplicates.
//
// The live query runs only when the cache says zero — for the common
// case (existing speaker), we still take the cache hit and skip the
// Notion call.
func GetSpeakersByEmail(n *types.Notion, email string) ([]*types.Speaker, error) {
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
		// fall through to live query — cache may be stale.
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

// SearchSpeakersByNameOrEmail returns up to limit Speakers whose Name
// or Email contains q (case-insensitive substring). Cache-only — used
// by the admin invite-speaker autocomplete; rapid keystrokes shouldn't
// hammer Notion. Empty q returns nil.
func SearchSpeakersByNameOrEmail(q string, limit int) []*types.Speaker {
	q = strings.TrimSpace(strings.ToLower(q))
	if q == "" {
		return nil
	}
	out := make([]*types.Speaker, 0, limit)
	for _, s := range cacheSpeakers {
		if s == nil {
			continue
		}
		if strings.Contains(strings.ToLower(s.Name), q) ||
			strings.Contains(strings.ToLower(s.Email), q) {
			out = append(out, s)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func CreateSpeaker(n *types.Notion, in SpeakerInput) (string, error) {
	in = normalizeSpeakerInput(in)
	parent := notion.NewDatabaseParent(n.Config.SpeakersDb)
	page, err := n.Client.CreatePage(context.Background(), parent, speakerCreateProps(in))
	if err != nil {
		return "", err
	}
	// Eagerly insert into the warm cache so an immediately-following
	// GetSpeakersByEmail (e.g. from the dashboard the just-invited
	// speaker is about to land on) finds the row without waiting for
	// the periodic refresh.
	CacheSpeakerInsert(speakerFromInput(page.ID, in))
	return page.ID, nil
}

// speakerFromInput builds a *types.Speaker from a SpeakerInput + page ID
// for cache insertion. The fields mirror parseSpeaker — anything not
// in SpeakerInput stays zero-valued and gets overwritten on the next
// full cache refresh.
func speakerFromInput(pageID string, in SpeakerInput) *types.Speaker {
	return &types.Speaker{
		ID:            pageID,
		Name:          in.Name,
		Email:         in.Email,
		Photo:         in.Photo,
		Phone:         in.Phone,
		Signal:        in.Signal,
		Telegram:      in.Telegram,
		Twitter:       types.ParseTwitter(in.Twitter),
		Nostr:         in.Nostr,
		Github:        in.Github,
		Instagram:     in.Instagram,
		LinkedIn:      in.LinkedIn,
		Website:       in.Website,
		Company:       in.Company,
		OrgLogo:       in.OrgLogo,
		AvailToHire:   in.AvailToHire,
		LookingToHire: in.LookingToHire,
		TShirt:        in.TShirt,
	}
}

func UpdateSpeaker(n *types.Notion, speakerID string, up SpeakerUpdate) error {
	up = normalizeSpeakerUpdate(up)
	props := speakerUpdateProps(up)
	if len(props) == 0 {
		return nil
	}
	_, err := n.Client.UpdatePageProperties(context.Background(), speakerID, props)
	return err
}

func normalizeSpeakerInput(in SpeakerInput) SpeakerInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.TrimSpace(in.Email)
	in.Photo = strings.TrimSpace(in.Photo)
	in.Phone = strings.TrimSpace(in.Phone)
	in.Signal = strings.TrimSpace(in.Signal)
	in.Telegram = strings.TrimSpace(in.Telegram)
	in.Twitter = types.ParseTwitter(in.Twitter).Handle
	in.Nostr = strings.TrimSpace(in.Nostr)
	in.Github = strings.TrimSpace(in.Github)
	in.Instagram = strings.TrimSpace(in.Instagram)
	in.LinkedIn = strings.TrimSpace(in.LinkedIn)
	in.Website = strings.TrimSpace(in.Website)
	in.Company = strings.TrimSpace(in.Company)
	in.OrgLogo = strings.TrimSpace(in.OrgLogo)
	in.TShirt = strings.TrimSpace(in.TShirt)
	return in
}

func normalizeSpeakerUpdate(up SpeakerUpdate) SpeakerUpdate {
	up.Photo = strings.TrimSpace(up.Photo)
	up.Phone = strings.TrimSpace(up.Phone)
	up.Signal = strings.TrimSpace(up.Signal)
	up.Telegram = strings.TrimSpace(up.Telegram)
	up.Twitter = types.ParseTwitter(up.Twitter).Handle
	up.Nostr = strings.TrimSpace(up.Nostr)
	up.Github = strings.TrimSpace(up.Github)
	up.Instagram = strings.TrimSpace(up.Instagram)
	up.LinkedIn = strings.TrimSpace(up.LinkedIn)
	up.Website = strings.TrimSpace(up.Website)
	up.Company = strings.TrimSpace(up.Company)
	up.OrgLogo = strings.TrimSpace(up.OrgLogo)
	up.TShirt = strings.TrimSpace(up.TShirt)
	return up
}

// AllCachedSpeakers returns the in-memory Speaker slice. Read-only
// access for callers outside the package — exposed so the dashboard's
// role manager can look up a Speaker by ID without hammering Notion.
// Don't mutate the returned slice.
func AllCachedSpeakers() []*types.Speaker {
	return cacheSpeakers
}

// UpdateSpeakerRoles overwrites the Roles multi-select on a Speakers
// row. Used by the dashboard's global-admin role manager. Roles are
// the per-conf admin tags ("vienna-admin", "global-volcoord", ...);
// see the auth package for the parsing/coverage rules. Caller is
// responsible for whatever authorization gate (this function trusts
// its input).
func UpdateSpeakerRoles(n *types.Notion, speakerID string, roles []string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), speakerID,
		map[string]*notion.PropertyValue{
			"Roles": multiSelectValue(roles),
		})
	if err != nil {
		return err
	}
	// Patch the warm cache so the next request sees the new role set
	// without waiting for a periodic refresh tick. The cached pointer
	// survives across cache rebuilds in practice, but mutating in
	// place is still the right move because the role lookup goes
	// through GetSpeakersByEmail → cacheSpeakers.
	for _, s := range cacheSpeakers {
		if s != nil && s.ID == speakerID {
			s.Roles = append(s.Roles[:0], roles...)
			break
		}
	}
	return nil
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
// Empty input → nil slice; callers should skip the property entirely in
// that case rather than writing an empty value (the go-notion struct
// uses `omitempty` on title/rich_text, so an empty array gets dropped
// and Notion rejects the resulting "title undefined" payload).
//
// Long input is split at rune boundaries at the per-block limit
// (notionRichTextLimit) so no single entry exceeds what the API accepts.
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
