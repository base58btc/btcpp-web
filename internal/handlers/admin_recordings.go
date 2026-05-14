// admin_recordings.go wires the /admin/recordings dashboard:
//   - list every Notion Recording row with per-row publish status
//   - per-recording detail page with editable YT + X copy
//   - YouTube OAuth bootstrap (start / callback / disconnect)
//   - YouTube upload kickoff + async status polling
//   - X manual-handoff: save the X post URL the admin pastes back
//
// X automation via chromedp is out-of-scope for this round (see the
// in-progress plan) — the detail page just opens an intent/post link
// pre-filled with the generated text; the admin uploads the video by
// hand and pastes the resulting URL back into a tiny form.
package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

const youtubeOAuthStateKey = "yt_oauth_state"

// ---- page data ---------------------------------------------------------

type RecordingRow struct {
	Recording *types.Recording
	ConfTalk  *types.ConfTalk
	Speakers  []*types.Speaker
	HasFile   bool
	HasYT     bool
	HasX      bool
}

type RecordingsAdminListPage struct {
	Rows           []*RecordingRow
	YouTubeReady   bool
	YouTubeAuthURL string
	FlashMessage   string
	FlashError     string
	Year           uint
}

type RecordingsAdminDetailPage struct {
	Row          *RecordingRow
	YTTitle      string
	YTBody       string
	XBody        string
	XIntentURL   string
	JobActive    bool
	JobStatus    string
	JobMessage   string
	YouTubeReady bool
	FlashMessage string
	FlashError   string
	Year         uint
}

// ---- job tracker -------------------------------------------------------

// uploadJob captures the state of an in-flight YouTube upload, keyed
// by Recording ID. Only one job per recording at a time.
type uploadJob struct {
	Status    string // "running" | "succeeded" | "failed"
	Message   string
	StartedAt time.Time
	EndedAt   time.Time
}

var (
	jobsMu sync.Mutex
	jobs   = map[string]*uploadJob{}
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

// ---- list page ---------------------------------------------------------

func RecordingsAdminList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	recs := getters.ListRecordingsCached()
	rows := make([]*RecordingRow, 0, len(recs))
	for _, rec := range recs {
		if rec == nil {
			continue
		}
		rows = append(rows, buildRecordingRow(rec))
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rowSortKey(rows[i]) > rowSortKey(rows[j])
	})

	page := &RecordingsAdminListPage{
		Rows:           rows,
		YouTubeReady:   youtubepkg.IsConfigured() && youtubepkg.IsConnected(),
		YouTubeAuthURL: "/admin/recordings/oauth/youtube/start",
		Year:           uint(time.Now().Year()),
	}
	if !youtubepkg.IsConfigured() {
		page.FlashError = "YouTube OAuth env vars (YOUTUBE_CLIENT_ID/SECRET/REDIRECT_URL) are not set — set them and restart to enable uploads."
	} else if !youtubepkg.IsConnected() {
		page.FlashError = "YouTube is configured but not connected. Click \"Authorize YouTube\" to grant upload access to the btcplusplus channel."
	}
	if flash := r.URL.Query().Get("flash"); flash != "" {
		page.FlashMessage = flash
		page.FlashError = ""
	}

	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/recordings.tmpl", page); err != nil {
		ctx.Err.Printf("/admin/recordings render: %s", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
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
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	recordingID := mux.Vars(r)["id"]
	rec := getters.FetchRecordingByID(recordingID)
	if rec == nil {
		handle404(w, r, ctx)
		return
	}
	row := buildRecordingRow(rec)

	ytTitle, ytBody := defaultYouTubeCopy(ctx, row)
	xBody := defaultXCopy(ctx, row)
	intentURL := "https://x.com/intent/post?" + url.Values{"text": []string{xBody}}.Encode()

	page := &RecordingsAdminDetailPage{
		Row:          row,
		YTTitle:      ytTitle,
		YTBody:       ytBody,
		XBody:        xBody,
		XIntentURL:   intentURL,
		YouTubeReady: youtubepkg.IsConfigured() && youtubepkg.IsConnected(),
		Year:         uint(time.Now().Year()),
	}
	if job := getJob(recordingID); job != nil {
		page.JobActive = job.Status == "running"
		page.JobStatus = job.Status
		page.JobMessage = job.Message
	}
	if flash := r.URL.Query().Get("flash"); flash != "" {
		page.FlashMessage = flash
	}
	if flashErr := r.URL.Query().Get("err"); flashErr != "" {
		page.FlashError = flashErr
	}

	if err := ctx.TemplateCache.ExecuteTemplate(w, "admin/recording_detail.tmpl", page); err != nil {
		ctx.Err.Printf("/admin/recordings/%s render: %s", recordingID, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// ---- upload to YouTube ------------------------------------------------

func RecordingsAdminUploadYT(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	recordingID := mux.Vars(r)["id"]
	rec := getters.FetchRecordingByID(recordingID)
	if rec == nil {
		handle404(w, r, ctx)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, recordingID, "couldn't parse form: "+err.Error())
		return
	}
	title := strings.TrimSpace(r.FormValue("yt_title"))
	body := r.FormValue("yt_body")
	privacy := strings.TrimSpace(r.FormValue("privacy"))
	if privacy == "" {
		privacy = "public"
	}
	if title == "" {
		redirectWithErr(w, r, recordingID, "YouTube title is required")
		return
	}
	if rec.FileURI == "" {
		redirectWithErr(w, r, recordingID, "Recording row has no FileURI — set the Spaces key in Notion first")
		return
	}
	if !youtubepkg.IsConfigured() {
		redirectWithErr(w, r, recordingID, "YouTube OAuth is not configured")
		return
	}
	if !youtubepkg.IsConnected() {
		redirectWithErr(w, r, recordingID, "YouTube is not connected — click Authorize on the recordings page")
		return
	}
	if !claimJob(recordingID) {
		redirectWithErr(w, r, recordingID, "An upload is already in progress for this recording")
		return
	}
	go runYouTubeUpload(ctx, recordingID, rec.FileURI, title, body, privacy)

	http.Redirect(w, r, fmt.Sprintf("/admin/recordings/%s?flash=Upload+started", recordingID), http.StatusSeeOther)
}

// runYouTubeUpload streams the source video from Spaces straight into
// YouTube's resumable-upload endpoint, then writes the resulting URL
// back to the Notion Recording row. Uses a fresh context.Background()
// because the HTTP request that kicked us off has already returned.
func runYouTubeUpload(ctx *config.AppContext, recordingID, fileURI, title, body, privacy string) {
	defer func() {
		if rec := recover(); rec != nil {
			ctx.Err.Printf("youtube upload panic recording=%s: %v", recordingID, rec)
			setJobStatus(recordingID, "failed", fmt.Sprintf("internal error: %v", rec))
		}
	}()
	src, _, err := spaces.GetStream(fileURI)
	if err != nil {
		ctx.Err.Printf("youtube upload: fetch %s: %s", fileURI, err)
		setJobStatus(recordingID, "failed", "couldn't fetch source video from Spaces: "+err.Error())
		return
	}
	defer src.Close()

	bg := context.Background()
	ytURL, err := youtubepkg.Upload(bg, youtubepkg.UploadParams{
		Title:         title,
		Description:   body,
		PrivacyStatus: privacy,
	}, src, -1)
	if err != nil {
		ctx.Err.Printf("youtube upload: %s", err)
		setJobStatus(recordingID, "failed", err.Error())
		return
	}
	if err := getters.UpdateRecordingYTLink(ctx, recordingID, ytURL); err != nil {
		ctx.Err.Printf("youtube upload: persist YTLink: %s", err)
		setJobStatus(recordingID, "failed", "uploaded to YouTube but failed to update Notion: "+err.Error())
		return
	}
	setJobStatus(recordingID, "succeeded", ytURL)
}

// ---- save X link (manual handoff) ------------------------------------

func RecordingsAdminSaveXLink(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	recordingID := mux.Vars(r)["id"]
	rec := getters.FetchRecordingByID(recordingID)
	if rec == nil {
		handle404(w, r, ctx)
		return
	}
	if err := r.ParseForm(); err != nil {
		redirectWithErr(w, r, recordingID, "couldn't parse form: "+err.Error())
		return
	}
	xURL := strings.TrimSpace(r.FormValue("x_url"))
	if xURL == "" {
		redirectWithErr(w, r, recordingID, "Paste the X URL before saving")
		return
	}
	if !strings.HasPrefix(xURL, "https://x.com/") && !strings.HasPrefix(xURL, "https://twitter.com/") {
		redirectWithErr(w, r, recordingID, "That doesn't look like an X.com URL")
		return
	}
	if err := getters.UpdateRecordingXLink(ctx, recordingID, xURL); err != nil {
		ctx.Err.Printf("save xlink recording=%s: %s", recordingID, err)
		redirectWithErr(w, r, recordingID, "couldn't update Notion: "+err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/recordings/%s?flash=X+link+saved", recordingID), http.StatusSeeOther)
}

// ---- job status polling ----------------------------------------------

func RecordingsAdminJobStatus(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	recordingID := mux.Vars(r)["id"]
	job := getJob(recordingID)
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

// ---- YouTube OAuth bootstrap -----------------------------------------

func RecordingsYTOAuthStart(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	if !youtubepkg.IsConfigured() {
		http.Error(w, "YouTube OAuth env vars are not set", http.StatusServiceUnavailable)
		return
	}
	state := mintState()
	ctx.Session.Put(r.Context(), youtubeOAuthStateKey, state)
	http.Redirect(w, r, youtubepkg.AuthCodeURL(state), http.StatusSeeOther)
}

func RecordingsYTOAuthCallback(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	wantState, _ := ctx.Session.Pop(r.Context(), youtubeOAuthStateKey).(string)
	gotState := r.URL.Query().Get("state")
	if wantState == "" || gotState == "" || wantState != gotState {
		http.Error(w, "OAuth state mismatch — try again from the recordings page", http.StatusBadRequest)
		return
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
	if err := youtubepkg.Exchange(r.Context(), code); err != nil {
		ctx.Err.Printf("youtube oauth exchange: %s", err)
		http.Error(w, "OAuth exchange failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/recordings?flash=YouTube+connected", http.StatusSeeOther)
}

func RecordingsYTOAuthDisconnect(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}
	if err := youtubepkg.Disconnect(); err != nil {
		ctx.Err.Printf("youtube disconnect: %s", err)
		http.Error(w, "disconnect failed", http.StatusInternalServerError)
		return
	}
	_ = tokens.Set("youtube", nil)
	http.Redirect(w, r, "/admin/recordings?flash=YouTube+disconnected", http.StatusSeeOther)
}

// ---- helpers ---------------------------------------------------------

func mintState() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func redirectWithErr(w http.ResponseWriter, r *http.Request, recordingID, msg string) {
	http.Redirect(w, r,
		fmt.Sprintf("/admin/recordings/%s?err=%s", recordingID, url.QueryEscape(msg)),
		http.StatusSeeOther)
}

func buildRecordingRow(rec *types.Recording) *RecordingRow {
	row := &RecordingRow{
		Recording: rec,
		HasFile:   rec.FileURI != "",
		HasYT:     rec.YTLink != "",
		HasX:      rec.XLink != "",
	}
	if rec.ConfTalkID != "" {
		row.ConfTalk = getters.FetchConfTalkByID(rec.ConfTalkID)
		if row.ConfTalk != nil && row.ConfTalk.Proposal != nil {
			for _, sc := range row.ConfTalk.Proposal.Speakers {
				if sc == nil || sc.Speaker == nil {
					continue
				}
				row.Speakers = append(row.Speakers, sc.Speaker)
			}
		}
	}
	return row
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

var xCopy = template.Must(template.New("x").Parse(
	`🎥 New video: "{{ .TalkName }}"
{{- if .SpeakerHandles }}

with {{ .SpeakerHandles }}
{{- end }}
{{- if .Conf }}

from {{ .Conf.Desc }}{{ if .Conf.Location }} ({{ .Conf.Location }}){{ end }}
{{- end }}

Watch ▶ {{ .YTLink }}
{{- if .Conf }}
More: https://btcpp.dev/{{ .Conf.Tag }}#talks
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
	SpeakerHandles string
	Conf           *types.Conf
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

func defaultXCopy(ctx *config.AppContext, row *RecordingRow) string {
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
	yt := row.Recording.YTLink
	if yt == "" {
		yt = "<paste the YouTube link after you upload>"
	}
	var buf bytes.Buffer
	if err := xCopy.Execute(&buf, xCopyData{
		TalkName:       talkName,
		SpeakerHandles: joinSpeakerHandles(row.Speakers),
		Conf:           conf,
		YTLink:         yt,
	}); err != nil {
		ctx.Err.Printf("x copy gen: %s", err)
		return ""
	}
	return strings.TrimSpace(buf.String())
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

func joinSpeakerHandles(speakers []*types.Speaker) string {
	var parts []string
	for _, s := range speakers {
		if s == nil {
			continue
		}
		if s.Twitter.Handle != "" {
			parts = append(parts, "@"+s.Twitter.Handle)
		} else if s.Name != "" {
			parts = append(parts, s.Name)
		}
	}
	return strings.Join(parts, " ")
}
