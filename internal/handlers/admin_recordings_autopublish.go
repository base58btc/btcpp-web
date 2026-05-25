package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/external/xposter"
	youtubepkg "btcpp-web/external/youtube"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/types"
)

const (
	recordingStatusPending      = "pending"
	recordingStatusUploading    = "uploading"
	recordingStatusUploaded     = "uploaded"
	recordingStatusScheduling   = "scheduling"
	recordingStatusScheduled    = "scheduled"
	recordingStatusPosting      = "posting"
	recordingStatusPosted       = "posted"
	recordingStatusFailed       = "failed"
	recordingStatusAuthRequired = "auth_required"
)

func StartRecordingAutopublisher(ctx *config.AppContext) {
	if ctx == nil || ctx.Env == nil || !ctx.Env.Recordings.AutopublishEnabled {
		return
	}
	go func() {
		wait := time.Duration(ctx.Env.Recordings.PollSec) * time.Second
		if wait <= 0 {
			wait = time.Minute
		}
		ctx.Infos.Printf("recording autopublisher enabled; polling every %s", wait)
		time.Sleep(5 * time.Second)
		for {
			runRecordingAutopublishTick(ctx)
			time.Sleep(wait)
		}
	}()
}

func runRecordingAutopublishTick(ctx *config.AppContext) {
	now := time.Now()
	recs := getters.ListRecordingsCached()
	if len(recs) == 0 {
		return
	}
	if _, err := getters.FetchSocialPostsCached(ctx); err != nil {
		ctx.Err.Printf("recording autopublisher socialposts: %s", err)
	}
	youtubeReady := youtubepkg.IsConfigured() && youtubepkg.IsConnected()
	var xClient *xposter.Client
	var xInitErr error
	if ctx.Env.Recordings.X.Enabled {
		c, err := newXPosterClient(ctx)
		if err != nil {
			xInitErr = err
			ctx.Err.Printf("x uploader disabled this tick: %s", err)
		} else {
			xClient = c
		}
	}
	for _, rec := range recs {
		if rec == nil || rec.PublishAt == nil || rec.FileURI == "" {
			continue
		}
		row := buildRecordingRow(rec)
		if youtubeReady && shouldUploadRecordingToYouTube(row) {
			runScheduledYouTubeUpload(ctx, row)
		}
		if shouldPostRecordingToX(row, now) {
			if xClient != nil {
				runScheduledXPost(ctx, row, xClient)
			} else if xInitErr != nil {
				recordXFailure(ctx, row, recordingStatusFailed, "x uploader is not configured: "+xInitErr.Error())
			}
		}
	}
}

func shouldUploadRecordingToYouTube(row *RecordingRow) bool {
	if row == nil || row.Recording == nil {
		return false
	}
	if row.YTURL != "" || row.Recording.FileURI == "" {
		return false
	}
	return statusAllowsRetry(row.YTStatus)
}

func shouldPostRecordingToX(row *RecordingRow, now time.Time) bool {
	if row == nil || row.Recording == nil {
		return false
	}
	if row.XURL != "" || row.Recording.FileURI == "" || row.YTURL == "" || row.Recording.PublishAt == nil {
		return false
	}
	if now.Before(row.Recording.PublishAt.UTC()) {
		return false
	}
	return statusAllowsRetry(row.XStatus)
}

func statusAllowsRetry(status string) bool {
	status = strings.TrimSpace(strings.ToLower(status))
	return status == "" || status == recordingStatusPending || status == "queued"
}

func runScheduledYouTubeUpload(ctx *config.AppContext, row *RecordingRow) {
	rec := row.Recording
	title, body := defaultYouTubeCopy(ctx, row)
	if title == "" {
		title = rec.TalkName
	}
	status := recordingStatusUploading
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformYouTube, getters.SocialPostUpdate{
		Text:   &body,
		Status: &status,
	}); err != nil {
		ctx.Err.Printf("recording autopublish yt status recording=%s: %s", rec.ID, err)
	}

	privacy := "public"
	var publishAt time.Time
	if rec.PublishAt != nil && rec.PublishAt.After(time.Now()) {
		privacy = "private"
		publishAt = rec.PublishAt.UTC()
	}
	src, size, err := openRecordingSourceStream(rec.FileURI)
	if err != nil {
		recordYouTubeFailure(ctx, row, "couldn't fetch source video from Spaces: "+err.Error())
		return
	}
	defer src.Close()

	ytURL, err := youtubepkg.Upload(context.Background(), youtubepkg.UploadParams{
		Title:         title,
		Description:   body,
		PrivacyStatus: privacy,
		PublishAt:     publishAt,
	}, src, size)
	if err != nil {
		recordYouTubeFailure(ctx, row, err.Error())
		return
	}
	now := time.Now()
	status = recordingStatusUploaded
	if err := getters.UpdateRecordingYTLink(ctx, rec.ID, ytURL); err != nil {
		ctx.Err.Printf("recording autopublish persist yt recording=%s: %s", rec.ID, err)
		return
	}
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformYouTube, getters.SocialPostUpdate{
		URL:      &ytURL,
		Status:   &status,
		PostedAt: &now,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist yt socialpost recording=%s: %s", rec.ID, err)
		return
	}
	ctx.Infos.Printf("recording autopublish yt uploaded recording=%s url=%s", rec.ID, ytURL)
}

func recordYouTubeFailure(ctx *config.AppContext, row *RecordingRow, msg string) {
	rec := row.Recording
	status := recordingStatusFailed
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformYouTube, getters.SocialPostUpdate{Status: &status, Error: &msg}); err != nil {
		ctx.Err.Printf("recording autopublish persist yt failure recording=%s: %s", rec.ID, err)
	}
	ctx.Err.Printf("recording autopublish yt failed recording=%s: %s", rec.ID, msg)
}

func runScheduledXPost(ctx *config.AppContext, row *RecordingRow, client *xposter.Client) {
	if row != nil && row.Recording != nil {
		setXJobStage(row.Recording.ID, "prepare", "Starting scheduled X post")
	}
	mainText := recordingXMainCopy(ctx, row)
	runXPost(ctx, row, client, mainText, recordingXReplyCopyForPost(ctx, row))
}

func runXPostNow(ctx *config.AppContext, row *RecordingRow, client *xposter.Client, mainText string) {
	runXPost(ctx, row, client, mainText, recordingXReplyCopyForPost(ctx, row))
}

func runXPost(ctx *config.AppContext, row *RecordingRow, client *xposter.Client, mainText, replyText string) {
	rec := row.Recording
	status := recordingStatusPosting
	clear := ""
	setXJobStage(rec.ID, "prepare", "Preparing X post")
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		Text:             &mainText,
		Status:           &status,
		Error:            &clear,
		ErrorFingerprint: &clear,
	}); err != nil {
		ctx.Err.Printf("recording autopublish x status recording=%s: %s", rec.ID, err)
	}
	setXJobStage(rec.ID, "download", "Downloading source video from Spaces")
	videoPath, cleanup, err := downloadRecordingVideo(rec.ID, rec.FileURI)
	if err != nil {
		recordXFailure(ctx, row, recordingStatusFailed, "couldn't fetch source video from Spaces: "+err.Error())
		return
	}
	defer cleanup()

	setXJobStage(rec.ID, "upload", "Uploading video to X and waiting for processing")
	result, err := client.Post(context.Background(), xposter.PostParams{
		Text:      mainText,
		ReplyText: replyText,
		VideoPath: videoPath,
		Progress:  recordingXProgressReporter(rec.ID),
	})
	if err != nil {
		var replyErr *xposter.ReplyError
		if errors.As(err, &replyErr) && replyErr.PostURL != "" {
			recordXPartialReplyFailure(ctx, row, mainText, replyErr.PostURL, err.Error())
			return
		}
		status := recordingStatusFailed
		if xposter.IsAuthError(err) {
			status = recordingStatusAuthRequired
		}
		recordXFailure(ctx, row, status, err.Error())
		return
	}
	setXJobStage(rec.ID, "save", "Saving X post URL")
	now := time.Now()
	status = recordingStatusPosted
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{
		XLink:      &result.PostURL,
		XReplyLink: &result.ReplyURL,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist x recording=%s: %s", rec.ID, err)
		setXJobStatus(rec.ID, "failed", "posted to X but failed to update Notion: "+err.Error())
		return
	}
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		URL:              &result.PostURL,
		ReplyURL:         &result.ReplyURL,
		Status:           &status,
		Error:            &clear,
		ErrorFingerprint: &clear,
		PostedAt:         &now,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist x socialpost recording=%s: %s", rec.ID, err)
		setXJobStatus(rec.ID, "failed", "posted to X but failed to update SocialPosts: "+err.Error())
		return
	}
	setXJobProgress(rec.ID, "succeeded", result.PostURL, "done", 100)
	ctx.Infos.Printf("recording autopublish x posted recording=%s url=%s", rec.ID, result.PostURL)
}

func recordXPartialReplyFailure(ctx *config.AppContext, row *RecordingRow, mainText, postURL, msg string) {
	rec := row.Recording
	setXJobStatus(rec.ID, "failed", msg)
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{
		XLink: &postURL,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist partial x recording=%s: %s", rec.ID, err)
	}
	status := recordingStatusFailed
	fp := xFailureFingerprint(status, msg)
	now := time.Now()
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		Text:             &mainText,
		URL:              &postURL,
		Status:           &status,
		Error:            &msg,
		ErrorFingerprint: &fp,
		PostedAt:         &now,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist partial x socialpost recording=%s: %s", rec.ID, err)
	}
	ctx.Err.Printf("recording autopublish x reply failed recording=%s url=%s: %s", rec.ID, postURL, msg)
}

func recordingXReplyCopyForPost(ctx *config.AppContext, row *RecordingRow) string {
	if row == nil || row.YTURL == "" {
		return ""
	}
	return defaultXReplyCopy(ctx, row)
}

func recordingXProgressReporter(recordingID string) xposter.ProgressFunc {
	return func(stage string, progress int, message string) {
		setXJobProgress(recordingID, "running", message, stage, progress)
	}
}

func runXSchedule(ctx *config.AppContext, rec *types.Recording, conf *types.Conf, mainText string) {
	row := buildRecordingRow(rec)
	setXJobStage(rec.ID, "prepare", "Preparing X schedule")
	if rec.PublishAt == nil {
		recordXFailure(ctx, row, recordingStatusFailed, "PublishAt is required before scheduling on X")
		return
	}
	client, err := newXPosterClient(ctx)
	if err != nil {
		recordXFailure(ctx, row, recordingStatusFailed, "x uploader is not configured: "+err.Error())
		return
	}
	setXJobStage(rec.ID, "download", "Downloading source video from Spaces")
	videoPath, cleanup, err := downloadRecordingVideo(rec.ID, rec.FileURI)
	if err != nil {
		recordXFailure(ctx, row, recordingStatusFailed, "couldn't fetch source video from Spaces: "+err.Error())
		return
	}
	defer cleanup()

	setXJobStage(rec.ID, "upload", "Uploading video to X and creating scheduled post")
	timezone := ""
	scheduleAt := *rec.PublishAt
	if conf != nil {
		timezone = conf.Timezone
		scheduleAt = rec.PublishAt.In(conf.Loc())
	}
	if err := client.Schedule(context.Background(), xposter.ScheduleParams{
		Text:      mainText,
		VideoPath: videoPath,
		Schedule:  scheduleAt,
		Timezone:  timezone,
		Progress:  recordingXProgressReporter(rec.ID),
	}); err != nil {
		status := recordingStatusFailed
		if xposter.IsAuthError(err) {
			status = recordingStatusAuthRequired
		}
		recordXFailure(ctx, row, status, err.Error())
		return
	}

	setXJobStage(rec.ID, "save", "Saving X schedule status")
	status := recordingStatusScheduled
	clear := ""
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		Text:             &mainText,
		Status:           &status,
		Error:            &clear,
		ErrorFingerprint: &clear,
		ScheduledAt:      rec.PublishAt,
	}); err != nil {
		ctx.Err.Printf("recording schedule x persist socialpost recording=%s: %s", rec.ID, err)
		setXJobStatus(rec.ID, "failed", "scheduled on X but failed to update SocialPosts: "+err.Error())
		return
	}
	setXJobProgress(rec.ID, "succeeded", "Scheduled on X", "done", 100)
	ctx.Infos.Printf("recording x scheduled recording=%s publishAt=%s", rec.ID, rec.PublishAt.UTC().Format(time.RFC3339))
}

func downloadRecordingVideo(recordingID, fileURI string) (string, func(), error) {
	key := recordingSourceObjectKey(fileURI)
	src, size, err := openRecordingSourceStream(fileURI)
	if err != nil {
		return "", func() {}, err
	}
	defer src.Close()
	ext := filepath.Ext(key)
	if ext == "" {
		ext = ".mp4"
	}
	f, err := os.CreateTemp("", "btcpp-recording-*"+ext)
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	copyErr := copyRecordingSource(recordingID, f, src, size)
	closeErr := f.Close()
	if copyErr != nil {
		cleanup()
		return "", func() {}, copyErr
	}
	if closeErr != nil {
		cleanup()
		return "", func() {}, closeErr
	}
	return path, cleanup, nil
}

func copyRecordingSource(recordingID string, dst io.Writer, src io.Reader, size int64) error {
	if size <= 0 {
		setXJobProgress(recordingID, "running", "Downloading source video from Spaces", "download", 0)
		_, err := io.Copy(dst, src)
		return err
	}
	buf := make([]byte, 1024*1024)
	var copied int64
	lastProgress := -1
	lastUpdate := time.Time{}
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, err := dst.Write(buf[:n]); err != nil {
				return err
			}
			copied += int64(n)
			progress := int(copied * 100 / size)
			if progress > 100 {
				progress = 100
			}
			if progress != lastProgress && (progress%5 == 0 || time.Since(lastUpdate) >= time.Second || progress == 100) {
				setXJobProgress(recordingID, "running", fmt.Sprintf("Downloading source video from Spaces (%d%%)", progress), "download", progress)
				lastProgress = progress
				lastUpdate = time.Now()
			}
		}
		if readErr == io.EOF {
			setXJobProgress(recordingID, "running", "Downloaded source video from Spaces", "download", 100)
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func openRecordingSourceStream(fileURI string) (io.ReadCloser, int64, error) {
	key := recordingSourceObjectKey(fileURI)
	if key == "" {
		return nil, 0, fmt.Errorf("empty FileURI")
	}
	src, size, err := spaces.GetStream(key)
	if err != nil {
		return nil, 0, fmt.Errorf("key %q: %w", key, err)
	}
	return src, size, nil
}

func recordingSourceObjectKey(fileURI string) string {
	raw := strings.TrimSpace(fileURI)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" && u.Host != "" {
		path := strings.TrimPrefix(u.EscapedPath(), "/")
		if unescaped, err := url.PathUnescape(path); err == nil {
			path = unescaped
		}
		return path
	}
	return strings.TrimPrefix(raw, "/")
}

func recordXFailure(ctx *config.AppContext, row *RecordingRow, status, msg string) {
	rec := row.Recording
	setXJobStatus(rec.ID, "failed", msg)
	fp := xFailureFingerprint(status, msg)
	shouldNotify := row.XErrorFingerprint != fp
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		Status:           &status,
		Error:            &msg,
		ErrorFingerprint: &fp,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist x failure recording=%s: %s", rec.ID, err)
	}
	ctx.Err.Printf("recording autopublish x failed recording=%s status=%s: %s", rec.ID, status, msg)
	if !shouldNotify {
		return
	}
	if err := sendXFailureEmail(ctx, rec, status, msg, fp); err != nil {
		ctx.Err.Printf("recording autopublish x notify recording=%s: %s", rec.ID, err)
		return
	}
	now := time.Now()
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{NotifiedAt: &now}); err != nil {
		ctx.Err.Printf("recording autopublish x notify stamp recording=%s: %s", rec.ID, err)
	}
}

func sendXFailureEmail(ctx *config.AppContext, rec *types.Recording, status, msg, fp string) error {
	to := strings.TrimSpace(ctx.Env.Recordings.NotifyEmail)
	if to == "" {
		return nil
	}
	row := buildRecordingRow(rec)
	title := rec.TalkName
	if row.ConfTalk != nil && row.ConfTalk.Proposal != nil && row.ConfTalk.Proposal.Title != "" {
		title = row.ConfTalk.Proposal.Title
	}
	adminURL := strings.TrimRight(ctx.Env.GetURI(), "/")
	if row.ConfTalk != nil && row.ConfTalk.Conf != nil {
		adminURL += recordingDetailPath(row.ConfTalk.Conf.Tag, rec.ID)
	} else {
		adminURL += "/dashboard"
	}
	text := fmt.Sprintf(`X uploader issue for %s

Status: %s
Recording: %s
FileURI: %s
PublishAt: %s
Fingerprint: %s

%s

Admin: %s
`, title, status, rec.ID, rec.FileURI, formatMaybeTime(rec.PublishAt), fp, msg, adminURL)
	html := fmt.Sprintf(`<p>X uploader issue for <strong>%s</strong></p>
<p><strong>Status:</strong> %s<br>
<strong>Recording:</strong> %s<br>
<strong>FileURI:</strong> %s<br>
<strong>PublishAt:</strong> %s<br>
<strong>Fingerprint:</strong> %s</p>
<pre style="white-space:pre-wrap">%s</pre>
<p><a href="%s">Open recording admin</a></p>`,
		template.HTMLEscapeString(title),
		template.HTMLEscapeString(status),
		template.HTMLEscapeString(rec.ID),
		template.HTMLEscapeString(rec.FileURI),
		template.HTMLEscapeString(formatMaybeTime(rec.PublishAt)),
		template.HTMLEscapeString(fp),
		template.HTMLEscapeString(msg),
		template.HTMLEscapeString(adminURL),
	)
	return emails.ComposeAndSendMail(ctx, &emails.Mail{
		JobKey:   "x-uploader:" + rec.ID + ":" + fp,
		Email:    to,
		Title:    "X uploader issue: " + title,
		SendAt:   time.Now(),
		HTMLBody: []byte(html),
		TextBody: []byte(text),
	})
}

func xFailureFingerprint(status, msg string) string {
	sum := sha256.Sum256([]byte(status + "\x00" + strings.TrimSpace(msg)))
	return hex.EncodeToString(sum[:8])
}

func formatMaybeTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func newXPosterClient(ctx *config.AppContext) (*xposter.Client, error) {
	postTimeout := time.Duration(ctx.Env.Recordings.X.PostTimeoutSec) * time.Second
	authWait := time.Duration(ctx.Env.Recordings.X.AuthWaitSec) * time.Second
	if ctx.Infos != nil {
		ctx.Infos.Printf("x uploader config headed=%t postTimeout=%s authWait=%s profileObject=%q",
			ctx.Env.Recordings.X.Headed,
			postTimeout,
			authWait,
			ctx.Env.Recordings.X.ProfileObject,
		)
	}
	return xposter.New(xposter.Config{
		ProfileObject: ctx.Env.Recordings.X.ProfileObject,
		EncryptionKey: ctx.Env.Recordings.EncryptionKey,
		Headed:        ctx.Env.Recordings.X.Headed,
		LoginUsername: ctx.Env.Recordings.X.LoginUsername,
		LoginPassword: ctx.Env.Recordings.X.LoginPassword,
		PostTimeout:   postTimeout,
		AuthWait:      authWait,
		Logf:          ctx.Infos.Printf,
	})
}

func RecordingsAdminXAuthCheck(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, ok := requireRecordingsConfAdmin(w, r, ctx)
	if !ok {
		return
	}
	client, err := newXPosterClient(ctx)
	if err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, "X uploader is not configured: "+err.Error())
		return
	}
	status, err := client.AuthStatus(r.Context())
	if err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, "X auth check failed: "+err.Error())
		return
	}
	http.Redirect(w, r, recordingsAdminPath(conf.Tag, "?flash="+url.QueryEscape("X auth status: "+status)), http.StatusSeeOther)
}

func RecordingsAdminRetryX(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, row, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	status := recordingStatusPending
	clear := ""
	if err := upsertRecordingSocialPost(ctx, row, recordingPlatformX, getters.SocialPostUpdate{
		Status:           &status,
		Error:            &clear,
		ErrorFingerprint: &clear,
	}); err != nil {
		redirectWithErr(w, r, conf.Tag, rec.ID, "couldn't update Notion: "+err.Error())
		return
	}
	http.Redirect(w, r, recordingDetailPath(conf.Tag, rec.ID)+"?flash=X+post+queued+for+retry", http.StatusSeeOther)
}

func redirectRecordingsListErr(w http.ResponseWriter, r *http.Request, confTag, msg string) {
	http.Redirect(w, r, recordingsAdminPath(confTag, "?err="+url.QueryEscape(msg)), http.StatusSeeOther)
}
