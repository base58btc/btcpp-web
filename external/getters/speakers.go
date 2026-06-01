package getters

import (
	"strings"
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// SpeakerInput is the data needed to create a Speakers/People row.
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
	OrgLogo       string
	AvailToHire   bool
	LookingToHire bool
	TShirt        string
}

// SpeakerUpdate is a sparse update for an existing speaker/person row. Empty
// strings mean "leave this field alone".
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

func getSpeakers(ctx *config.AppContext) {
	var err error
	ctx.Infos.Printf("getting speakers...")
	if UsePostgresBackend(ctx) {
		cacheSpeakers, err = listSpeakersPostgres(ctx)
	} else {
		cacheSpeakers, err = ListSpeakersNotion(ctx.Notion)
	}

	if err != nil {
		ctx.Err.Printf("error fetching speakers %s", err)
	} else {
		ctx.Infos.Printf("Loaded %d speakers!", len(cacheSpeakers))
		writeCache("speakers", cacheSpeakers)
		ctx.Infos.Printf("there are %d callbacks", len(onSpeakersRefresh))
		for _, cb := range onSpeakersRefresh {
			cb(ctx, cacheSpeakers)
		}
	}
}

/* This may return nil */
func FetchSpeakersCached(ctx *config.AppContext) ([]*types.Speaker, error) {
	now := time.Now()
	deadline := now.Add(-cacheTTL)
	if cacheSpeakers == nil || lastSpeakerFetch.Before(deadline) {
		/* Set last fetch to now even if there's errors */
		lastSpeakerFetch = time.Now()
		queueRefresh(JobSpeakers)
	}

	return cacheSpeakers, nil
}

func ListSpeakers(n *types.Notion) ([]*types.Speaker, error) {
	return ListSpeakersNotion(n)
}

func GetSpeakersByEmail(ctx *config.AppContext, email string) ([]*types.Speaker, error) {
	if UsePostgresBackend(ctx) {
		return getSpeakersByEmailPostgres(ctx, email)
	}
	return getSpeakersByEmailNotion(ctx.Notion, email)
}

// SearchSpeakersByNameOrEmail returns up to limit Speakers whose Name or Email
// contains q (case-insensitive substring). Cache-only.
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

func CreateSpeaker(ctx *config.AppContext, in SpeakerInput) (string, error) {
	if UsePostgresBackend(ctx) {
		return createSpeakerPostgres(ctx, in)
	}
	return createSpeakerNotion(ctx.Notion, in)
}

// speakerFromInput builds a *types.Speaker from a SpeakerInput + row ID for
// cache insertion. Fields not in SpeakerInput stay zero-valued until refresh.
func speakerFromInput(id string, in SpeakerInput) *types.Speaker {
	return &types.Speaker{
		ID:            id,
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

func UpdateSpeaker(ctx *config.AppContext, speakerID string, up SpeakerUpdate) error {
	if UsePostgresBackend(ctx) {
		return updateSpeakerPostgres(ctx, speakerID, up)
	}
	return updateSpeakerNotion(ctx.Notion, speakerID, up)
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

func patchCachedSpeaker(speakerID string, up SpeakerUpdate) {
	for _, s := range cacheSpeakers {
		if s == nil || s.ID != speakerID {
			continue
		}
		if up.Photo != "" {
			s.Photo = up.Photo
		}
		if up.Phone != "" {
			s.Phone = up.Phone
		}
		if up.Signal != "" {
			s.Signal = up.Signal
		}
		if up.Telegram != "" {
			s.Telegram = up.Telegram
		}
		if up.Twitter != "" {
			s.Twitter = types.ParseTwitter(up.Twitter)
		}
		if up.Nostr != "" {
			s.Nostr = up.Nostr
		}
		if up.Github != "" {
			s.Github = up.Github
		}
		if up.Instagram != "" {
			s.Instagram = up.Instagram
		}
		if up.LinkedIn != "" {
			s.LinkedIn = up.LinkedIn
		}
		if up.Website != "" {
			s.Website = up.Website
		}
		if up.Company != "" {
			s.Company = up.Company
		}
		if up.OrgLogo != "" {
			s.OrgLogo = up.OrgLogo
		}
		if up.TShirt != "" {
			s.TShirt = up.TShirt
		}
		return
	}
}

// AllCachedSpeakers returns the in-memory Speaker slice. Don't mutate it.
func AllCachedSpeakers() []*types.Speaker {
	return cacheSpeakers
}

func FetchSpeakerByID(ctx *config.AppContext, speakerID string) (*types.Speaker, error) {
	if UsePostgresBackend(ctx) {
		return fetchSpeakerByIDPostgres(ctx, speakerID)
	}
	return fetchSpeakerByIDNotion(ctx.Notion, speakerID)
}

func UpdateSpeakerRoles(ctx *config.AppContext, speakerID string, roles []string) error {
	if UsePostgresBackend(ctx) {
		return updateSpeakerRolesPostgres(ctx, speakerID, roles)
	}
	return updateSpeakerRolesNotion(ctx.Notion, speakerID, roles)
}

// MergeUniqueTags returns existing followed by additions not already present.
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
