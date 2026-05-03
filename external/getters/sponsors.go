package getters

import (
	"context"
	"fmt"
	"strings"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
	"github.com/niftynei/go-notion"
)

func parseOrg(pageID string, props map[string]notion.PropertyValue) *types.Org {
	return &types.Org{
		Ref:       pageID,
		Name:      parseRichText("Name", props),
		Tagline:      parseRichText("Tagline", props),
		LogoLight: props["LogoLight"].URL,
		LogoDark:  props["LogoDark"].URL,
		Email:     props["Email"].Email,
		Github:    props["Github"].URL,
		Website:   props["Website"].URL,
		Twitter:   types.ParseTwitter(parseRichText("Twitter", props)),
		Nostr:     parseRichText("Nostr", props),
		Matrix:   parseRichText("Matrix", props),
		LinkedIn:  props["LinkedIn"].URL,
		Instagram: props["Instagram"].URL,
		Youtube:   props["Youtube"].URL,
		Hiring:    parseCheckbox(props["Hiring"].Checkbox),
		Notes:     parseRichText("Notes", props),
	}
}

func parseSponsorship(ctx *config.AppContext, pageID string, props map[string]notion.PropertyValue, orgs []*types.Org) *types.Sponsorship {
	sp := &types.Sponsorship{
		Ref:           pageID,
		Level:         parseSelect("Level", props),
		Status:        parseSelect("Status", props),
		IsVendor:      parseCheckbox(props["IsVendor"].Checkbox),
		Notes:         parseRichText("Notes", props),
                Confs:         parseConfList(ctx, "event", props),
                Org:           parseOrgOne(ctx, "org", props),
	}

	return sp
}

func ListOrgs(n *types.Notion) ([]*types.Org, error) {
	var orgs []*types.Org

	hasMore := true
	nextCursor := ""
	for hasMore {
		var err error
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.OrgDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			org := parseOrg(page.ID, page.Properties)
			orgs = append(orgs, org)
		}
	}

	return orgs, nil
}

func GetOrg(n *types.Notion, ref string) (*types.Org, error) {
	orgs, err := ListOrgs(n)
	if err != nil {
		return nil, err
	}
	for _, o := range orgs {
		if o.Ref == ref {
			return o, nil
		}
	}
	return nil, fmt.Errorf("org %s not found", ref)
}

func ListSponsorships(ctx *config.AppContext, confRef string) ([]*types.Sponsorship, error) {
	n := ctx.Notion
	cachedOrgs, err := FetchOrgsCached(ctx)
	if err != nil {
		return nil, err
	}

	var sponsorships []*types.Sponsorship

	var filter *notion.Filter
	if confRef != "" {
		filter = &notion.Filter{
			Property: "event",
			Relation: &notion.RelationFilterCondition{
				Contains: confRef,
			},
		}
	}

	hasMore := true
	nextCursor := ""
	for hasMore {
		var pages []*notion.Page

		pages, nextCursor, hasMore, err = n.Client.QueryDatabase(context.Background(),
			n.Config.SponsorshipsDb, notion.QueryDatabaseParam{
				StartCursor: nextCursor,
                                Filter:      filter,
			})

		if err != nil {
			return nil, err
		}
		for _, page := range pages {
			sp := parseSponsorship(ctx, page.ID, page.Properties, cachedOrgs)
			sponsorships = append(sponsorships, sp)
		}
	}

	return sponsorships, nil
}

func richText(s string) []*notion.RichText {
	return []*notion.RichText{
		{Type: notion.RichTextText, Text: &notion.Text{Content: s}},
	}
}

func RegisterOrg(n *types.Notion, org *types.Org) (string, error) {
	props := map[string]*notion.PropertyValue{
		"Name": notion.NewTitlePropertyValue(richText(org.Name)...),
	}

	if org.Twitter.Handle != "" {
		props["Twitter"] = notion.NewRichTextPropertyValue(richText(org.Twitter.Handle)...)
	}
	if org.Nostr != "" {
		props["Nostr"] = notion.NewRichTextPropertyValue(richText(org.Nostr)...)
	}
	if org.Matrix != "" {
		props["Matrix"] = notion.NewRichTextPropertyValue(richText(org.Matrix)...)
	}
	if org.Notes != "" {
		props["Notes"] = notion.NewRichTextPropertyValue(richText(org.Notes)...)
	}

	if org.LogoLight != "" {
		props["LogoLight"] = notion.NewURLPropertyValue(org.LogoLight)
	}
	if org.LogoDark != "" {
		props["LogoDark"] = notion.NewURLPropertyValue(org.LogoDark)
	}
	if org.Email != "" {
		props["Email"] = notion.NewEmailPropertyValue(org.Email)
	}
	if org.Website != "" {
		props["Website"] = notion.NewURLPropertyValue(org.Website)
	}
	if org.LinkedIn != "" {
		props["LinkedIn"] = notion.NewURLPropertyValue(org.LinkedIn)
	}
	if org.Instagram != "" {
		props["Instagram"] = notion.NewURLPropertyValue(org.Instagram)
	}
	if org.Youtube != "" {
		props["Youtube"] = notion.NewURLPropertyValue(org.Youtube)
	}
	if org.Github != "" {
		props["Github"] = notion.NewURLPropertyValue(org.Github)
	}

	page, err := n.Client.CreatePage(context.Background(),
		notion.NewDatabaseParent(n.Config.OrgDb), props)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

// OrgUpdate is a sparse fill-only update for an existing Org row. Empty
// values are skipped.
type OrgUpdate struct {
	Website   string
	Twitter   string // bare handle
	Nostr     string
	Github    string
	LogoLight string // full Spaces URL
	LogoDark  string
}

func UpdateOrg(n *types.Notion, orgID string, up OrgUpdate) error {
	props := map[string]*notion.PropertyValue{}
	if up.Website != "" {
		props["Website"] = notion.NewURLPropertyValue(up.Website)
	}
	if up.Twitter != "" {
		props["Twitter"] = notion.NewRichTextPropertyValue(richText(up.Twitter)...)
	}
	if up.Nostr != "" {
		props["Nostr"] = notion.NewRichTextPropertyValue(richText(up.Nostr)...)
	}
	if up.Github != "" {
		props["Github"] = notion.NewURLPropertyValue(up.Github)
	}
	if up.LogoLight != "" {
		props["LogoLight"] = notion.NewURLPropertyValue(up.LogoLight)
	}
	if up.LogoDark != "" {
		props["LogoDark"] = notion.NewURLPropertyValue(up.LogoDark)
	}
	if len(props) == 0 {
		return nil
	}
	_, err := n.Client.UpdatePageProperties(context.Background(), orgID, props)
	return err
}

// FindOrg returns the first Org whose Website matches `website` (preferred),
// or whose Name matches `name` (fallback). Both sides are normalized
// (lowercase + trim, websites also strip trailing /). Returns nil, nil when
// no match — letting callers decide whether to create a new Org.
func FindOrg(n *types.Notion, website, name string) (*types.Org, error) {
	wantSite := normalizeWebsite(website)
	wantName := normalizeName(name)
	if wantSite == "" && wantName == "" {
		return nil, nil
	}
	orgs, err := ListOrgs(n)
	if err != nil {
		return nil, err
	}
	if wantSite != "" {
		for _, o := range orgs {
			if normalizeWebsite(o.Website) == wantSite {
				return o, nil
			}
		}
	}
	if wantName != "" {
		for _, o := range orgs {
			if normalizeName(o.Name) == wantName {
				return o, nil
			}
		}
	}
	return nil, nil
}

func normalizeWebsite(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, "/")
	return s
}

func normalizeName(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}

func RegisterSponsorship(n *types.Notion, sp *types.Sponsorship) error {
	name := sp.Level + " Sponsorship"
	if sp.Org != nil {
		name = sp.Org.Name + " @ " + sp.Level
	}

	props := map[string]*notion.PropertyValue{
		"Name":  notion.NewTitlePropertyValue(richText(name)...),
		"Notes": notion.NewRichTextPropertyValue(richText(sp.Notes)...),
	}

	if sp.Org != nil {
		props["Org"] = notion.NewRelationPropertyValue(
			[]*notion.ObjectReference{{ID: sp.Org.Ref}}...,
		)
	}
	if len(sp.Confs) > 0 {
		refs := make([]*notion.ObjectReference, len(sp.Confs))
		for i, c := range sp.Confs {
			refs[i] = &notion.ObjectReference{ID: c.Ref}
		}
		props["event"] = notion.NewRelationPropertyValue(refs...)
	}
	if sp.Level != "" {
		props["Level"] = &notion.PropertyValue{
			Type:   notion.PropertySelect,
			Select: &notion.SelectOption{Name: sp.Level},
		}
	}
	if sp.Status != "" {
		props["Status"] = &notion.PropertyValue{
			Type:   notion.PropertySelect,
			Select: &notion.SelectOption{Name: sp.Status},
		}
	}

	_, err := n.Client.CreatePage(context.Background(),
		notion.NewDatabaseParent(n.Config.SponsorshipsDb), props)
	return err
}

func UpdateSponsorshipStatus(n *types.Notion, ref string, status string) error {
	_, err := n.Client.UpdatePageProperties(context.Background(), ref,
		map[string]*notion.PropertyValue{
			"Status": {
				Type:   notion.PropertySelect,
				Select: &notion.SelectOption{Name: status},
			},
		})
	return err
}
