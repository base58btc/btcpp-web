package handlers

import (
	"errors"
	"strings"
	"testing"

	"btcpp-web/external/getters"
	"btcpp-web/internal/types"
)

type submitRecorder struct {
	findCalls       []string
	speakerCreated  []getters.SpeakerInput
	speakerUpdated  []struct {
		ID     string
		Update getters.SpeakerUpdate
	}
	orgFindCalls     []string
	orgsCreated      []*types.Org
	proposalCreated  []getters.ProposalInput
	speakerProposals []getters.SpeakerProposalInput
}

func makeSubmitApp(email, scheduleTag string, otherTags ...string) *types.TalkApp {
	others := make([]*types.Conf, 0, len(otherTags))
	for _, tag := range otherTags {
		others = append(others, &types.Conf{Tag: tag, Ref: "conf-" + tag})
	}
	var schedule *types.Conf
	if scheduleTag != "" {
		schedule = &types.Conf{Tag: scheduleTag, Ref: "conf-" + scheduleTag}
	}
	return &types.TalkApp{
		Ref:           "tapp-1",
		Status:        "Applied",
		Name:          "Alice Test",
		Email:         email,
		Phone:         "+15551234567",
		TalkTitle:     "On Bitcoin",
		Description:   "A talk",
		Setup:         "Install Bitcoin Core 27.0",
		Comments:      "looking forward to it",
		PresType:      "20talk",
		Recording:     "",
		Org:           "ACME",
		NormPhoto:     "abc123.jpg",
		Twitter:       types.Twitter{Handle: "alice"},
		Github:        "https://github.com/alice",
		Website:       "https://alice.example",
		Nostr:         "npub1alice",
		Signal:        "alice.99",
		Telegram:      "alice_tg",
		Hometown:      "Brooklyn, NY",
		Visa:          "I have a US passport",
		Shirt:         "MM",
		FirstEvent:    true,
		DinnerRSVP:    true,
		Sponsor:       false,
		Availability:  []string{"day-thursday", "day-friday"},
		ScheduleFor:   schedule,
		OtherEvents:   others,
	}
}

func newSubmitRecorder(t *testing.T, app *types.TalkApp, matches []*types.Speaker, opts ...func(*submitDeps)) (submitPipeline, *submitRecorder) {
	t.Helper()
	rec := &submitRecorder{}
	deps := submitDeps{
		findSpeakers: func(email string) ([]*types.Speaker, error) {
			rec.findCalls = append(rec.findCalls, email)
			return matches, nil
		},
		createSpeaker: func(in getters.SpeakerInput) (string, error) {
			rec.speakerCreated = append(rec.speakerCreated, in)
			return "sp-new", nil
		},
		updateSpeaker: func(id string, up getters.SpeakerUpdate) error {
			rec.speakerUpdated = append(rec.speakerUpdated, struct {
				ID     string
				Update getters.SpeakerUpdate
			}{id, up})
			return nil
		},
		findOrg: func(website, name string) (*types.Org, error) {
			rec.orgFindCalls = append(rec.orgFindCalls, website+"|"+name)
			return nil, nil
		},
		createOrg: func(org *types.Org) (string, error) {
			rec.orgsCreated = append(rec.orgsCreated, org)
			return "org-new", nil
		},
		createProposal: func(in getters.ProposalInput) (string, error) {
			rec.proposalCreated = append(rec.proposalCreated, in)
			return "prop-1", nil
		},
		createSpeakerProposal: func(in getters.SpeakerProposalInput) (string, error) {
			rec.speakerProposals = append(rec.speakerProposals, in)
			return "sp-prop-1", nil
		},
		logf: t.Logf,
	}
	for _, opt := range opts {
		opt(&deps)
	}
	return submitPipeline{deps: deps}, rec
}

func TestSubmit_NewSpeaker_OneConf(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26")
	p, rec := newSubmitRecorder(t, app, nil)

	res, err := p.Submit(app)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.SpeakerCreated {
		t.Errorf("expected SpeakerCreated=true")
	}
	if res.SpeakerID != "sp-new" || res.ProposalID != "prop-1" || res.SpeakerProposalID != "sp-prop-1" {
		t.Errorf("result IDs: %+v", res)
	}
	if len(rec.speakerCreated) != 1 {
		t.Fatalf("expected 1 speaker created; got %d", len(rec.speakerCreated))
	}
	created := rec.speakerCreated[0]
	if created.Photo != "abc123-400.avif" {
		t.Errorf("Photo: got %q want abc123-400.avif", created.Photo)
	}
	if created.Phone != "+15551234567" || created.Telegram != "alice_tg" {
		t.Errorf("Phone/Telegram not promoted: %+v", created)
	}
	if created.OrgLogo != "" {
		t.Errorf("OrgLogo: got %q (expected empty when app.OrgLogo is empty)", created.OrgLogo)
	}

	if len(rec.proposalCreated) != 1 {
		t.Fatalf("expected 1 proposal; got %d", len(rec.proposalCreated))
	}
	prop := rec.proposalCreated[0]
	if prop.Title != "On Bitcoin" {
		t.Errorf("Proposal Title: %q", prop.Title)
	}
	if prop.Setup != "Install Bitcoin Core 27.0" {
		t.Errorf("Proposal Setup not copied: %q", prop.Setup)
	}
	if prop.TalkType != "talk" {
		t.Errorf("TalkType: got %q want talk", prop.TalkType)
	}
	if prop.DesiredDuration != 20 || prop.AvailDuration != 20 {
		t.Errorf("Durations: desired=%d avail=%d", prop.DesiredDuration, prop.AvailDuration)
	}
	if prop.ScheduleForTag != "berlin26" {
		t.Errorf("ScheduleForTag: %q", prop.ScheduleForTag)
	}
	if prop.Status != "Applied" {
		t.Errorf("Status: %q", prop.Status)
	}

	if len(rec.speakerProposals) != 1 {
		t.Fatalf("expected 1 SpeakerProposal; got %d", len(rec.speakerProposals))
	}
	sp := rec.speakerProposals[0]
	if sp.SpeakerID != "sp-new" || sp.ProposalID != "prop-1" {
		t.Errorf("SpeakerProposal links: %+v", sp)
	}
	if sp.ComingFrom != "Brooklyn, NY" {
		t.Errorf("ComingFrom: %q", sp.ComingFrom)
	}
	if sp.RecordOK != "RecordingOK" {
		t.Errorf("RecordOK: got %q want default RecordingOK", sp.RecordOK)
	}
	if !sp.FirstEvent || !sp.DinnerRSVP {
		t.Errorf("FirstEvent/DinnerRSVP not promoted: %+v", sp)
	}
	if sp.Sponsor {
		t.Errorf("Sponsor: got true, expected false from form input")
	}
	if len(sp.OtherEventTags) != 0 {
		t.Errorf("OtherEventTags should be empty for single-conf submission; got %v", sp.OtherEventTags)
	}
}

func TestSubmit_OtherEventsPassThrough(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26", "atx26", "vienna26")
	p, rec := newSubmitRecorder(t, app, nil)

	if _, err := p.Submit(app); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	prop := rec.proposalCreated[0]
	if prop.ScheduleForTag != "berlin26" {
		t.Errorf("ScheduleForTag: got %q want berlin26", prop.ScheduleForTag)
	}
	sp := rec.speakerProposals[0]
	want := []string{"atx26", "vienna26"}
	if !stringsEq(sp.OtherEventTags, want) {
		t.Errorf("OtherEventTags: got %v want %v", sp.OtherEventTags, want)
	}
}

func TestSubmit_ExistingSpeakerMerges(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "atx26")
	existing := &types.Speaker{
		ID:      "sp-1",
		Name:    "Alice",
		Email:   "alice@example.com",
		Photo:   "preexisting.jpg",
		Twitter: types.Twitter{Handle: "alice_curated"},
		Phone:   "+19998887777",
		Company: "OldOrg",
	}
	p, rec := newSubmitRecorder(t, app, []*types.Speaker{existing})

	res, err := p.Submit(app)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.SpeakerCreated {
		t.Errorf("should not create when match found")
	}
	if res.SpeakerID != "sp-1" {
		t.Errorf("SpeakerID: %q", res.SpeakerID)
	}
	if len(rec.speakerUpdated) != 1 {
		t.Fatalf("expected 1 update; got %d", len(rec.speakerUpdated))
	}
	up := rec.speakerUpdated[0].Update
	// Photo and Phone curated → must not overwrite.
	if up.Photo != "" {
		t.Errorf("Photo should not overwrite curated value; got %q", up.Photo)
	}
	if up.Phone != "" {
		t.Errorf("Phone should not overwrite curated value; got %q", up.Phone)
	}
	// Github/Website/Nostr/Signal/Telegram empty on existing → filled.
	if up.Github == "" || up.Website == "" || up.Nostr == "" || up.Signal == "" || up.Telegram == "" {
		t.Errorf("expected empties filled; got %+v", up)
	}
	// Company is no longer written to Speaker — it lives on SpeakerProposal now.
	if up.Company != "" {
		t.Errorf("Company should NOT be written to Speaker; got %q", up.Company)
	}
	// Proposal still gets created on update path.
	if len(rec.proposalCreated) != 1 {
		t.Errorf("Proposal should still be created when speaker matched")
	}
	// Company lands on SpeakerProposal.
	if len(rec.speakerProposals) != 1 || rec.speakerProposals[0].Company != "ACME" {
		t.Errorf("SpeakerProposal.Company: got %q, want ACME", rec.speakerProposals[0].Company)
	}
}

func TestSubmit_DuplicateEmailFailsLoudly(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26")
	dups := []*types.Speaker{
		{ID: "sp-1", Email: "alice@example.com"},
		{ID: "sp-2", Email: "alice@example.com"},
	}
	p, rec := newSubmitRecorder(t, app, dups)

	_, err := p.Submit(app)
	if !errors.Is(err, ErrDuplicateSpeakerEmail) {
		t.Fatalf("expected ErrDuplicateSpeakerEmail; got %v", err)
	}
	if len(rec.speakerCreated)+len(rec.speakerUpdated)+len(rec.proposalCreated)+len(rec.speakerProposals) != 0 {
		t.Errorf("no writes expected on dup-email; got creates=%d updates=%d proposals=%d sp=%d",
			len(rec.speakerCreated), len(rec.speakerUpdated), len(rec.proposalCreated), len(rec.speakerProposals))
	}
}

func TestSubmit_EmptyScheduleFor(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "")
	p, rec := newSubmitRecorder(t, app, nil)

	_, err := p.Submit(app)
	if err == nil || !strings.Contains(err.Error(), "ScheduleFor") {
		t.Fatalf("expected ScheduleFor error; got %v", err)
	}
	if len(rec.speakerCreated)+len(rec.proposalCreated) != 0 {
		t.Error("no writes should occur when ScheduleFor empty")
	}
}

func TestSubmit_EmptyEmail(t *testing.T) {
	app := makeSubmitApp("", "berlin26")
	p, rec := newSubmitRecorder(t, app, nil)

	_, err := p.Submit(app)
	if err == nil || !strings.Contains(err.Error(), "email") {
		t.Fatalf("expected email error; got %v", err)
	}
	if len(rec.findCalls) != 0 {
		t.Error("findSpeakers should not be called with empty email")
	}
}

func TestSubmit_RecordingPassThrough(t *testing.T) {
	cases := map[string]string{
		"":            "RecordingOK", // default
		"NoRecord":    "NoRecord",
		"AudioOnly":   "AudioOnly",
		"RecordingOK": "RecordingOK",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			app := makeSubmitApp("alice@example.com", "berlin26")
			app.Recording = input
			p, rec := newSubmitRecorder(t, app, nil)
			if _, err := p.Submit(app); err != nil {
				t.Fatalf("Submit: %v", err)
			}
			if got := rec.speakerProposals[0].RecordOK; got != want {
				t.Errorf("RecordOK: got %q want %q", got, want)
			}
		})
	}
}

func TestDurationFromPresType(t *testing.T) {
	cases := map[string]int{
		"lntalk":       5,
		"20talk":       20,
		"45talk":       45,
		"45panel":      45,
		"45workshop":   45,
		"60workshop":   60,
		"90workshop":   90,
		"120workshop":  120,
		"30talk":       30,
		"60keynote":    60,
		"180hackathon": 180,
		"":             0,
		"unknown":      0,
		"talk20":       0, // doesn't lead with digits → 0
		"45foo":        0, // suffix not a recognized type → 0
	}
	for in, want := range cases {
		if got := durationFromPresType(in); got != want {
			t.Errorf("durationFromPresType(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestOtherEventTags(t *testing.T) {
	a := &types.Conf{Tag: "a", Ref: "ref-a"}
	b := &types.Conf{Tag: "b", Ref: "ref-b"}
	c := &types.Conf{Tag: "c", Ref: "ref-c"}

	cases := []struct {
		name  string
		input []*types.Conf
		want  []string
	}{
		{"empty", nil, nil},
		{"single", []*types.Conf{a}, []string{"a"}},
		{"multiple", []*types.Conf{a, b, c}, []string{"a", "b", "c"}},
		{"dedup", []*types.Conf{a, b, a, c, b}, []string{"a", "b", "c"}},
		{"skips nil/empty tags", []*types.Conf{nil, {Tag: ""}, a}, []string{"a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := otherEventTags(tc.input)
			if !stringsEq(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestSubmit_OrgUpsert_BothEmpty_SkipsOrg(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26")
	app.Org = ""
	app.OrgSite = ""
	app.OrgTwitter = types.Twitter{}
	app.OrgNostr = ""
	p, rec := newSubmitRecorder(t, app, nil)

	res, err := p.Submit(app)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if len(rec.orgFindCalls) != 0 {
		t.Errorf("findOrg should not be called when both Org and OrgSite are empty; got %v", rec.orgFindCalls)
	}
	if len(rec.orgsCreated) != 0 {
		t.Errorf("createOrg should not be called when both Org and OrgSite are empty; got %d", len(rec.orgsCreated))
	}
	if res.OrgID != "" {
		t.Errorf("OrgID: got %q, want empty", res.OrgID)
	}
	if len(rec.speakerProposals) != 1 || rec.speakerProposals[0].OrgID != "" {
		t.Errorf("SpeakerProposal.OrgID: got %q, want empty", rec.speakerProposals[0].OrgID)
	}
}

func TestSubmit_OrgUpsert_NameAndLogoOnly_SkipsCreate(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26")
	app.Org = "ACME"
	app.OrgLogo = "abc123.svg" // logo is captured on SpeakerProposal, not enough to create an Org
	app.OrgSite = ""
	app.OrgTwitter = types.Twitter{}
	app.OrgNostr = ""
	p, rec := newSubmitRecorder(t, app, nil)

	res, err := p.Submit(app)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.OrgCreated {
		t.Error("OrgCreated should be false when only name + logo are supplied")
	}
	if res.OrgID != "" {
		t.Errorf("OrgID: got %q, want empty (no Org created)", res.OrgID)
	}
	if len(rec.orgsCreated) != 0 {
		t.Errorf("createOrg must not be called for name+logo-only submissions; got %d", len(rec.orgsCreated))
	}
	// findOrg should still be called so we can link to an existing Org if one
	// matches by name.
	if len(rec.orgFindCalls) != 1 {
		t.Errorf("expected findOrg to be called once for name match; got %d", len(rec.orgFindCalls))
	}
}

func TestSubmit_OrgUpsert_NameOnly_LinksToExistingByName(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26")
	app.Org = "ACME"
	app.OrgSite = ""
	app.OrgTwitter = types.Twitter{}
	app.OrgNostr = ""
	p, rec := newSubmitRecorder(t, app, nil, func(d *submitDeps) {
		d.findOrg = func(website, name string) (*types.Org, error) {
			return &types.Org{Ref: "org-existing", Name: name}, nil
		}
	})

	res, err := p.Submit(app)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.OrgCreated {
		t.Error("OrgCreated should be false when matching an existing Org")
	}
	if res.OrgID != "org-existing" {
		t.Errorf("OrgID: got %q, want org-existing", res.OrgID)
	}
	if len(rec.orgsCreated) != 0 {
		t.Errorf("createOrg must not be called when an existing org matches by name; got %d", len(rec.orgsCreated))
	}
}

func TestSubmit_OrgUpsert_NoMatch_CreatesOrg(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26")
	app.Org = "ACME"
	app.OrgSite = "https://acme.example"
	app.OrgTwitter = types.Twitter{Handle: "acme"}
	app.OrgNostr = "npub1acme"
	p, rec := newSubmitRecorder(t, app, nil)

	res, err := p.Submit(app)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.OrgCreated {
		t.Errorf("expected OrgCreated=true")
	}
	if res.OrgID != "org-new" {
		t.Errorf("OrgID: got %q, want org-new", res.OrgID)
	}
	if len(rec.orgsCreated) != 1 {
		t.Fatalf("expected 1 org created; got %d", len(rec.orgsCreated))
	}
	created := rec.orgsCreated[0]
	if created.Name != "ACME" || created.Website != "https://acme.example" || created.Twitter.Handle != "acme" || created.Nostr != "npub1acme" {
		t.Errorf("created org fields: %+v", created)
	}
	if len(rec.speakerProposals) != 1 || rec.speakerProposals[0].OrgID != "org-new" {
		t.Errorf("SpeakerProposal.OrgID: got %q, want org-new", rec.speakerProposals[0].OrgID)
	}
}

func TestSubmit_OrgUpsert_MatchFound_LinksWithoutCreate(t *testing.T) {
	app := makeSubmitApp("alice@example.com", "berlin26")
	app.OrgSite = "https://acme.example"
	p, rec := newSubmitRecorder(t, app, nil, func(d *submitDeps) {
		d.findOrg = func(website, name string) (*types.Org, error) {
			return &types.Org{Ref: "org-existing", Name: "ACME", Website: website}, nil
		}
	})

	res, err := p.Submit(app)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.OrgCreated {
		t.Error("OrgCreated should be false when an existing match was found")
	}
	if res.OrgID != "org-existing" {
		t.Errorf("OrgID: got %q, want org-existing", res.OrgID)
	}
	if len(rec.orgsCreated) != 0 {
		t.Errorf("createOrg must not be called when an existing org matches; got %d", len(rec.orgsCreated))
	}
	if len(rec.speakerProposals) != 1 || rec.speakerProposals[0].OrgID != "org-existing" {
		t.Errorf("SpeakerProposal.OrgID: got %q, want org-existing", rec.speakerProposals[0].OrgID)
	}
}
