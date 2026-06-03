// admin_recordings.go wires the /{conf}/admin/recordings dashboard:
//   - list every Notion Recording row with per-row publish status
//   - per-recording detail page with editable YT + X copy
//   - YouTube OAuth bootstrap (start / callback / disconnect)
//   - YouTube upload kickoff + async status polling
//   - X browser automation status + manual fallback URL save
//
// X automation is implemented in admin_recordings_autopublish.go and
// external/xposter; this file keeps the dashboard, YouTube OAuth, and
// manual escape hatches.
package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/template"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/external/tokens"
	youtubepkg "btcpp-web/external/youtube"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

const youtubeOAuthStateKey = "yt_oauth_state"

const maxRecordingUploadBytes int64 = 25 << 30 // 25 GiB

const (
	recordingPlatformYouTube = "youtube"
	recordingPlatformX       = "twitter"
)

// ---- page data ---------------------------------------------------------

type RecordingRow struct {
	Recording         *types.Recording
	ConfTalk          *types.ConfTalk
	Speakers          []*types.Speaker
	YTSocialPost      *types.SocialPost
	XSocialPost       *types.SocialPost
	YTURL             string
	XURL              string
	XReplyURL         string
	YTStatus          string
	XStatus           string
	YTError           string
	XError            string
	XErrorFingerprint string
	YTPrivacyStatus   string
	YTUploadStatus    string
	YTPublishAt       *time.Time
	YTStatusError     string
	HasFile           bool
	HasYT             bool
	HasX              bool
}

type RecordingsAdminListPage struct {
	Rows               []*RecordingRow
	Conf               *types.Conf
	YouTubeReady       bool
	YouTubeAuthURL     string
	AutopublishEnabled bool
	XUploaderEnabled   bool
	XProfileObject     string
	FlashMessage       string
	FlashError         string
	Year               uint
}

type RecordingsAdminDetailPage struct {
	Conf             *types.Conf
	Row              *RecordingRow
	YTTitle          string
	YTBody           string
	XBody            string
	XReplyBody       string
	XIntentURL       string
	PublishAtInput   string
	PublishTimezone  string
	JobActive        bool
	JobStatus        string
	JobMessage       string
	XJobActive       bool
	XJobStatus       string
	XJobMessage      string
	YouTubeReady     bool
	XUploaderEnabled bool
	FlashMessage     string
	FlashError       string
	Year             uint
}

// ---- job tracker -------------------------------------------------------

// uploadJob captures the state of an in-flight YouTube upload, keyed
// by Recording ID. Only one job per recording at a time.
type uploadJob struct {
	Status    string // "running" | "succeeded" | "failed"
	Message   string
	Stage     string
	Progress  int
	StartedAt time.Time
	EndedAt   time.Time
}

var (
	jobsMu sync.Mutex
	jobs   = map[string]*uploadJob{}

	xJobsMu sync.Mutex
	xJobs   = map[string]*uploadJob{}
)

func getJob(recordingID string) *uploadJob {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	j := jobs[recordingID]
	if j == nil {
		return nil
	}
	cp := *j
	return &cp
}

func setJobStatus(recordingID, status, message string) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	j := jobs[recordingID]
	if j == nil {
		j = &uploadJob{StartedAt: time.Now()}
		jobs[recordingID] = j
	}
	j.Status = status
	j.Message = message
	if status == "succeeded" || status == "failed" {
		j.EndedAt = time.Now()
	}
}

func claimJob(recordingID string) bool {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	if j, ok := jobs[recordingID]; ok && j.Status == "running" {
		return false
	}
	jobs[recordingID] = &uploadJob{Status: "running", StartedAt: time.Now()}
	return true
}

func getXJob(recordingID string) *uploadJob {
	xJobsMu.Lock()
	defer xJobsMu.Unlock()
	j := xJobs[recordingID]
	if j == nil {
		return nil
	}
	cp := *j
	return &cp
}

func setXJobStatus(recordingID, status, message string) {
	setXJobProgress(recordingID, status, message, "", 0)
}

func setXJobProgress(recordingID, status, message, stage string, progress int) {
	xJobsMu.Lock()
	defer xJobsMu.Unlock()
	j := xJobs[recordingID]
	if j == nil {
		j = &uploadJob{StartedAt: time.Now()}
		xJobs[recordingID] = j
	}
	j.Status = status
	j.Message = message
	if stage != "" {
		j.Stage = stage
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	j.Progress = progress
	if status == "succeeded" || status == "failed" {
		j.EndedAt = time.Now()
	}
}

func setXJobStage(recordingID, stage, message string) {
	progress := 0
	if stage == "done" {
		progress = 100
	}
	setXJobProgress(recordingID, "running", message, stage, progress)
}

// ---- list page ---------------------------------------------------------

func RecordingsAdminList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, ok := requireRecordingsConfAdmin(w, r, ctx)
	if !ok {
		return
	}

	if _, err := getters.FetchSocialPostsCached(ctx); err != nil {
		ctx.Err.Printf("/%s/admin/recordings socialposts: %s", conf.Tag, err)
	}
	rows := recordingRowsForConf(ctx, conf.Tag)
	enrichRowsWithYouTubeStatus(ctx, rows)
	sort.SliceStable(rows, func(i, j int) bool {
		return rowSortKey(rows[i]) > rowSortKey(rows[j])
	})

	page := &RecordingsAdminListPage{
		Rows:               rows,
		Conf:               conf,
		YouTubeReady:       youtubepkg.IsConfigured() && youtubepkg.IsConnected(),
		YouTubeAuthURL:     recordingsAdminPath(conf.Tag, "/oauth/youtube/start"),
		AutopublishEnabled: ctx.Env.Recordings.AutopublishEnabled,
		XUploaderEnabled:   ctx.Env.Recordings.X.Enabled,
		XProfileObject:     ctx.Env.Recordings.X.ProfileObject,
		Year:               uint(time.Now().Year()),
	}
	if !youtubepkg.IsConfigured() {
		page.FlashError = "YouTube OAuth env vars (YOUTUBE_CLIENT_ID/SECRET) are not set — set them and restart to enable uploads."
	} else if !youtubepkg.IsConnected() {
		page.FlashError = "YouTube is configured but not connected. Click \"Authorize YouTube\" to grant upload access to the btcplusplus channel."
	}
	if flash := r.URL.Query().Get("flash"); flash != "" {
		page.FlashMessage = flash
		page.FlashError = ""
	}
	if flashErr := r.URL.Query().Get("err"); flashErr != "" {
		page.FlashError = flashErr
	}

	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/recordings.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/recordings render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

func recordingRowsForConf(ctx *config.AppContext, confTag string) []*RecordingRow {
	recs := getters.ListRecordingsCached()
	rows := recordingRowsFromList(recs, confTag)
	if len(rows) > 0 || len(recs) > 0 {
		return rows
	}

	// The admin page is a control surface; if the warm cache missed at
	// startup, do a synchronous refresh instead of rendering an empty page
	// that suggests the Notion DB itself has no rows.
	ctx.Infos.Printf("/%s/admin/recordings cache empty; refreshing Notion caches", confTag)
	getters.WaitFetch(ctx)
	recs = getters.ListRecordingsCached()
	rows = recordingRowsFromList(recs, confTag)
	if len(rows) > 0 || len(recs) > 0 {
		return rows
	}

	live, err := getters.ListRecordings(ctx)
	if err != nil {
		ctx.Err.Printf("/%s/admin/recordings live recordings fetch failed: %s", confTag, err)
		return rows
	}
	ctx.Infos.Printf("/%s/admin/recordings live recordings fallback loaded %d rows", confTag, len(live))
	return recordingRowsFromList(live, confTag)
}

func recordingRowsFromList(recs []*types.Recording, confTag string) []*RecordingRow {
	rows := make([]*RecordingRow, 0, len(recs))
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		row := buildRecordingRow(rec)
		if recordingRowBelongsToConf(row, confTag) {
			rows = append(rows, row)
		}
	}
	return rows
}

func enrichRowsWithYouTubeStatus(ctx *config.AppContext, rows []*RecordingRow) {
	if !youtubepkg.IsConfigured() || !youtubepkg.IsConnected() {
		return
	}
	for _, row := range rows {
		if row == nil || row.Recording == nil || strings.TrimSpace(row.Recording.YTLink) == "" {
			continue
		}
		videoID := youtubeVideoID(row.Recording.YTLink)
		if videoID == "" {
			row.YTStatusError = "could not parse video ID"
			continue
		}
		status, err := youtubepkg.GetVideoStatus(context.Background(), videoID)
		if err != nil {
			row.YTStatusError = err.Error()
			ctx.Err.Printf("recording youtube status recording=%s video=%s: %s", row.Recording.ID, videoID, err)
			continue
		}
		row.YTPrivacyStatus = status.PrivacyStatus
		row.YTUploadStatus = status.UploadStatus
		row.YTPublishAt = status.PublishAt
	}
}

// rowSortKey returns a string that, when sorted descending, puts the
// newest talks first. Falls back to title when the ConfTalk has no
// scheduled time (e.g., past talk imported without a timestamp).
func rowSortKey(row *RecordingRow) string {
	if row.ConfTalk != nil && row.ConfTalk.Sched != nil && !row.ConfTalk.Sched.Start.IsZero() {
		return row.ConfTalk.Sched.Start.UTC().Format(time.RFC3339)
	}
	return row.Recording.TalkName
}

// ---- detail page -------------------------------------------------------

func RecordingsAdminDetail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, row, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}

	ytTitle, ytBody := defaultYouTubeCopy(ctx, row)
	enrichRowsWithYouTubeStatus(ctx, []*RecordingRow{row})
	xBody := recordingXMainCopy(ctx, row)
	xReplyBody := defaultXReplyCopy(ctx, row)
	intentURL := "https://x.com/intent/post?" + url.Values{"text": []string{xBody}}.Encode()

	page := &RecordingsAdminDetailPage{
		Conf:             conf,
		Row:              row,
		YTTitle:          ytTitle,
		YTBody:           ytBody,
		XBody:            xBody,
		XReplyBody:       xReplyBody,
		XIntentURL:       intentURL,
		PublishAtInput:   recordingPublishAtInput(rec.PublishAt, conf),
		PublishTimezone:  recordingPublishTimezone(conf),
		YouTubeReady:     youtubepkg.IsConfigured() && youtubepkg.IsConnected(),
		XUploaderEnabled: ctx.Env.Recordings.X.Enabled,
		Year:             uint(time.Now().Year()),
	}
	if job := getJob(rec.ID); job != nil {
		page.JobActive = job.Status == "running"
		page.JobStatus = job.Status
		page.JobMessage = job.Message
	}
	if job := getXJob(rec.ID); job != nil {
		page.XJobActive = job.Status == "running"
		page.XJobStatus = job.Status
		page.XJobMessage = job.Message
	}
	if flash := r.URL.Query().Get("flash"); flash != "" {
		page.FlashMessage = flash
	}
	if flashErr := r.URL.Query().Get("err"); flashErr != "" {
		page.FlashError = flashErr
	}

	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/recording_detail.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/recordings/%s render: %s", conf.Tag, rec.ID, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// ---- upload source recording to Spaces --------------------------------

func RecordingsAdminUploadSourceFile(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, _, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	if rec.FileURI != "" {
		redirectRecordingsListErr(w, r, conf.Tag, "Recording already has a FileURI")
		return
	}
	if !spaces.IsConfigured() {
		redirectRecordingsListErr(w, r, conf.Tag, "Spaces is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRecordingUploadBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, "couldn't parse uploaded recording: "+err.Error())
		return
	}
	file, handler, err := r.FormFile("recording_file")
	if err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, "choose a recording file to upload")
		return
	}
	defer file.Close()

	key, err := recordingUploadKey(conf.Tag, rec.ID, handler.Filename)
	if err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, err.Error())
		return
	}
	contentType := handler.Header.Get("Content-Type")
	if contentType == "" || contentType == "application/octet-stream" {
		if detected := mime.TypeByExtension(filepath.Ext(handler.Filename)); detected != "" {
			contentType = detected
		}
	}
	if contentType == "" {
		contentType = "video/mp4"
	}
	if !allowedRecordingUploadType(contentType, handler.Filename) {
		redirectRecordingsListErr(w, r, conf.Tag, "recording upload must be a video file")
		return
	}

	if spaces.Exists(key) {
		if err := getters.UpdateRecordingFileURI(ctx, rec.ID, key); err != nil {
			redirectRecordingsListErr(w, r, conf.Tag, "file already exists in Spaces, but couldn't update Notion: "+err.Error())
			return
		}
		redirectRecordingsList(w, r, conf.Tag, "Recording file already existed in Spaces; linked it in Notion")
		return
	}
	if _, err := spaces.UploadStream(key, file, contentType, handler.Size); err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, "couldn't upload recording to Spaces: "+err.Error())
		return
	}
	if err := getters.UpdateRecordingFileURI(ctx, rec.ID, key); err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, "uploaded to Spaces, but couldn't update Notion FileURI: "+err.Error())
		return
	}
	redirectRecordingsList(w, r, conf.Tag, "Recording file uploaded")
}

func recordingUploadKey(confTag, recordingID, filename string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		return "", fmt.Errorf("recording upload filename needs a video extension")
	}
	return fmt.Sprintf("recordings/%s/%s%s", slugPathSegment(confTag), recordingID, ext), nil
}

func allowedRecordingUploadType(contentType, filename string) bool {
	if strings.HasPrefix(strings.ToLower(contentType), "video/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp4", ".mov", ".m4v", ".webm":
		return true
	default:
		return false
	}
}

func slugPathSegment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

// ---- upload to YouTube ------------------------------------------------

func RecordingsAdminUploadYT(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, _, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	recordingID := rec.ID
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse form: "+err.Error())
		return
	}
	title := strings.TrimSpace(r.FormValue("yt_title"))
	body := r.FormValue("yt_body")
	privacy := strings.TrimSpace(r.FormValue("privacy"))
	if privacy == "" {
		privacy = "public"
	}
	var publishAt time.Time
	if rec.PublishAt != nil && rec.PublishAt.After(time.Now()) {
		privacy = "private"
		publishAt = rec.PublishAt.UTC()
	}
	if title == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "YouTube title is required")
		return
	}
	if rec.FileURI == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "Recording row has no FileURI — set the Spaces key in Notion first")
		return
	}
	if !youtubepkg.IsConfigured() {
		redirectWithErr(w, r, conf.Tag, recordingID, "YouTube OAuth is not configured")
		return
	}
	if !youtubepkg.IsConnected() {
		redirectWithErr(w, r, conf.Tag, recordingID, "YouTube is not connected — click Authorize on the recordings page")
		return
	}
	if !claimJob(recordingID) {
		redirectWithErr(w, r, conf.Tag, recordingID, "An upload is already in progress for this recording")
		return
	}
	go runYouTubeUpload(ctx, rec, title, body, privacy, publishAt)

	http.Redirect(w, r, recordingDetailPath(conf.Tag, recordingID)+"?flash=Upload+started", http.StatusSeeOther)
}

func RecordingsAdminSchedule(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, _, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	recordingID := rec.ID
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse form: "+err.Error())
		return
	}

	var publishAt *time.Time
	if r.FormValue("action") != "clear" {
		raw := strings.TrimSpace(r.FormValue("publish_at"))
		if raw == "" {
			redirectWithErr(w, r, conf.Tag, recordingID, "Choose a publish time or clear the schedule")
			return
		}
		when, err := parseRecordingPublishAt(raw, conf)
		if err != nil {
			redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse publish time: "+err.Error())
			return
		}
		if strings.TrimSpace(rec.YTLink) != "" && !when.After(time.Now()) {
			redirectWithErr(w, r, conf.Tag, recordingID, "YouTube scheduled publish time must be in the future")
			return
		}
		publishAt = &when
	}

	if err := getters.UpdateRecordingPublishAt(ctx, recordingID, publishAt); err != nil {
		ctx.Err.Printf("schedule recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update PublishAt: "+err.Error())
		return
	}
	ytScheduleResult, err := updateRecordingYouTubeSchedule(ctx, rec, publishAt)
	if err != nil {
		ctx.Err.Printf("recording youtube schedule recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "saved Notion PublishAt, but couldn't update YouTube schedule: "+err.Error())
		return
	}
	flash := "Schedule cleared"
	if publishAt != nil {
		flash = "Schedule saved"
		if ytScheduleResult == "updated" {
			flash = "Schedule saved and YouTube updated"
		} else if ytScheduleResult == "public" {
			flash = "Schedule saved. YouTube is already public, so it was not changed."
		}
	} else if ytScheduleResult == "updated" {
		flash = "Schedule cleared and YouTube set to unlisted"
	} else if ytScheduleResult == "public" {
		flash = "Schedule cleared. YouTube is already public, so it was not changed."
	}
	http.Redirect(w, r, recordingDetailPath(conf.Tag, recordingID)+"?flash="+url.QueryEscape(flash), http.StatusSeeOther)
}

func updateRecordingYouTubeSchedule(ctx *config.AppContext, rec *types.Recording, publishAt *time.Time) (string, error) {
	if rec == nil || strings.TrimSpace(rec.YTLink) == "" || !youtubepkg.IsConfigured() || !youtubepkg.IsConnected() {
		return "", nil
	}
	videoID := youtubeVideoID(rec.YTLink)
	if videoID == "" {
		return "", fmt.Errorf("could not parse video ID from %q", rec.YTLink)
	}
	status, err := youtubepkg.GetVideoStatus(context.Background(), videoID)
	if err != nil {
		return "", err
	}
	if status.PrivacyStatus == "public" {
		return "public", nil
	}
	if publishAt != nil {
		return "updated", youtubepkg.ScheduleExistingVideo(context.Background(), videoID, *publishAt)
	}
	return "updated", youtubepkg.ClearExistingVideoSchedule(context.Background(), videoID)
}

func RecordingsAdminSaveXCopy(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, row, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	recordingID := rec.ID
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse form: "+err.Error())
		return
	}
	xBody := strings.TrimSpace(r.FormValue("x_body"))
	if xBody == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "X post text is required")
		return
	}
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{Text: &xBody}); err != nil {
		ctx.Err.Printf("save x copy recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update SocialPosts: "+err.Error())
		return
	}
	http.Redirect(w, r, recordingDetailPath(conf.Tag, recordingID)+"?flash=X+text+saved", http.StatusSeeOther)
}

func RecordingsAdminPostXNow(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, row, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	recordingID := rec.ID
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse form: "+err.Error())
		return
	}
	xBody := strings.TrimSpace(r.FormValue("x_body"))
	if xBody == "" {
		xBody = strings.TrimSpace(recordingXMainCopy(ctx, row))
	}
	if xBody == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "X post text is required")
		return
	}
	if rec.FileURI == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "Recording row has no FileURI — set the Spaces key in Notion first")
		return
	}
	if row.XURL != "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "X already has a saved URL")
		return
	}
	if row.XStatus == recordingStatusScheduling || row.XStatus == recordingStatusScheduled || row.XStatus == recordingStatusPosting {
		redirectWithErr(w, r, conf.Tag, recordingID, "X is already "+row.XStatus)
		return
	}
	if !ctx.Env.Recordings.X.Enabled {
		redirectWithErr(w, r, conf.Tag, recordingID, "X uploader is disabled")
		return
	}
	client, err := newXPosterClient(ctx)
	if err != nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "X uploader is not configured: "+err.Error())
		return
	}

	status := recordingStatusPosting
	clear := ""
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		Text:             &xBody,
		Status:           &status,
		Error:            &clear,
		ErrorFingerprint: &clear,
	}); err != nil {
		ctx.Err.Printf("post x status recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update SocialPosts: "+err.Error())
		return
	}
	setXJobStatus(recordingID, "running", "Starting X post")
	go runXPostNow(ctx, row, client, xBody)

	http.Redirect(w, r, recordingDetailPath(conf.Tag, recordingID)+"?flash=X+post+started", http.StatusSeeOther)
}

func RecordingsAdminScheduleX(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, row, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	recordingID := rec.ID
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse form: "+err.Error())
		return
	}
	xBody := strings.TrimSpace(r.FormValue("x_body"))
	if xBody == "" {
		xBody = strings.TrimSpace(recordingXMainCopy(ctx, row))
	}
	if xBody == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "X post text is required")
		return
	}
	if rec.FileURI == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "Recording row has no FileURI — set the Spaces key in Notion first")
		return
	}
	publishAt := rec.PublishAt
	rawPublishAt := strings.TrimSpace(r.FormValue("publish_at"))
	if rawPublishAt != "" {
		when, err := parseRecordingPublishAt(rawPublishAt, conf)
		if err != nil {
			redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse publish time: "+err.Error())
			return
		}
		publishAt = &when
	}
	if publishAt == nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "Set PublishAt before scheduling on X")
		return
	}
	if !publishAt.After(time.Now()) {
		redirectWithErr(w, r, conf.Tag, recordingID, "PublishAt must be in the future to schedule on X")
		return
	}
	if row.XURL != "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "X already has a saved URL")
		return
	}
	if row.XStatus == recordingStatusScheduling || row.XStatus == recordingStatusScheduled || row.XStatus == recordingStatusPosting {
		redirectWithErr(w, r, conf.Tag, recordingID, "X is already "+row.XStatus)
		return
	}
	if !ctx.Env.Recordings.X.Enabled {
		redirectWithErr(w, r, conf.Tag, recordingID, "X uploader is disabled")
		return
	}
	if rec.PublishAt == nil || !rec.PublishAt.Equal(*publishAt) {
		if err := getters.UpdateRecordingPublishAt(ctx, recordingID, publishAt); err != nil {
			ctx.Err.Printf("schedule x publishAt recording=%s: %s", recordingID, err)
			redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update PublishAt: "+err.Error())
			return
		}
		rec.PublishAt = publishAt
		row.Recording.PublishAt = publishAt
	}
	if _, err := updateRecordingYouTubeSchedule(ctx, rec, publishAt); err != nil {
		ctx.Err.Printf("schedule x youtube recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update YouTube schedule: "+err.Error())
		return
	}

	status := recordingStatusScheduling
	clear := ""
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		Text:             &xBody,
		Status:           &status,
		Error:            &clear,
		ErrorFingerprint: &clear,
		ScheduledAt:      publishAt,
	}); err != nil {
		ctx.Err.Printf("schedule x status recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update SocialPosts: "+err.Error())
		return
	}
	setXJobStatus(recordingID, "running", "Starting X schedule")
	go runXSchedule(ctx, rec, conf, xBody)

	http.Redirect(w, r, recordingDetailPath(conf.Tag, recordingID)+"?flash=X+scheduling+started", http.StatusSeeOther)
}

// runYouTubeUpload streams the source video from Spaces straight into
// YouTube's resumable-upload endpoint, then writes the resulting URL
// back to the Notion Recording row. Uses a fresh context.Background()
// because the HTTP request that kicked us off has already returned.
func runYouTubeUpload(ctx *config.AppContext, rec *types.Recording, title, body, privacy string, publishAt time.Time) {
	recordingID := rec.ID
	defer func() {
		if rec := recover(); rec != nil {
			ctx.Err.Printf("youtube upload panic recording=%s: %v", recordingID, rec)
			setJobStatus(recordingID, "failed", fmt.Sprintf("internal error: %v", rec))
		}
	}()
	row := buildRecordingRow(rec)
	status := recordingStatusUploading
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformYouTube, getters.SocialPostUpdate{
		Text:   &body,
		Status: &status,
	}); err != nil {
		ctx.Err.Printf("youtube upload: socialpost status recording=%s: %s", recordingID, err)
	}
	src, size, err := openRecordingSourceStream(rec.FileURI)
	if err != nil {
		ctx.Err.Printf("youtube upload: fetch %s: %s", rec.FileURI, err)
		setJobStatus(recordingID, "failed", "couldn't fetch source video from Spaces: "+err.Error())
		msg := "couldn't fetch source video from Spaces: " + err.Error()
		status = recordingStatusFailed
		_ = upsertRecordingSocialPost(ctx, row, recordingPlatformYouTube, getters.SocialPostUpdate{Status: &status, Error: &msg})
		return
	}
	defer src.Close()

	bg := context.Background()
	ytURL, err := youtubepkg.Upload(bg, youtubepkg.UploadParams{
		Title:         title,
		Description:   body,
		PrivacyStatus: privacy,
		PublishAt:     publishAt,
	}, src, size)
	if err != nil {
		ctx.Err.Printf("youtube upload: %s", err)
		setJobStatus(recordingID, "failed", err.Error())
		msg := err.Error()
		status = recordingStatusFailed
		_ = upsertRecordingSocialPost(ctx, row, recordingPlatformYouTube, getters.SocialPostUpdate{Status: &status, Error: &msg})
		return
	}
	now := time.Now()
	status = recordingStatusUploaded
	if err := getters.UpdateRecordingYTLink(ctx, recordingID, ytURL); err != nil {
		ctx.Err.Printf("youtube upload: persist YTLink: %s", err)
		setJobStatus(recordingID, "failed", "uploaded to YouTube but failed to update Notion: "+err.Error())
		return
	}
	rec.YTLink = ytURL
	if err := uploadRecordingYouTubeThumbnail(context.Background(), rec); err != nil {
		ctx.Err.Printf("youtube upload: thumbnail recording=%s: %s", recordingID, err)
	}
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformYouTube, getters.SocialPostUpdate{
		URL:      &ytURL,
		Status:   &status,
		PostedAt: &now,
	}); err != nil {
		ctx.Err.Printf("youtube upload: persist socialpost recording=%s: %s", recordingID, err)
	}
	setJobStatus(recordingID, "succeeded", ytURL)
}

func uploadRecordingYouTubeThumbnail(ctx context.Context, rec *types.Recording) error {
	if rec == nil || rec.YTLink == "" || rec.ConfTalkID == "" {
		return nil
	}
	videoID := youtubeVideoID(rec.YTLink)
	if videoID == "" {
		return fmt.Errorf("could not parse video ID from %q", rec.YTLink)
	}
	key := recordingTalkCardKey(rec.ConfTalkID)
	if key == "" {
		return nil
	}
	data, err := spaces.Get(key)
	if err != nil {
		return fmt.Errorf("load talk card %s: %w", key, err)
	}
	return youtubepkg.SetThumbnailBytes(ctx, videoID, filepath.Base(key), data)
}

func recordingTalkCardKey(confTalkID string) string {
	ct := getters.FetchConfTalkByID(confTalkID)
	if ct == nil {
		return ""
	}
	if strings.TrimSpace(ct.SocialCard) != "" {
		return strings.TrimPrefix(strings.TrimSpace(ct.SocialCard), "/")
	}
	if ct.Conf == nil || ct.Conf.Tag == "" {
		return ""
	}
	return fmt.Sprintf("%s/talks/%s-1080p.png", ct.Conf.Tag, ct.ID)
}

func youtubeVideoID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.Contains(host, "youtu.be"):
		return strings.Trim(strings.TrimPrefix(u.Path, "/"), "/")
	case strings.Contains(host, "youtube.com"):
		if id := u.Query().Get("v"); id != "" {
			return id
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && (parts[0] == "shorts" || parts[0] == "embed") {
			return parts[1]
		}
	}
	return ""
}

// ---- save X link (manual handoff) ------------------------------------

func RecordingsAdminSaveXLink(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, _, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	recordingID := rec.ID
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't parse form: "+err.Error())
		return
	}
	xURL := strings.TrimSpace(r.FormValue("x_url"))
	if xURL == "" {
		redirectWithErr(w, r, conf.Tag, recordingID, "Paste the X URL before saving")
		return
	}
	if !strings.HasPrefix(xURL, "https://x.com/") && !strings.HasPrefix(xURL, "https://twitter.com/") {
		redirectWithErr(w, r, conf.Tag, recordingID, "That doesn't look like an X.com URL")
		return
	}
	if err := getters.UpdateRecordingXLink(ctx, recordingID, xURL); err != nil {
		ctx.Err.Printf("save xlink recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update Notion: "+err.Error())
		return
	}
	status := recordingStatusPosted
	now := time.Now()
	if err := upsertRecordingSocialPost(ctx, buildRecordingRow(rec), recordingPlatformX, getters.SocialPostUpdate{
		URL:      &xURL,
		Status:   &status,
		PostedAt: &now,
	}); err != nil {
		ctx.Err.Printf("save xlink socialpost recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, conf.Tag, recordingID, "couldn't update SocialPosts: "+err.Error())
		return
	}
	http.Redirect(w, r, recordingDetailPath(conf.Tag, recordingID)+"?flash=X+link+saved", http.StatusSeeOther)
}

// ---- job status polling ----------------------------------------------

func RecordingsAdminJobStatus(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	_, rec, _, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	job := getJob(rec.ID)
	w.Header().Set("Content-Type", "application/json")
	if job == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": ""})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  job.Status,
		"message": job.Message,
	})
}

func RecordingsAdminXJobStatus(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	_, rec, row, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	job := getXJob(rec.ID)
	w.Header().Set("Content-Type", "application/json")
	if job == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":   row.XStatus,
			"message":  row.XError,
			"stage":    "",
			"progress": 0,
			"url":      row.XURL,
			"reply":    row.XReplyURL,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   job.Status,
		"message":  job.Message,
		"stage":    job.Stage,
		"progress": job.Progress,
		"url":      row.XURL,
		"reply":    row.XReplyURL,
	})
}

// ---- YouTube OAuth bootstrap -----------------------------------------

func RecordingsYTOAuthStart(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, ok := requireRecordingsConfAdmin(w, r, ctx)
	if !ok {
		return
	}
	if !youtubepkg.IsConfigured() {
		http.Error(w, "YouTube OAuth env vars are not set", http.StatusServiceUnavailable)
		return
	}
	state := mintState(conf.Tag)
	ctx.Session.Put(r.Context(), youtubeOAuthStateKey, state)
	http.Redirect(w, r, youtubepkg.AuthCodeURLForRedirect(state, recordingsOAuthRedirectURL(ctx)), http.StatusSeeOther)
}

func RecordingsYTOAuthCallback(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	wantState, _ := ctx.Session.Pop(r.Context(), youtubeOAuthStateKey).(string)
	gotState := r.URL.Query().Get("state")
	if wantState == "" || gotState == "" || wantState != gotState {
		http.Error(w, "OAuth state mismatch — try again from the recordings page", http.StatusBadRequest)
		return
	}
	confTag := confTagFromOAuthState(wantState)
	if confTag == "" {
		confTag = mux.Vars(r)["conf"]
	}
	if confTag == "" {
		confTag = "vienna"
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		http.Error(w, "Google denied the request: "+errMsg, http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	if err := youtubepkg.ExchangeForRedirect(r.Context(), code, recordingsOAuthRedirectURL(ctx)); err != nil {
		ctx.Err.Printf("youtube oauth exchange: %s", err)
		http.Error(w, "OAuth exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, recordingsAdminPath(confTag, "?flash=YouTube+connected"), http.StatusSeeOther)
}

func RecordingsYTOAuthDisconnect(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, ok := requireRecordingsConfAdmin(w, r, ctx)
	if !ok {
		return
	}
	if err := youtubepkg.Disconnect(); err != nil {
		ctx.Err.Printf("youtube disconnect: %s", err)
		http.Error(w, "disconnect failed", http.StatusInternalServerError)
		return
	}
	_ = tokens.Set("youtube", nil)
	http.Redirect(w, r, recordingsAdminPath(conf.Tag, "?flash=YouTube+disconnected"), http.StatusSeeOther)
}

// ---- helpers ---------------------------------------------------------

func mintState(confTag string) string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return url.PathEscape(confTag) + ":" + base64.RawURLEncoding.EncodeToString(b[:])
}

func confTagFromOAuthState(state string) string {
	confTag, _, ok := strings.Cut(state, ":")
	if !ok {
		return ""
	}
	out, err := url.PathUnescape(confTag)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func redirectWithErr(w http.ResponseWriter, r *http.Request, confTag, recordingID, msg string) {
	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/recordings/%s?err=%s", url.PathEscape(confTag), url.PathEscape(recordingID), url.QueryEscape(msg)),
		http.StatusSeeOther)
}

func redirectRecordingsList(w http.ResponseWriter, r *http.Request, confTag, msg string) {
	http.Redirect(w, r, recordingsAdminPath(confTag, "?flash="+url.QueryEscape(msg)), http.StatusSeeOther)
}

func requireRecordingsConfAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) (*types.Conf, bool) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return nil, false
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return nil, false
	}
	return conf, true
}

func scopedRecordingFromRequest(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) (*types.Conf, *types.Recording, *RecordingRow, bool) {
	conf, ok := requireRecordingsConfAdmin(w, r, ctx)
	if !ok {
		return nil, nil, nil, false
	}
	recordingID := mux.Vars(r)["id"]
	rec := getters.FetchRecordingByID(recordingID)
	if rec == nil {
		handle404(w, r, ctx)
		return nil, nil, nil, false
	}
	if _, err := getters.FetchSocialPostsCached(ctx); err != nil {
		ctx.Err.Printf("/%s/admin/recordings/%s socialposts: %s", conf.Tag, recordingID, err)
	}
	row := buildRecordingRow(rec)
	if !recordingRowBelongsToConf(row, conf.Tag) {
		handle404(w, r, ctx)
		return nil, nil, nil, false
	}
	return conf, rec, row, true
}

func recordingRowBelongsToConf(row *RecordingRow, confTag string) bool {
	return row != nil &&
		row.ConfTalk != nil &&
		row.ConfTalk.Conf != nil &&
		row.ConfTalk.Conf.Tag == confTag
}

func recordingsAdminPath(confTag, suffix string) string {
	if suffix != "" && !strings.HasPrefix(suffix, "/") && !strings.HasPrefix(suffix, "?") {
		suffix = "/" + suffix
	}
	return fmt.Sprintf("/%s/admin/recordings%s", url.PathEscape(confTag), suffix)
}

func recordingDetailPath(confTag, recordingID string) string {
	return fmt.Sprintf("/%s/admin/recordings/%s", url.PathEscape(confTag), url.PathEscape(recordingID))
}

func recordingsOAuthRedirectURL(ctx *config.AppContext) string {
	return strings.TrimRight(ctx.Env.GetURI(), "/") + "/admin/recordings/oauth/youtube/callback"
}

func recordingPublishAtInput(publishAt *time.Time, conf *types.Conf) string {
	if publishAt == nil {
		return ""
	}
	loc := time.Local
	if conf != nil {
		loc = conf.Loc()
	}
	return publishAt.In(loc).Format("2006-01-02T15:04")
}

func parseRecordingPublishAt(raw string, conf *types.Conf) (time.Time, error) {
	loc := time.Local
	if conf != nil {
		loc = conf.Loc()
	}
	return time.ParseInLocation("2006-01-02T15:04", raw, loc)
}

func recordingPublishTimezone(conf *types.Conf) string {
	if conf == nil {
		return time.Local.String()
	}
	if conf.Timezone != "" {
		return conf.Timezone
	}
	return conf.Loc().String()
}

func recordingSocialPostRef(rec *types.Recording, platform string) string {
	if rec == nil {
		return ""
	}
	return fmt.Sprintf("recording:%s:%s", rec.ID, platform)
}

func upsertRecordingSocialPost(ctx *config.AppContext, row *RecordingRow, platform string, up getters.SocialPostUpdate) error {
	if row == nil || row.Recording == nil {
		return fmt.Errorf("recording row required")
	}
	up.Ref = recordingSocialPostRef(row.Recording, platform)
	up.PostedTo = platform
	up.Kind = getters.SocialPostKindRecording
	up.RecordingID = row.Recording.ID
	if row.ConfTalk != nil {
		up.ConfTalkID = row.ConfTalk.ID
	}
	if up.ScheduledAt == nil && row.Recording.PublishAt != nil {
		up.ScheduledAt = row.Recording.PublishAt
	}
	_, err := getters.UpsertSocialPost(ctx, up)
	if err == nil {
		attachRecordingSocialPosts(row)
		row.HasYT = row.YTURL != ""
		row.HasX = row.XURL != ""
	}
	return err
}

func buildRecordingRow(rec *types.Recording) *RecordingRow {
	row := &RecordingRow{
		Recording: rec,
		YTURL:     rec.YTLink,
		XURL:      rec.XLink,
		XReplyURL: rec.XReplyLink,
		HasFile:   rec.FileURI != "",
	}
	if rec.ConfTalkID != "" {
		row.ConfTalk = getters.FetchConfTalkByID(rec.ConfTalkID)
		if row.ConfTalk != nil && row.ConfTalk.Proposal != nil {
			row.Speakers = recordingSpeakersForProposal(row.ConfTalk.Proposal)
		}
	}
	attachRecordingSocialPosts(row)
	if !recordingStatusIsError(row.YTStatus) {
		row.YTError = ""
	}
	if !recordingStatusIsError(row.XStatus) {
		row.XError = ""
	}
	row.HasYT = row.YTURL != ""
	row.HasX = row.XURL != ""
	return row
}

func recordingStatusIsError(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case recordingStatusFailed, recordingStatusAuthRequired:
		return true
	default:
		return false
	}
}

func attachRecordingSocialPosts(row *RecordingRow) {
	if row == nil || row.Recording == nil {
		return
	}
	row.YTSocialPost = getters.FetchSocialPostByRef(recordingSocialPostRef(row.Recording, recordingPlatformYouTube))
	row.XSocialPost = getters.FetchSocialPostByRef(recordingSocialPostRef(row.Recording, recordingPlatformX))
	if row.YTSocialPost != nil {
		if row.YTSocialPost.URL != "" {
			row.YTURL = row.YTSocialPost.URL
		}
		if row.YTSocialPost.Status != "" {
			row.YTStatus = row.YTSocialPost.Status
		}
		if row.YTSocialPost.Error != "" {
			row.YTError = row.YTSocialPost.Error
		}
	}
	if row.XSocialPost != nil {
		if row.XSocialPost.URL != "" {
			row.XURL = row.XSocialPost.URL
		}
		if row.XSocialPost.ReplyURL != "" {
			row.XReplyURL = row.XSocialPost.ReplyURL
		}
		if row.XSocialPost.Status != "" {
			row.XStatus = row.XSocialPost.Status
		}
		if row.XSocialPost.Error != "" {
			row.XError = row.XSocialPost.Error
		}
		if row.XSocialPost.ErrorFingerprint != "" {
			row.XErrorFingerprint = row.XSocialPost.ErrorFingerprint
		}
	}
}

func recordingSpeakersForProposal(proposal *types.Proposal) []*types.Speaker {
	if proposal == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]*types.Speaker, 0, len(proposal.SpeakerConfRefs))
	appendSpeakerConf := func(sc *types.SpeakerConf) {
		if sc == nil || sc.Speaker == nil || seen[sc.Speaker.ID] {
			return
		}
		seen[sc.Speaker.ID] = true
		out = append(out, sc.Speaker)
	}

	// Cached proposals generally carry raw SpeakerConfRefs; the
	// Speakers slice is only filled by specific enrichers. Resolve
	// through the warm SpeakerConf cache so recordings get names even
	// when the proposal has not been enriched in this request.
	for _, sc := range resolveProposalSpeakers(proposal) {
		appendSpeakerConf(sc)
	}
	for _, sc := range proposal.Speakers {
		appendSpeakerConf(sc)
	}
	return out
}

// ---- copy generators -------------------------------------------------

// ytCopy is parsed once at package init, with the funcmap closure
// attached before parsing — Funcs() must be called pre-Parse for the
// names to resolve.
var ytCopy = template.Must(template.New("yt").Funcs(template.FuncMap{
	"joinSpeakers": joinSpeakerCredits,
}).Parse(`{{ .TalkName }}

{{- if .Speakers }}

By: {{ joinSpeakers .Speakers }}
{{- end }}
{{- if .Conf }}

Recorded {{ if .RecordedOn }}{{ .RecordedOn }} {{ end }}at {{ .Conf.Desc }}{{ if .Conf.Location }} — {{ .Conf.Location }}{{ end }}.
{{- end }}

{{- if .TalkDesc }}

{{ .TalkDesc }}
{{- end }}

{{- if .Conf }}

Find out more about this talk: https://btcpp.dev/{{ .Conf.Tag }}#talks
{{- end }}

Follow @btcplusplus for upcoming events: https://x.com/btcplusplus
Future bitcoin++ events: https://btcpp.dev
`))

var xMainCopy = template.Must(template.New("x-main").Parse(
	`POSTED 🎥: {{ .TalkName }}
{{- if .SpeakerCredits }}

Featuring: {{ .SpeakerCredits }}
{{- end }}
{{- if .Conf }}

from {{ .Conf.Desc }}{{ if .Conf.Location }} ({{ .Conf.Location }}){{ end }}
{{- end }}
`))

var xReplyCopy = template.Must(template.New("x-reply").Parse(
	`Watch ▶ {{ .YTLink }}
{{- if .TicketConf }}

Join us at {{ .TicketConf.Desc }}: https://btcpp.dev/{{ .TicketConf.Tag }}#tickets
{{- end }}`))

type ytCopyData struct {
	TalkName   string
	TalkDesc   string
	Speakers   []*types.Speaker
	Conf       *types.Conf
	RecordedOn string
}

type xCopyData struct {
	TalkName       string
	SpeakerCredits string
	Conf           *types.Conf
	TicketConf     *types.Conf
	YTLink         string
}

func defaultYouTubeCopy(ctx *config.AppContext, row *RecordingRow) (string, string) {
	if row == nil || row.Recording == nil {
		return "", ""
	}
	talkName := row.Recording.TalkName
	talkDesc := ""
	var conf *types.Conf
	var recordedOn string
	if row.ConfTalk != nil {
		conf = row.ConfTalk.Conf
		if row.ConfTalk.Proposal != nil {
			if row.ConfTalk.Proposal.Title != "" {
				talkName = row.ConfTalk.Proposal.Title
			}
			talkDesc = row.ConfTalk.Proposal.Description
		}
		if row.ConfTalk.Sched != nil && !row.ConfTalk.Sched.Start.IsZero() {
			recordedOn = row.ConfTalk.Sched.Start.Format("January 2, 2006")
		}
	}

	title := buildYTTitle(talkName, row.Speakers, conf)

	var buf bytes.Buffer
	if err := ytCopy.Execute(&buf, ytCopyData{
		TalkName:   talkName,
		TalkDesc:   talkDesc,
		Speakers:   row.Speakers,
		Conf:       conf,
		RecordedOn: recordedOn,
	}); err != nil {
		ctx.Err.Printf("yt copy gen: %s", err)
		return title, ""
	}
	return title, strings.TrimSpace(buf.String()) + "\n"
}

func defaultXMainCopy(ctx *config.AppContext, row *RecordingRow) string {
	if row == nil || row.Recording == nil {
		return ""
	}
	talkName := row.Recording.TalkName
	var conf *types.Conf
	if row.ConfTalk != nil {
		conf = row.ConfTalk.Conf
		if row.ConfTalk.Proposal != nil && row.ConfTalk.Proposal.Title != "" {
			talkName = row.ConfTalk.Proposal.Title
		}
	}
	var buf bytes.Buffer
	if err := xMainCopy.Execute(&buf, xCopyData{
		TalkName:       talkName,
		SpeakerCredits: joinSpeakerXCredits(row.Speakers),
		Conf:           conf,
	}); err != nil {
		ctx.Err.Printf("x copy gen: %s", err)
		return ""
	}
	return strings.TrimSpace(buf.String())
}

func recordingXMainCopy(ctx *config.AppContext, row *RecordingRow) string {
	if row != nil && row.XSocialPost != nil && strings.TrimSpace(row.XSocialPost.Text) != "" {
		return row.XSocialPost.Text
	}
	return defaultXMainCopy(ctx, row)
}

func defaultXReplyCopy(ctx *config.AppContext, row *RecordingRow) string {
	if row == nil || row.Recording == nil {
		return ""
	}
	var conf *types.Conf
	if row.ConfTalk != nil {
		conf = row.ConfTalk.Conf
	}
	yt := row.YTURL
	if yt == "" {
		yt = "<paste the YouTube link after you upload>"
	}
	var buf bytes.Buffer
	if err := xReplyCopy.Execute(&buf, xCopyData{
		Conf:       conf,
		TicketConf: nextTicketConf(ctx, conf),
		YTLink:     yt,
	}); err != nil {
		ctx.Err.Printf("x reply copy gen: %s", err)
		return ""
	}
	return strings.TrimSpace(buf.String())
}

func nextTicketConf(ctx *config.AppContext, current *types.Conf) *types.Conf {
	if ctx == nil {
		return nil
	}
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		return nil
	}
	return nextTicketConfFromList(confs, current, time.Now())
}

func nextTicketConfFromList(confs []*types.Conf, current *types.Conf, now time.Time) *types.Conf {
	var candidates []*types.Conf
	for _, conf := range confs {
		if conf == nil || !conf.Active || !conf.StartDate.After(now) || len(conf.Tickets) == 0 {
			continue
		}
		if current != nil && ((current.Ref != "" && conf.Ref == current.Ref) || (current.Tag != "" && conf.Tag == current.Tag)) {
			continue
		}
		candidates = append(candidates, conf)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].StartDate.Before(candidates[j].StartDate)
	})
	if len(candidates) == 0 {
		return nil
	}
	return candidates[0]
}

// buildYTTitle assembles "Talk Name — Speaker A, Speaker B | bitcoin++ Conf"
// clamped to YouTube's 100-char limit. Truncates right-to-left so we
// drop conf context before speaker context before talk name.
func buildYTTitle(talkName string, speakers []*types.Speaker, conf *types.Conf) string {
	var sb strings.Builder
	sb.WriteString(talkName)
	if names := joinSpeakerNames(speakers); names != "" {
		sb.WriteString(" — ")
		sb.WriteString(names)
	}
	if conf != nil && conf.Desc != "" {
		sb.WriteString(" | ")
		sb.WriteString(conf.Desc)
	}
	out := sb.String()
	if len(out) <= 100 {
		return out
	}
	return strings.TrimRight(out[:97], " ,-—|") + "..."
}

func joinSpeakerNames(speakers []*types.Speaker) string {
	var names []string
	for _, s := range speakers {
		if s == nil || s.Name == "" {
			continue
		}
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

func joinSpeakerCredits(speakers []*types.Speaker) string {
	var parts []string
	for _, s := range speakers {
		if s == nil || s.Name == "" {
			continue
		}
		if s.Twitter.Handle != "" {
			parts = append(parts, fmt.Sprintf("%s (@%s)", s.Name, s.Twitter.Handle))
		} else {
			parts = append(parts, s.Name)
		}
	}
	return strings.Join(parts, ", ")
}

func joinSpeakerXCredits(speakers []*types.Speaker) string {
	var parts []string
	for _, s := range speakers {
		if s == nil || s.Name == "" {
			continue
		}
		if s.Twitter.Handle != "" {
			parts = append(parts, fmt.Sprintf("%s (@%s)", s.Name, s.Twitter.Handle))
		} else {
			parts = append(parts, s.Name)
		}
	}
	return strings.Join(parts, ", ")
}
