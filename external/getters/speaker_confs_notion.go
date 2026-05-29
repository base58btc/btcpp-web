package getters

import (
	"context"
	"fmt"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

func fetchSpeakerConfWithSpeakerNotion(ctx *config.AppContext, speakerConfID string) (*types.SpeakerConf, error) {
	page, err := ctx.Notion.Client.RetrievePage(context.Background(), speakerConfID)
	if err != nil {
		return nil, fmt.Errorf("retrieve speakerconf %s: %w", speakerConfID, err)
	}
	speakerID := parseRef(page.Properties, "speaker")
	if speakerID == "" {
		return nil, nil
	}
	spPage, err := ctx.Notion.Client.RetrievePage(context.Background(), speakerID)
	if err != nil {
		return nil, fmt.Errorf("retrieve speaker %s: %w", speakerID, err)
	}
	speaker := parseSpeaker(spPage.ID, spPage.Properties)
	speakerMap := map[string]*types.Speaker{speakerID: speaker}
	return parseSpeakerConf(ctx, page.ID, page.Properties, speakerMap, nil), nil
}
