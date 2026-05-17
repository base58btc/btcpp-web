package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		if youtubeReady && shouldUploadRecordingToYouTube(rec) {
			runScheduledYouTubeUpload(ctx, rec)
		}
		if shouldPostRecordingToX(rec, now) {
			if xClient != nil {
				runScheduledXPost(ctx, rec, xClient)
			} else if xInitErr != nil {
				recordXFailure(ctx, rec, recordingStatusFailed, "x uploader is not configured: "+xInitErr.Error())
			}
		}
	}
}

func shouldUploadRecordingToYouTube(rec *types.Recording) bool {
	if rec.YTLink != "" || rec.FileURI == "" {
		return false
	}
	return statusAllowsRetry(rec.YTStatus)
}

func shouldPostRecordingToX(rec *types.Recording, now time.Time) bool {
	if rec.XLink != "" || rec.FileURI == "" || rec.YTLink == "" || rec.PublishAt == nil {
		return false
	}
	if now.Before(rec.PublishAt.UTC()) {
		return false
	}
	return statusAllowsRetry(rec.XStatus)
}

func statusAllowsRetry(status string) bool {
	status = strings.TrimSpace(strings.ToLower(status))
	return status == "" || status == recordingStatusPending || status == "queued"
}

func runScheduledYouTubeUpload(ctx *config.AppContext, rec *types.Recording) {
	row := buildRecordingRow(rec)
	title, body := defaultYouTubeCopy(ctx, row)
	if title == "" {
		title = rec.TalkName
	}
	status := recordingStatusUploading
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{YTStatus: &status}); err != nil {
		ctx.Err.Printf("recording autopublish yt status recording=%s: %s", rec.ID, err)
		return
	}

	privacy := "public"
	var publishAt time.Time
	if rec.PublishAt != nil && rec.PublishAt.After(time.Now()) {
		privacy = "private"
		publishAt = rec.PublishAt.UTC()
	}
	src, size, err := spaces.GetStream(rec.FileURI)
	if err != nil {
		recordYouTubeFailure(ctx, rec, "couldn't fetch source video from Spaces: "+err.Error())
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
		recordYouTubeFailure(ctx, rec, err.Error())
		return
	}
	now := time.Now()
	status = recordingStatusUploaded
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{
		YTLink:       &ytURL,
		YTStatus:     &status,
		YTUploadedAt: &now,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist yt recording=%s: %s", rec.ID, err)
		return
	}
	ctx.Infos.Printf("recording autopublish yt uploaded recording=%s url=%s", rec.ID, ytURL)
}

func recordYouTubeFailure(ctx *config.AppContext, rec *types.Recording, msg string) {
	status := recordingStatusFailed
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{
		YTStatus: &status,
		YTError:  &msg,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist yt failure recording=%s: %s", rec.ID, err)
	}
	ctx.Err.Printf("recording autopublish yt failed recording=%s: %s", rec.ID, msg)
}

func runScheduledXPost(ctx *config.AppContext, rec *types.Recording, client *xposter.Client) {
	status := recordingStatusPosting
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{XStatus: &status}); err != nil {
		ctx.Err.Printf("recording autopublish x status recording=%s: %s", rec.ID, err)
		return
	}
	videoPath, cleanup, err := downloadRecordingVideo(rec.FileURI)
	if err != nil {
		recordXFailure(ctx, rec, recordingStatusFailed, "couldn't fetch source video from Spaces: "+err.Error())
		return
	}
	defer cleanup()

	row := buildRecordingRow(rec)
	result, err := client.Post(context.Background(), xposter.PostParams{
		Text:      defaultXMainCopy(ctx, row),
		ReplyText: defaultXReplyCopy(ctx, row),
		VideoPath: videoPath,
	})
	if err != nil {
		status := recordingStatusFailed
		if xposter.IsAuthError(err) {
			status = recordingStatusAuthRequired
		}
		recordXFailure(ctx, rec, status, err.Error())
		return
	}
	now := time.Now()
	status = recordingStatusPosted
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{
		XLink:      &result.PostURL,
		XReplyLink: &result.ReplyURL,
		XStatus:    &status,
		XPostedAt:  &now,
	}); err != nil {
		ctx.Err.Printf("recording autopublish persist x recording=%s: %s", rec.ID, err)
		return
	}
	ctx.Infos.Printf("recording autopublish x posted recording=%s url=%s", rec.ID, result.PostURL)
}

func downloadRecordingVideo(fileURI string) (string, func(), error) {
	src, _, err := spaces.GetStream(fileURI)
	if err != nil {
		return "", func() {}, err
	}
	defer src.Close()
	ext := filepath.Ext(fileURI)
	if ext == "" {
		ext = ".mp4"
	}
	f, err := os.CreateTemp("", "btcpp-recording-*"+ext)
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	cleanup := func() { _ = os.Remove(path) }
	_, copyErr := io.Copy(f, src)
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

func recordXFailure(ctx *config.AppContext, rec *types.Recording, status, msg string) {
	fp := xFailureFingerprint(status, msg)
	shouldNotify := rec.XErrorFingerprint != fp
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{
		XStatus:           &status,
		XError:            &msg,
		XErrorFingerprint: &fp,
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
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{XNotifiedAt: &now}); err != nil {
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
	return xposter.New(xposter.Config{
		ProfileObject: ctx.Env.Recordings.X.ProfileObject,
		EncryptionKey: ctx.Env.Recordings.EncryptionKey,
		Headed:        ctx.Env.Recordings.X.Headed,
		PostTimeout:   time.Duration(ctx.Env.Recordings.X.PostTimeoutSec) * time.Second,
		AuthWait:      time.Duration(ctx.Env.Recordings.X.AuthWaitSec) * time.Second,
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

func RecordingsAdminXBootstrap(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, ok := requireRecordingsConfAdmin(w, r, ctx)
	if !ok {
		return
	}
	if !ctx.Env.Recordings.X.Headed {
		redirectRecordingsListErr(w, r, conf.Tag, "X bootstrap must be run locally with X_BROWSER_HEADED=true")
		return
	}
	client, err := newXPosterClient(ctx)
	if err != nil {
		redirectRecordingsListErr(w, r, conf.Tag, "X uploader is not configured: "+err.Error())
		return
	}
	go func() {
		if err := client.Bootstrap(context.Background()); err != nil {
			ctx.Err.Printf("x bootstrap failed: %s", err)
			return
		}
		ctx.Infos.Printf("x bootstrap completed and profile archive saved")
	}()
	http.Redirect(w, r, recordingsAdminPath(conf.Tag, "?flash="+url.QueryEscape("X bootstrap started; complete login in the Chrome window, then run Test X auth")), http.StatusSeeOther)
}

func RecordingsAdminRetryX(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, rec, _, ok := scopedRecordingFromRequest(w, r, ctx)
	if !ok {
		return
	}
	status := recordingStatusPending
	if err := getters.UpdateRecordingPublishing(ctx, rec.ID, getters.RecordingPublishingUpdate{XStatus: &status}); err != nil {
		redirectWithErr(w, r, conf.Tag, rec.ID, "couldn't update Notion: "+err.Error())
		return
	}
	http.Redirect(w, r, recordingDetailPath(conf.Tag, rec.ID)+"?flash=X+post+queued+for+retry", http.StatusSeeOther)
}

func redirectRecordingsListErr(w http.ResponseWriter, r *http.Request, confTag, msg string) {
	http.Redirect(w, r, recordingsAdminPath(confTag, "?err="+url.QueryEscape(msg)), http.StatusSeeOther)
}
