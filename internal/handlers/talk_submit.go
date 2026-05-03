package handlers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

const StatusApplied = "Applied"

// submitDeps carries the side-effecting collaborators used by the talk-submit
// pipeline. Production wires these to the getters package; tests pass fakes.
type submitDeps struct {
	findSpeakers          func(email string) ([]*types.Speaker, error)
	createSpeaker         func(in getters.SpeakerInput) (string, error)
	updateSpeaker         func(speakerID string, up getters.SpeakerUpdate) error
	findOrg               func(website, name string) (*types.Org, error)
	createOrg             func(org *types.Org) (string, error)
	createProposal        func(in getters.ProposalInput) (string, error)
	createSpeakerProposal func(in getters.SpeakerProposalInput) (string, error)
	logf                  func(format string, args ...interface{})
}

type submitPipeline struct {
	deps submitDeps
}

// SubmitResult summarises what the pipeline did, for logging / future flash.
type SubmitResult struct {
	SpeakerID         string
	SpeakerCreated    bool
	OrgID             string
	OrgCreated        bool
	ProposalID        string
	SpeakerProposalID string
}

func newSubmitPipeline(ctx *config.AppContext) submitPipeline {
	return submitPipeline{deps: submitDeps{
		findSpeakers: func(email string) ([]*types.Speaker, error) {
			return getters.GetSpeakersByEmail(ctx.Notion, email)
		},
		createSpeaker: func(in getters.SpeakerInput) (string, error) {
			return getters.CreateSpeaker(ctx.Notion, in)
		},
		updateSpeaker: func(id string, up getters.SpeakerUpdate) error {
			return getters.UpdateSpeaker(ctx.Notion, id, up)
		},
		findOrg: func(website, name string) (*types.Org, error) {
			return getters.FindOrg(ctx.Notion, website, name)
		},
		createOrg: func(org *types.Org) (string, error) {
			return getters.RegisterOrg(ctx.Notion, org)
		},
		createProposal: func(in getters.ProposalInput) (string, error) {
			return getters.CreateProposal(ctx.Notion, in)
		},
		createSpeakerProposal: func(in getters.SpeakerProposalInput) (string, error) {
			return getters.CreateSpeakerProposal(ctx.Notion, in)
		},
		logf: ctx.Err.Printf,
	}}
}

// Submit writes a form-decoded TalkApp into Speaker + Proposal +
// SpeakerProposal Notion DBs. Speaker is upserted by email (fail-loudly on
// duplicates). Each submission yields exactly one Proposal pinned to the
// conf the form was submitted from.
func (p submitPipeline) Submit(app *types.TalkApp) (SubmitResult, error) {
	var result SubmitResult

	if app.Email == "" {
		return result, errors.New("email is required")
	}
	if app.ScheduleFor == nil {
		return result, errors.New("ScheduleFor conf is required")
	}

	// 1. Upsert Speaker by email.
	matches, err := p.deps.findSpeakers(app.Email)
	if err != nil {
		return result, fmt.Errorf("find speakers by email: %w", err)
	}
	if len(matches) > 1 {
		return result, fmt.Errorf("%w: %d matches for %s", ErrDuplicateSpeakerEmail, len(matches), app.Email)
	}

	if len(matches) == 0 {
		speakerID, err := p.deps.createSpeaker(speakerInputFromTalkApp(app))
		if err != nil {
			return result, fmt.Errorf("create speaker: %w", err)
		}
		result.SpeakerID = speakerID
		result.SpeakerCreated = true
	} else {
		existing := matches[0]
		result.SpeakerID = existing.ID
		update := buildSpeakerUpdateFromForm(existing, app)
		if err := p.deps.updateSpeaker(existing.ID, update); err != nil {
			return result, fmt.Errorf("update speaker %s: %w", existing.ID, err)
		}
	}

	// 2. Upsert Org. We attempt the upsert when either the website or the
	// name is non-empty so that submissions without a website still land an
	// Org row that the SpeakerProposal can link to.
	if app.OrgSite != "" || app.Org != "" {
		orgID, created, err := p.upsertOrg(app)
		if err != nil {
			return result, fmt.Errorf("upsert org: %w", err)
		}
		result.OrgID = orgID
		result.OrgCreated = created
	}

	// 3. Create Proposal (one — pinned to the form's conf).
	dur := durationFromPresType(app.PresType)
	proposalID, err := p.deps.createProposal(getters.ProposalInput{
		Title:           app.TalkTitle,
		Description:     app.Description,
		Setup:           app.Setup,
		Comments:        app.Comments,
		TalkType:        mapPresTypeToTalkType(app.PresType),
		DesiredDuration: dur,
		AvailDuration:   dur,
		ScheduleForTag:  app.ScheduleFor.Tag,
		Status:          StatusApplied,
	})
	if err != nil {
		return result, fmt.Errorf("create proposal: %w", err)
	}
	result.ProposalID = proposalID

	// 4. Create SpeakerProposal linking Speaker, Proposal, and Org.
	otherEventTags := otherEventTags(app.OtherEvents)
	spID, err := p.deps.createSpeakerProposal(getters.SpeakerProposalInput{
		SpeakerID:      result.SpeakerID,
		ProposalID:     result.ProposalID,
		OrgID:          result.OrgID,
		Company:        app.Org,
		OrgPhoto:       app.OrgLogo,
		ComingFrom:     app.Hometown,
		Availability:   app.Availability,
		RecordOK:       defaultRecording(app.Recording),
		Visa:           app.Visa,
		FirstEvent:     app.FirstEvent,
		OtherEventTags: otherEventTags,
		DinnerRSVP:     app.DinnerRSVP,
		Sponsor:        app.Sponsor,
	})
	if err != nil {
		return result, fmt.Errorf("create speaker proposal: %w", err)
	}
	result.SpeakerProposalID = spID

	return result, nil
}

// upsertOrg looks up an existing Org by Website (preferred) or Name; creates
// a new one only when the form supplies org data beyond name + logo (i.e.,
// at least one of OrgSite / OrgTwitter / OrgNostr is set). Submissions that
// just type a company name skip the create — they'll still link to an
// existing Org if one matches by name.
// Returns (orgID, created, err); both empty when no link or create happens.
func (p submitPipeline) upsertOrg(app *types.TalkApp) (string, bool, error) {
	existing, err := p.deps.findOrg(app.OrgSite, app.Org)
	if err != nil {
		return "", false, fmt.Errorf("find org: %w", err)
	}
	if existing != nil {
		return existing.Ref, false, nil
	}
	if app.OrgSite == "" && app.OrgTwitter.Handle == "" && app.OrgNostr == "" {
		return "", false, nil
	}
	orgID, err := p.deps.createOrg(&types.Org{
		Name:    app.Org,
		Website: app.OrgSite,
		Twitter: app.OrgTwitter,
		Nostr:   app.OrgNostr,
	})
	if err != nil {
		return "", false, fmt.Errorf("create org: %w", err)
	}
	return orgID, true, nil
}

// speakerInputFromTalkApp builds the create payload from form data. Org and
// org-logo are intentionally NOT written here — they live on the
// SpeakerProposal (per-application) and the Orgs DB (deduped entity).
func speakerInputFromTalkApp(app *types.TalkApp) getters.SpeakerInput {
	return getters.SpeakerInput{
		Name:     app.Name,
		Email:    app.Email,
		Photo:    avif400Name(app.NormPhoto),
		Phone:    app.Phone,
		Signal:   app.Signal,
		Telegram: app.Telegram,
		Twitter:  app.Twitter.Handle,
		Nostr:    app.Nostr,
		Github:   app.Github,
		Website:  app.Website,
		TShirt:   validShirtCode(app.Shirt),
	}
}

// buildSpeakerUpdateFromForm fills empty fields on an existing Speaker with
// values from the new submission. Curated values are not overwritten.
func buildSpeakerUpdateFromForm(sp *types.Speaker, app *types.TalkApp) getters.SpeakerUpdate {
	up := getters.SpeakerUpdate{}
	if sp.Photo == "" && app.NormPhoto != "" {
		up.Photo = avif400Name(app.NormPhoto)
	}
	if sp.Phone == "" && app.Phone != "" {
		up.Phone = app.Phone
	}
	if sp.Signal == "" && app.Signal != "" {
		up.Signal = app.Signal
	}
	if sp.Telegram == "" && app.Telegram != "" {
		up.Telegram = app.Telegram
	}
	if sp.Twitter.Handle == "" && app.Twitter.Handle != "" {
		up.Twitter = app.Twitter.Handle
	}
	if sp.Nostr == "" && app.Nostr != "" {
		up.Nostr = app.Nostr
	}
	if sp.Github == "" && app.Github != "" {
		up.Github = app.Github
	}
	if sp.Website == "" && app.Website != "" {
		up.Website = app.Website
	}
	if sp.TShirt == "" {
		if mapped := validShirtCode(app.Shirt); mapped != "" {
			up.TShirt = mapped
		}
	}
	return up
}

// otherEventTags extracts conf tags from a slice of Conf pointers, deduped,
// preserving order. Used to populate SpeakerProposal.OtherEvents (multi_select).
func otherEventTags(confs []*types.Conf) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, c := range confs {
		if c == nil || c.Tag == "" {
			continue
		}
		if _, ok := seen[c.Tag]; ok {
			continue
		}
		seen[c.Tag] = struct{}{}
		out = append(out, c.Tag)
	}
	return out
}

// durationFromPresType pulls the leading integer out of a PresType ID whose
// suffix is one of the five recognized types (talk / panel / workshop /
// keynote / hackathon). e.g. "20talk" → 20, "120workshop" → 120,
// "lntalk" → 5. Returns 0 for unrecognized suffixes or empty input.
func durationFromPresType(presType string) int {
	if presType == "" {
		return 0
	}
	if presType == "lntalk" {
		return 5
	}
	i := 0
	for i < len(presType) && presType[i] >= '0' && presType[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	switch presType[i:] {
	case "talk", "panel", "workshop", "keynote", "hackathon":
	default:
		return 0
	}
	n, err := strconv.Atoi(presType[:i])
	if err != nil {
		return 0
	}
	return n
}

// defaultRecording returns "RecordingOK" when the form's Recording field is
// empty, else passes the value through.
func defaultRecording(s string) string {
	if strings.TrimSpace(s) == "" {
		return "RecordingOK"
	}
	return s
}
