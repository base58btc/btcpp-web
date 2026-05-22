package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"btcpp-web/internal/types"
)

func validateConfTalkDuplicateConfig(env *types.EnvConfig) error {
	var missing []string
	if strings.TrimSpace(env.Notion.Token) == "" {
		missing = append(missing, "NOTION_TOKEN")
	}
	if strings.TrimSpace(env.Notion.ProposalDb) == "" {
		missing = append(missing, "NOTION_PROPOSAL_DB")
	}
	if strings.TrimSpace(env.Notion.ConfTalkDb) == "" {
		missing = append(missing, "NOTION_CONFTALK_DB")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func printConfTalkProposalDuplicates(rows []*confTalkImportRow, proposalsByRef map[string]*types.Proposal) {
	byProposal := make(map[string][]*confTalkImportRow)
	for _, row := range rows {
		if row == nil || row.proposalRef == "" {
			continue
		}
		byProposal[row.proposalRef] = append(byProposal[row.proposalRef], row)
	}

	proposalRefs := make([]string, 0)
	extraRows := 0
	for proposalRef, matches := range byProposal {
		if len(matches) <= 1 {
			continue
		}
		proposalRefs = append(proposalRefs, proposalRef)
		extraRows += len(matches) - 1
	}
	sort.Strings(proposalRefs)

	fmt.Printf("conf_talk_rows=%d duplicate_proposal_refs=%d extra_rows=%d\n", len(rows), len(proposalRefs), extraRows)
	for _, proposalRef := range proposalRefs {
		matches := byProposal[proposalRef]
		sort.Slice(matches, func(i, j int) bool {
			return matches[i].ref < matches[j].ref
		})

		title := ""
		if proposal := proposalsByRef[proposalRef]; proposal != nil {
			title = proposal.Title
		}
		fmt.Printf("\nproposal_ref=%s\n", proposalRef)
		fmt.Printf("proposal_url=%s\n", notionPageURL(proposalRef))
		if title != "" {
			fmt.Printf("proposal_title=%q\n", title)
		}
		fmt.Printf("conf_talk_count=%d\n", len(matches))
		for _, row := range matches {
			fmt.Printf("  conf_talk_ref=%s url=%s event=%q venue=%q section=%q talk_time=%q clipart=%q\n",
				row.ref,
				notionPageURL(row.ref),
				row.confTag,
				row.venue,
				row.section,
				formatTimeRange(row.scheduledStart, row.scheduledEnd),
				row.clipart,
			)
		}
	}
}

func notionPageURL(id string) string {
	return "https://www.notion.so/" + strings.ReplaceAll(id, "-", "")
}

func formatTimeRange(start, end *time.Time) string {
	if start == nil || start.IsZero() {
		return ""
	}
	out := start.Format(time.RFC3339)
	if end != nil && !end.IsZero() {
		out += " -> " + end.Format(time.RFC3339)
	}
	return out
}
