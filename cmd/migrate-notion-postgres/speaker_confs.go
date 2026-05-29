package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"btcpp-web/internal/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/niftynei/go-notion"
)

type speakerConfImportRow struct {
	ref            string
	speakerRef     string
	orgRef         string
	proposalRefs   []string
	comingFrom     string
	availability   []string
	recordOK       string
	visa           string
	firstEvent     bool
	dinnerRSVP     bool
	sponsor        bool
	company        string
	orgPhoto       string
	otherEventTags []string
	invitedAt      *time.Time
	viewedAt       *time.Time
	acceptedAt     *time.Time
}

func listSpeakerConfImportRows(n *types.Notion) ([]*speakerConfImportRow, error) {
	var out []*speakerConfImportRow
	hasMore := true
	nextCursor := ""
	for hasMore {
		pages, next, more, err := n.Client.QueryDatabase(context.Background(),
			n.Config.SpeakerConfDb, notion.QueryDatabaseParam{StartCursor: nextCursor})
		if err != nil {
			return nil, err
		}
		nextCursor = next
		hasMore = more
		for _, page := range pages {
			out = append(out, parseSpeakerConfImportRow(page.ID, page.Properties))
		}
	}
	return out, nil
}

func parseSpeakerConfImportRow(ref string, props map[string]notion.PropertyValue) *speakerConfImportRow {
	return &speakerConfImportRow{
		ref:            ref,
		speakerRef:     relationID(props["speaker"]),
		orgRef:         relationID(props["org"]),
		proposalRefs:   relationIDs(props["talk"]),
		comingFrom:     titleText(props["ComingFrom"]),
		availability:   multiSelectNames(props["Avails"]),
		recordOK:       selectName(props["RecordOK"]),
		visa:           selectName(props["Visa"]),
		firstEvent:     checkbox(props["FirstEvent"].Checkbox),
		dinnerRSVP:     checkbox(props["DinnerRSVP"].Checkbox),
		sponsor:        checkbox(props["Sponsor"].Checkbox),
		company:        richText(props["Company"]),
		orgPhoto:       richText(props["OrgPhoto"]),
		otherEventTags: multiSelectNames(props["OtherEvents"]),
		invitedAt:      dateStart(props["InvitedAt"]),
		viewedAt:       dateStart(props["ViewedAt"]),
		acceptedAt:     dateStart(props["AcceptedAt"]),
	}
}

func validateSpeakerConfRows(rows []*speakerConfImportRow, speakersByRef map[string]*types.Speaker, proposalsByRef map[string]*types.Proposal, orgsByRef map[string]*types.Org) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		if row.speakerRef == "" {
			return fmt.Errorf("speaker conf %q has empty speaker relation", row.ref)
		}
		if speakersByRef[row.speakerRef] == nil {
			return fmt.Errorf("speaker conf %q has unresolved speaker ref", row.ref)
		}
		if row.orgRef != "" && orgsByRef[row.orgRef] == nil {
			return fmt.Errorf("speaker conf %q has unresolved org ref", row.ref)
		}
		for _, proposalRef := range row.proposalRefs {
			if proposalsByRef[proposalRef] == nil {
				return fmt.Errorf("speaker conf %q has unresolved proposal ref", row.ref)
			}
		}
	}
	return nil
}

func importSpeakerConfsRows(ctx context.Context, pool *pgxpool.Pool, rows []*speakerConfImportRow, speakerIDsByRef, orgIDsByRef, proposalIDsByRef map[string]string) error {
	for _, row := range rows {
		if row == nil {
			continue
		}
		speakerID := speakerIDsByRef[row.speakerRef]
		if speakerID == "" {
			return fmt.Errorf("speaker conf %q has unresolved imported speaker", row.ref)
		}
		orgID := ""
		if row.orgRef != "" {
			orgID = orgIDsByRef[row.orgRef]
		}

		var speakerConfID string
		err := pool.QueryRow(ctx, `
				INSERT INTO speaker_confs (
					speaker_id, organization_id, coming_from, availability,
					record_ok, visa, first_event, dinner_rsvp, sponsor, company,
					org_photo_path, invited_at, viewed_at, accepted_at
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
				RETURNING id::text
			`, speakerID, nullableString(orgID), row.comingFrom, row.availability,
			row.recordOK, row.visa, row.firstEvent, row.dinnerRSVP, row.sponsor, row.company,
			row.orgPhoto, nullableTimePtr(row.invitedAt), nullableTimePtr(row.viewedAt), nullableTimePtr(row.acceptedAt)).Scan(&speakerConfID)
		if err != nil {
			return fmt.Errorf("insert speaker conf %q: %w", row.ref, err)
		}

		for _, tag := range row.otherEventTags {
			if strings.TrimSpace(tag) == "" {
				continue
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO speaker_confs_conferences (speaker_conf_id, conference_id)
				SELECT $1, id
				FROM conferences
				WHERE tag = $2
				ON CONFLICT DO NOTHING
			`, speakerConfID, tag); err != nil {
				return fmt.Errorf("insert speaker conf other event %q/%q: %w", row.ref, tag, err)
			}
		}

		for _, proposalRef := range row.proposalRefs {
			proposalID := proposalIDsByRef[proposalRef]
			if proposalID == "" {
				return fmt.Errorf("speaker conf %q has unresolved imported proposal", row.ref)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO proposals_speaker_confs (proposal_id, speaker_conf_id)
				VALUES ($1, $2)
				ON CONFLICT DO NOTHING
			`, proposalID, speakerConfID); err != nil {
				return fmt.Errorf("insert proposal speaker conf %q/%q: %w", proposalRef, row.ref, err)
			}
		}
	}
	return nil
}

func validateSpeakerConfs(ctx context.Context, pool *pgxpool.Pool, rows []*speakerConfImportRow) error {
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM speaker_confs`).Scan(&count); err != nil {
		return fmt.Errorf("count speaker confs: %w", err)
	}
	if count < len(rows) {
		return fmt.Errorf("postgres speaker conf count %d is less than Notion count %d", count, len(rows))
	}

	expectedLinks := 0
	for _, row := range rows {
		if row != nil {
			expectedLinks += len(row.proposalRefs)
		}
	}
	var linkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM proposals_speaker_confs`).Scan(&linkCount); err != nil {
		return fmt.Errorf("count proposal speaker conf links: %w", err)
	}
	if linkCount < expectedLinks {
		return fmt.Errorf("postgres proposal speaker conf link count %d is less than Notion relation count %d", linkCount, expectedLinks)
	}
	return nil
}

func relationID(prop notion.PropertyValue) string {
	for _, ref := range prop.Relation {
		if ref != nil && ref.ID != "" {
			return ref.ID
		}
	}
	return ""
}

func relationIDs(prop notion.PropertyValue) []string {
	out := make([]string, 0, len(prop.Relation))
	for _, ref := range prop.Relation {
		if ref != nil && ref.ID != "" {
			out = append(out, ref.ID)
		}
	}
	return out
}

func richText(prop notion.PropertyValue) string {
	var sb strings.Builder
	for _, text := range prop.RichText {
		if text != nil && text.Text != nil {
			sb.WriteString(text.Text.Content)
		}
	}
	return sb.String()
}

func titleText(prop notion.PropertyValue) string {
	var sb strings.Builder
	for _, text := range prop.Title {
		if text != nil && text.Text != nil {
			sb.WriteString(text.Text.Content)
		}
	}
	return sb.String()
}

func selectName(prop notion.PropertyValue) string {
	if prop.Select == nil {
		return ""
	}
	return prop.Select.Name
}

func multiSelectNames(prop notion.PropertyValue) []string {
	if prop.MultiSelect == nil {
		return nil
	}
	out := make([]string, 0, len(*prop.MultiSelect))
	for _, opt := range *prop.MultiSelect {
		if opt != nil {
			out = append(out, opt.Name)
		}
	}
	return out
}

func checkbox(value *bool) bool {
	return value != nil && *value
}

func dateStart(prop notion.PropertyValue) *time.Time {
	if prop.Date == nil {
		return nil
	}
	return &prop.Date.Start
}

func uniqueID(prop notion.PropertyValue) uint64 {
	if prop.UniqueID == nil {
		return 0
	}
	return uint64(prop.UniqueID.Number)
}
