// Package youtube wraps the OAuth dance + resumable video upload
// against YouTube Data API v3. One-time bootstrap: an admin visits the
// AuthCodeURL, grants the youtube.upload scope, and the callback hands
// back a refresh token which we persist via external/tokens. After that
// every upload reuses the refresh token to mint short-lived access
// tokens automatically.
//
// Upload needs youtube.upload; admin maintenance commands can also use
// the persisted token to list the authenticated channel's uploaded videos.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"btcpp-web/external/tokens"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	youtubeapi "google.golang.org/api/youtube/v3"
)

const (
	tokenKey = "youtube"
)

const maxThumbnailBytes = 2 * 1024 * 1024

var (
	cfg   *oauth2.Config
	cfgMu sync.RWMutex
)

// Init wires the OAuth client config. Safe to call multiple times —
// the last call wins. Call at startup once env vars are loaded; the
// rest of the package is no-op until Init runs.
func Init(clientID, clientSecret, redirectURL string) {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if clientID == "" || clientSecret == "" {
		cfg = nil
		return
	}
	cfg = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes: []string{
			youtubeapi.YoutubeUploadScope,
			youtubeapi.YoutubeReadonlyScope,
			youtubeapi.YoutubeForceSslScope,
		},
		Endpoint: google.Endpoint,
	}
}

// IsConfigured reports whether the OAuth client config has been wired.
// False means the YOUTUBE_CLIENT_ID/SECRET/REDIRECT env vars are
// missing — callers should hide the upload UI in that case.
func IsConfigured() bool {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg != nil
}

// IsConnected reports whether a refresh token is persisted (i.e. the
// admin has completed the OAuth bootstrap at least once).
func IsConnected() bool {
	return tokens.Has(tokenKey)
}

// AuthCodeURL builds the Google consent URL the admin clicks to grant
// access. `state` is an opaque CSRF token the caller mints (we just
// verify it on the callback).
//
// access_type=offline + prompt=consent are critical: without them we
// get an access token but no refresh token, and the next upload after
// the access token expires will fail. The prompt=consent re-issues
// the refresh token on every fresh consent so a re-bootstrap after a
// revoke still works.
func AuthCodeURL(state string) string {
	c := configForRedirect("")
	if c == nil {
		return ""
	}
	return c.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

// AuthCodeURLForRedirect builds a consent URL with a request-specific
// redirect URI. This lets event-scoped admin pages keep OAuth callbacks
// under /{conf}/admin/recordings without needing one static redirect in
// config.toml.
func AuthCodeURLForRedirect(state, redirectURL string) string {
	c := configForRedirect(redirectURL)
	if c == nil {
		return ""
	}
	return c.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
	)
}

// Exchange swaps the OAuth code for a token and persists it. Called
// from the callback handler after CSRF state verification.
func Exchange(ctx context.Context, code string) error {
	return exchange(ctx, code, "")
}

// ExchangeForRedirect swaps a code using the same dynamic redirect URI
// that AuthCodeURLForRedirect sent to Google.
func ExchangeForRedirect(ctx context.Context, code, redirectURL string) error {
	return exchange(ctx, code, redirectURL)
}

func exchange(ctx context.Context, code, redirectURL string) error {
	c := configForRedirect(redirectURL)
	if c == nil {
		return fmt.Errorf("youtube: oauth not configured")
	}
	tok, err := c.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("youtube: exchange: %w", err)
	}
	if tok.RefreshToken == "" {
		// Google only issues a refresh token on the first consent (or
		// when prompt=consent forces re-consent). If we end up here,
		// the user has previously consented and Google withheld the
		// refresh token — surface a clear error so the admin can
		// revoke + retry.
		return fmt.Errorf("youtube: exchange returned no refresh token — revoke at https://myaccount.google.com/permissions and retry")
	}
	return tokens.Set(tokenKey, &tokens.Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
	})
}

func configForRedirect(redirectURL string) *oauth2.Config {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	if cfg == nil {
		return nil
	}
	c := *cfg
	if redirectURL != "" {
		c.RedirectURL = redirectURL
	}
	return &c
}

// Disconnect clears the stored token (UI "disconnect" button).
func Disconnect() error {
	return tokens.Set(tokenKey, nil)
}

// httpClient returns an oauth2-wrapped HTTP client that auto-refreshes
// the access token via the stored refresh token, and persists the
// refreshed token back to the bolt store so subsequent process
// restarts don't burn the refresh token unnecessarily.
func httpClient(ctx context.Context) (*http.Client, error) {
	cfgMu.RLock()
	c := cfg
	cfgMu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("youtube: oauth not configured")
	}
	stored, err := tokens.Get(tokenKey)
	if err != nil {
		return nil, fmt.Errorf("youtube: load token: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("youtube: not connected — visit the recordings admin page and authorize YouTube")
	}
	tok := &oauth2.Token{
		AccessToken:  stored.AccessToken,
		RefreshToken: stored.RefreshToken,
		TokenType:    stored.TokenType,
		Expiry:       stored.Expiry,
	}
	// Wrap the TokenSource so a refresh writes back to bolt.
	src := persistingSource{inner: c.TokenSource(ctx, tok), prev: tok}
	return oauth2.NewClient(ctx, &src), nil
}

// persistingSource is an oauth2.TokenSource that writes refreshed
// tokens back to the bolt store. Google's TokenSource transparently
// rotates the access token; without this wrapper the new access token
// would live only in memory and be re-fetched after every restart.
type persistingSource struct {
	inner oauth2.TokenSource
	prev  *oauth2.Token
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.inner.Token()
	if err != nil {
		return nil, err
	}
	if tok.AccessToken != p.prev.AccessToken || tok.RefreshToken != p.prev.RefreshToken {
		rt := tok.RefreshToken
		if rt == "" {
			// Google omits the refresh token in subsequent refresh
			// responses; preserve the original.
			rt = p.prev.RefreshToken
		}
		_ = tokens.Set(tokenKey, &tokens.Token{
			AccessToken:  tok.AccessToken,
			RefreshToken: rt,
			TokenType:    tok.TokenType,
			Expiry:       tok.Expiry,
		})
		// In-place update so the next compare sees the new value.
		p.prev = tok
	}
	return tok, nil
}

// UploadParams captures the metadata we set on a longform upload. We
// intentionally don't expose tags / category — the description carries
// the discovery context (speaker, conf, links) and YouTube's algorithm
// indexes title+description more reliably than tags anyway.
type UploadParams struct {
	Title       string
	Description string
	// PrivacyStatus is "private", "unlisted", or "public". Defaults to
	// "public" when empty.
	PrivacyStatus string
	// PublishAt, when non-zero, schedules the video to go public at the
	// given UTC time. Requires PrivacyStatus to be "private".
	PublishAt time.Time
}

type VideoStatus struct {
	ID            string
	PrivacyStatus string
	UploadStatus  string
	PublishAt     *time.Time
}

func GetVideoStatus(ctx context.Context, videoID string) (*VideoStatus, error) {
	if videoID == "" {
		return nil, fmt.Errorf("youtube video status: videoID is required")
	}
	client, err := httpClient(ctx)
	if err != nil {
		return nil, err
	}
	svc, err := youtubeapi.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("youtube: new service: %w", err)
	}
	resp, err := svc.Videos.List([]string{"status"}).Id(videoID).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("youtube videos.list: %w", err)
	}
	if resp == nil || len(resp.Items) == 0 || resp.Items[0].Status == nil {
		return nil, fmt.Errorf("youtube video %s not found", videoID)
	}
	st := resp.Items[0].Status
	out := &VideoStatus{
		ID:            videoID,
		PrivacyStatus: st.PrivacyStatus,
		UploadStatus:  st.UploadStatus,
	}
	if strings.TrimSpace(st.PublishAt) != "" {
		if t, err := time.Parse(time.RFC3339, st.PublishAt); err == nil {
			out.PublishAt = &t
		}
	}
	return out, nil
}

func ScheduleExistingVideo(ctx context.Context, videoID string, publishAt time.Time) error {
	if videoID == "" {
		return fmt.Errorf("youtube schedule: videoID is required")
	}
	if publishAt.IsZero() {
		return fmt.Errorf("youtube schedule: publishAt is required")
	}
	client, err := httpClient(ctx)
	if err != nil {
		return err
	}
	svc, err := youtubeapi.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("youtube: new service: %w", err)
	}
	video := &youtubeapi.Video{
		Id: videoID,
		Status: &youtubeapi.VideoStatus{
			PrivacyStatus: "private",
			PublishAt:     publishAt.UTC().Format(time.RFC3339),
		},
	}
	if _, err := svc.Videos.Update([]string{"status"}, video).Context(ctx).Do(); err != nil {
		return fmt.Errorf("youtube videos.update schedule: %w", err)
	}
	return nil
}

func ClearExistingVideoSchedule(ctx context.Context, videoID string) error {
	if videoID == "" {
		return fmt.Errorf("youtube clear schedule: videoID is required")
	}
	client, err := httpClient(ctx)
	if err != nil {
		return err
	}
	svc, err := youtubeapi.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("youtube: new service: %w", err)
	}
	video := &youtubeapi.Video{
		Id: videoID,
		Status: &youtubeapi.VideoStatus{
			PrivacyStatus: "unlisted",
		},
	}
	if _, err := svc.Videos.Update([]string{"status"}, video).Context(ctx).Do(); err != nil {
		return fmt.Errorf("youtube videos.update clear schedule: %w", err)
	}
	return nil
}

// Upload streams the source video bytes into YouTube via a resumable
// videos.insert call and returns the canonical https://youtu.be/<id>
// URL on success. The Reader is consumed once; size is optional but
// helps the SDK report progress.
func Upload(ctx context.Context, p UploadParams, src io.Reader, size int64) (string, error) {
	if p.Title == "" {
		return "", fmt.Errorf("youtube upload: Title is required")
	}
	if p.PrivacyStatus == "" {
		p.PrivacyStatus = "public"
	}
	client, err := httpClient(ctx)
	if err != nil {
		return "", err
	}
	svc, err := youtubeapi.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("youtube: new service: %w", err)
	}
	video := &youtubeapi.Video{
		Snippet: &youtubeapi.VideoSnippet{
			Title:       p.Title,
			Description: p.Description,
		},
		Status: &youtubeapi.VideoStatus{
			PrivacyStatus: p.PrivacyStatus,
		},
	}
	if !p.PublishAt.IsZero() {
		video.Status.PublishAt = p.PublishAt.UTC().Format(time.RFC3339)
	}
	// Media() configures a resumable upload internally; the SDK's
	// default chunk size (8 MiB) is fine for a few-GB longform talk.
	// `size` is currently unused; leaving the parameter so callers can
	// pass it for future progress reporting without a signature change.
	_ = size
	call := svc.Videos.Insert([]string{"snippet", "status"}, video).Media(src)
	resp, err := call.Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("youtube: videos.insert: %w", err)
	}
	if resp == nil || resp.Id == "" {
		raw, _ := json.Marshal(resp)
		return "", fmt.Errorf("youtube: videos.insert returned no id: %s", string(raw))
	}
	return fmt.Sprintf("https://youtu.be/%s", resp.Id), nil
}

// SetThumbnail uploads a custom thumbnail for an existing video.
func SetThumbnail(ctx context.Context, videoID, filename string, src io.Reader) error {
	if videoID == "" {
		return fmt.Errorf("youtube thumbnail: videoID is required")
	}
	client, err := httpClient(ctx)
	if err != nil {
		return err
	}
	svc, err := youtubeapi.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("youtube: new service: %w", err)
	}
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" {
		contentType = "image/png"
	}
	_, err = svc.Thumbnails.Set(videoID).Media(src, googleapi.ContentType(contentType)).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("youtube thumbnails.set: %w", err)
	}
	return nil
}

// SetThumbnailBytes uploads a custom thumbnail, transcoding oversized PNGs
// to JPEG so they fit YouTube's 2 MiB thumbnail limit.
func SetThumbnailBytes(ctx context.Context, videoID, filename string, data []byte) error {
	prepared, contentType, err := PrepareThumbnail(filename, data)
	if err != nil {
		return err
	}
	if videoID == "" {
		return fmt.Errorf("youtube thumbnail: videoID is required")
	}
	client, err := httpClient(ctx)
	if err != nil {
		return err
	}
	svc, err := youtubeapi.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("youtube: new service: %w", err)
	}
	_, err = svc.Thumbnails.Set(videoID).Media(bytes.NewReader(prepared), googleapi.ContentType(contentType)).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("youtube thumbnails.set: %w", err)
	}
	return nil
}

func PrepareThumbnail(filename string, data []byte) ([]byte, string, error) {
	contentType := mime.TypeByExtension(filepath.Ext(filename))
	if contentType == "" {
		contentType = "image/png"
	}
	if len(data) <= maxThumbnailBytes {
		return data, contentType, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("decode thumbnail %s: %w", filename, err)
	}
	for _, quality := range []int{92, 88, 84, 80, 76, 72, 68, 64, 60, 56, 52, 48, 44, 40} {
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, "", fmt.Errorf("encode thumbnail jpeg: %w", err)
		}
		if buf.Len() <= maxThumbnailBytes {
			return buf.Bytes(), "image/jpeg", nil
		}
	}
	return nil, "", fmt.Errorf("thumbnail %s remains larger than 2 MiB after JPEG compression", filename)
}
