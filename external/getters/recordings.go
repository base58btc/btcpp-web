package getters

import (
	"time"

	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// RecordingPublishingUpdate mirrors final published URLs onto the Recording
// row. Workflow state (status, errors, timestamps) lives in SocialPosts.
type RecordingPublishingUpdate struct {
	YTLink     *string
	XLink      *string
	XReplyLink *string
}

// getRecordings refreshes the Recording cache + by-ConfTalk index.
func getRecordings(ctx *config.AppContext) {
	ctx.Infos.Printf("getting recordings...")
	recs, err := ListRecordings(ctx)
	if err != nil {
		ctx.Err.Printf("error fetching recordings %s", err)
		return
	}
	byCT := make(map[string]*types.Recording, len(recs))
	for _, r := range recs {
		if r != nil && r.ConfTalkID != "" {
			byCT[r.ConfTalkID] = r
		}
	}
	recordingCacheMu.Lock()
	cacheRecordings = recs
	recordingByConfTalk = byCT
	recordingCacheMu.Unlock()
	ctx.Infos.Printf("Loaded %d recordings!", len(recs))
}

func ListRecordings(ctx *config.AppContext) ([]*types.Recording, error) {
	if UsePostgresBackend(ctx) {
		return listRecordingsPostgres(ctx)
	}
	return ListRecordingsNotion(ctx)
}

// FetchRecordingByConfTalk returns the cached Recording linked to confTalkID,
// or nil if none.
func FetchRecordingByConfTalk(confTalkID string) *types.Recording {
	recordingCacheMu.RLock()
	defer recordingCacheMu.RUnlock()
	return recordingByConfTalk[confTalkID]
}

// GetRecordingByConfTalk fetches the Recording row whose talk relation points
// at confTalkID. Cache-first; when warm, a missing entry means no recording
// exists and avoids a backend query.
func GetRecordingByConfTalk(ctx *config.AppContext, confTalkID string) (*types.Recording, error) {
	if r := FetchRecordingByConfTalk(confTalkID); r != nil {
		return r, nil
	}
	if cacheRecordingsWarm() {
		return nil, nil
	}
	if UsePostgresBackend(ctx) {
		return getRecordingByConfTalkPostgres(ctx, confTalkID)
	}
	return getRecordingByConfTalkNotion(ctx, confTalkID)
}

// FetchRecordingByID returns the cached Recording with the given page ID, or
// nil. The Recordings cache is by-ConfTalkID; this helper walks the slice so
// the admin-recordings page can address rows by their own ID.
func FetchRecordingByID(recordingID string) *types.Recording {
	recordingCacheMu.RLock()
	defer recordingCacheMu.RUnlock()
	for _, r := range cacheRecordings {
		if r != nil && r.ID == recordingID {
			return r
		}
	}
	return nil
}

// ListRecordingsCached returns the warm Recordings slice. Returns nil before
// the first fetch completes; callers can treat that as still booting and render
// an empty list rather than a hard error.
func ListRecordingsCached() []*types.Recording {
	recordingCacheMu.RLock()
	defer recordingCacheMu.RUnlock()
	if cacheRecordings == nil {
		return nil
	}
	out := make([]*types.Recording, len(cacheRecordings))
	copy(out, cacheRecordings)
	return out
}

// FetchYTLinkForTalk bridges the legacy Talks-DB renderer (which uses Talk.ID =
// Talks-DB page ID) to the Recording cache (keyed by ConfTalk.ID).
func FetchYTLinkForTalk(confTag, name string) string {
	if confTag == "" || name == "" {
		return ""
	}
	confTalkCacheMu.RLock()
	var matchID string
	for _, ct := range cacheConfTalks {
		if ct == nil || ct.Conf == nil || ct.Proposal == nil {
			continue
		}
		if ct.Conf.Tag == confTag && ct.Proposal.Title == name {
			matchID = ct.ID
			break
		}
	}
	confTalkCacheMu.RUnlock()
	if matchID == "" {
		return ""
	}
	if rec := FetchRecordingByConfTalk(matchID); rec != nil {
		return rec.YTLink
	}
	return ""
}

func cacheRecordingsWarm() bool {
	recordingCacheMu.RLock()
	defer recordingCacheMu.RUnlock()
	return cacheRecordings != nil
}

func InvalidateRecordingsCache() {
	recordingCacheMu.Lock()
	lastRecordingFetch = time.Time{}
	recordingCacheMu.Unlock()
}

func UpdateRecordingYTLink(ctx *config.AppContext, recordingID, ytLink string) error {
	if UsePostgresBackend(ctx) {
		return updateRecordingYTLinkPostgres(ctx, recordingID, ytLink)
	}
	return updateRecordingYTLinkNotion(ctx, recordingID, ytLink)
}

func UpdateRecordingXLink(ctx *config.AppContext, recordingID, xLink string) error {
	if UsePostgresBackend(ctx) {
		return updateRecordingXLinkPostgres(ctx, recordingID, xLink)
	}
	return updateRecordingXLinkNotion(ctx, recordingID, xLink)
}

func UpdateRecordingPublishAt(ctx *config.AppContext, recordingID string, publishAt *time.Time) error {
	if UsePostgresBackend(ctx) {
		return updateRecordingPublishAtPostgres(ctx, recordingID, publishAt)
	}
	return updateRecordingPublishAtNotion(ctx, recordingID, publishAt)
}

func UpdateRecordingFileURI(ctx *config.AppContext, recordingID, fileURI string) error {
	if UsePostgresBackend(ctx) {
		return updateRecordingFileURIPostgres(ctx, recordingID, fileURI)
	}
	return updateRecordingFileURINotion(ctx, recordingID, fileURI)
}

func UpdateRecordingPublishing(ctx *config.AppContext, recordingID string, up RecordingPublishingUpdate) error {
	if UsePostgresBackend(ctx) {
		return updateRecordingPublishingPostgres(ctx, recordingID, up)
	}
	return updateRecordingPublishingNotion(ctx, recordingID, up)
}

func patchRecordingCache(recordingID string, patch func(*types.Recording)) {
	recordingCacheMu.Lock()
	defer recordingCacheMu.Unlock()
	for _, r := range cacheRecordings {
		if r != nil && r.ID == recordingID {
			patch(r)
			return
		}
	}
}
