// Package xposter drives x.com through Chrome for the recordings uploader.
package xposter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"btcpp-web/external/secureblob"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type Config struct {
	ProfileObject string
	EncryptionKey string
	Headed        bool
	LoginUsername string
	LoginPassword string
	PostTimeout   time.Duration
	AuthWait      time.Duration
	Logf          func(string, ...any)
}

type Client struct {
	cfg Config
	key []byte
}

var profileMu sync.Mutex

const xDesktopUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

type PostParams struct {
	Text      string
	ReplyText string
	VideoPath string
	Progress  ProgressFunc
}

type ScheduleParams struct {
	Text      string
	VideoPath string
	Schedule  time.Time
	Timezone  string
	Progress  ProgressFunc
}

type ProgressFunc func(stage string, progress int, message string)

type PostResult struct {
	PostURL  string
	ReplyURL string
}

type ReplyError struct {
	PostURL string
	Err     error
}

func (e *ReplyError) Error() string {
	if e == nil || e.Err == nil {
		return "x reply failed"
	}
	return "x reply failed after main post succeeded: " + e.Err.Error()
}

func (e *ReplyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type AuthError struct {
	Reason string
}

func (e *AuthError) Error() string {
	if e.Reason == "" {
		return "x auth required"
	}
	return "x auth required: " + e.Reason
}

func IsAuthError(err error) bool {
	var auth *AuthError
	return errors.As(err, &auth)
}

func New(cfg Config) (*Client, error) {
	key, err := secureblob.DecodeKey(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	if cfg.ProfileObject == "" {
		return nil, fmt.Errorf("missing x profile object key")
	}
	if cfg.PostTimeout == 0 {
		cfg.PostTimeout = 5 * time.Minute
	}
	if cfg.AuthWait == 0 {
		cfg.AuthWait = 5 * time.Minute
	}
	return &Client{cfg: cfg, key: key}, nil
}

func (c *Client) AuthStatus(ctx context.Context) (string, error) {
	var status string
	err := c.withProfile(ctx, true, true, func(profileDir string) error {
		return c.withBrowser(ctx, profileDir, true, func(bctx context.Context) error {
			s, err := detectLogin(bctx)
			if err != nil {
				return err
			}
			status = s
			if s == "ok" {
				return nil
			}
			if s != "login required" {
				return &AuthError{Reason: s}
			}
			if c.cfg.Logf != nil {
				c.cfg.Logf("x auth status requires login; attempting configured credential login")
			}
			if err := c.loginWithCredentials(bctx); err != nil {
				return err
			}
			status = "ok"
			return nil
		})
	})
	if err != nil {
		return "", err
	}
	return status, nil
}

func (c *Client) hasLoginCredentials() bool {
	return strings.TrimSpace(c.cfg.LoginUsername) != "" && c.cfg.LoginPassword != ""
}

func (c *Client) loginWithCredentials(ctx context.Context) error {
	if !c.hasLoginCredentials() {
		return &AuthError{Reason: "login required; missing X_LOGIN_USERNAME/X_LOGIN_PASSWORD"}
	}
	if c.cfg.Logf != nil {
		c.cfg.Logf("x login required; signing in with configured credentials")
	}
	if err := chromedp.Run(ctx, chromedp.Navigate("https://x.com/i/flow/login")); err != nil {
		return err
	}
	if err := submitXLoginText(ctx, strings.TrimSpace(c.cfg.LoginUsername)); err != nil {
		return err
	}
	if err := maybeSubmitXLoginIdentifier(ctx, strings.TrimSpace(c.cfg.LoginUsername)); err != nil {
		return err
	}
	if err := submitXLoginPassword(ctx, c.cfg.LoginPassword); err != nil {
		return err
	}
	return waitForLoggedIn(ctx)
}

func (c *Client) Post(ctx context.Context, p PostParams) (PostResult, error) {
	if p.Text == "" {
		return PostResult{}, fmt.Errorf("x post text is required")
	}
	if p.VideoPath == "" {
		return PostResult{}, fmt.Errorf("x video path is required")
	}
	if _, err := os.Stat(p.VideoPath); err != nil {
		return PostResult{}, fmt.Errorf("x video path: %w", err)
	}
	var result PostResult
	err := c.withProfile(ctx, true, true, func(profileDir string) error {
		return c.withBrowser(ctx, profileDir, false, func(bctx context.Context) error {
			if err := c.ensureAuthenticated(bctx); err != nil {
				return err
			}
			postURL, err := createPost(bctx, p.Text, p.VideoPath, p.Progress)
			if err != nil {
				return err
			}
			result.PostURL = postURL
			if p.ReplyText != "" {
				replyURL, err := createReply(bctx, postURL, p.ReplyText)
				if err != nil {
					return &ReplyError{PostURL: postURL, Err: err}
				}
				result.ReplyURL = replyURL
			}
			return nil
		})
	})
	return result, err
}

func (c *Client) Schedule(ctx context.Context, p ScheduleParams) error {
	if p.Text == "" {
		return fmt.Errorf("x post text is required")
	}
	if p.VideoPath == "" {
		return fmt.Errorf("x video path is required")
	}
	if p.Schedule.IsZero() {
		return fmt.Errorf("x schedule time is required")
	}
	if !p.Schedule.After(time.Now()) {
		return fmt.Errorf("x schedule time must be in the future")
	}
	if _, err := os.Stat(p.VideoPath); err != nil {
		return fmt.Errorf("x video path: %w", err)
	}
	return c.withProfile(ctx, true, true, func(profileDir string) error {
		return c.withBrowser(ctx, profileDir, false, func(bctx context.Context) error {
			if err := c.ensureAuthenticated(bctx); err != nil {
				return err
			}
			return createScheduledPost(bctx, p.Text, p.VideoPath, p.Schedule, p.Timezone, p.Progress)
		})
	})
}

func (c *Client) withProfile(ctx context.Context, allowCreate, saveOnSuccess bool, fn func(profileDir string) error) error {
	profileMu.Lock()
	defer profileMu.Unlock()

	profileDir, err := os.MkdirTemp("", "btcpp-x-profile-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profileDir)

	raw, ok, err := secureblob.Load(c.cfg.ProfileObject, c.key)
	if err != nil {
		return fmt.Errorf("load x profile archive: %w", err)
	}
	if !ok && !allowCreate {
		return &AuthError{Reason: "profile archive missing"}
	}
	if ok {
		if err := extractDir(raw, profileDir); err != nil {
			return fmt.Errorf("extract x profile archive: %w", err)
		}
	}
	if err := fn(profileDir); err != nil {
		return err
	}
	if !saveOnSuccess {
		return nil
	}
	archived, err := archiveDir(profileDir)
	if err != nil {
		return fmt.Errorf("archive x profile: %w", err)
	}
	if err := secureblob.Save(c.cfg.ProfileObject, archived, c.key); err != nil {
		return fmt.Errorf("save x profile archive: %w", err)
	}
	return nil
}

func (c *Client) withBrowser(parent context.Context, profileDir string, authTimeout bool, fn func(context.Context) error) error {
	timeout := c.cfg.PostTimeout
	if c.cfg.Headed || authTimeout {
		timeout = c.cfg.AuthWait
	}
	if c.cfg.Logf != nil {
		c.cfg.Logf("x browser starting headed=%t timeout=%s", c.cfg.Headed, timeout)
	}
	ctx, cancelTimeout := context.WithTimeout(parent, timeout)
	defer cancelTimeout()

	opts := chromeOptions(profileDir, c.cfg.Headed)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	bctx, cancelBrowser := chromedp.NewContext(
		allocCtx,
		chromedp.WithLogf(discardChromedpLogf),
		chromedp.WithErrorf(discardChromedpLogf),
	)
	if err := installBrowserShims(bctx); err != nil {
		cancelBrowser()
		return wrapBrowserTimeout(err, timeout, c.cfg.Headed)
	}
	err := fn(bctx)
	if closeErr := chromedp.Cancel(bctx); closeErr != nil && err == nil {
		err = closeErr
	}
	return wrapBrowserTimeout(err, timeout, c.cfg.Headed)
}

func discardChromedpLogf(string, ...interface{}) {}

func wrapBrowserTimeout(err error, timeout time.Duration, headed bool) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("x browser timeout after %s headed=%t: %w", timeout, headed, err)
	}
	return err
}

func chromeOptions(profileDir string, headed bool) []chromedp.ExecAllocatorOption {
	if headed {
		return []chromedp.ExecAllocatorOption{
			chromedp.UserDataDir(profileDir),
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			chromedp.UserAgent(xDesktopUserAgent),
			chromedp.Flag("headless", false),
			chromedp.Flag("enable-automation", false),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("disable-infobars", true),
			chromedp.Flag("password-store", "basic"),
			chromedp.Flag("use-mock-keychain", true),
		}
	}
	return append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(profileDir),
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.UserAgent(xDesktopUserAgent),
		chromedp.Flag("headless", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
}

func installBrowserShims(ctx context.Context) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument(`
(() => {
  const webdriverDescriptor = { get: () => undefined, configurable: true };
  Object.defineProperty(navigator, 'webdriver', webdriverDescriptor);
  Object.defineProperty(Navigator.prototype, 'webdriver', webdriverDescriptor);
})();
`).Do(ctx)
		return err
	}))
}

func detectLogin(ctx context.Context) (string, error) {
	if err := chromedp.Run(ctx, chromedp.Navigate("https://x.com/home"), chromedp.Sleep(3*time.Second)); err != nil {
		return "", err
	}
	return currentLoginState(ctx)
}

func (c *Client) ensureAuthenticated(ctx context.Context) error {
	status, err := detectLogin(ctx)
	if err != nil {
		return err
	}
	if status == "ok" {
		return nil
	}
	if status == "login required" {
		return c.loginWithCredentials(ctx)
	}
	if status != "ok" {
		return &AuthError{Reason: status}
	}
	return nil
}

func currentLoginState(ctx context.Context) (string, error) {
	var status string
	js := `(() => {
		const body = (document.body && document.body.innerText || '').toLowerCase();
		if (document.querySelector('[data-testid="SideNav_AccountSwitcher_Button"]')) return 'ok';
		if (document.querySelector('a[href="/login"], a[href="/i/flow/login"], input[name="text"], input[name="password"]')) return 'login required';
		if (body.includes('verification') || body.includes('challenge') || body.includes('unusual login') || body.includes('suspicious')) return 'challenge required';
		return 'unknown';
	})()`
	err := chromedp.Run(ctx, chromedp.Evaluate(js, &status))
	return status, err
}

func submitXLoginText(ctx context.Context, value string) error {
	if value == "" {
		return fmt.Errorf("x login username is required")
	}
	return chromedp.Run(ctx,
		chromedp.WaitVisible(`input[name="text"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="text"]`, value+"\n", chromedp.ByQuery),
	)
}

func maybeSubmitXLoginIdentifier(ctx context.Context, value string) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, err := currentLoginFlowState(ctx)
		if err != nil {
			return err
		}
		switch state {
		case "password", "ok":
			return nil
		case "identifier":
			return submitXLoginText(ctx, value)
		case "challenge required":
			return &AuthError{Reason: state}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func submitXLoginPassword(ctx context.Context, password string) error {
	if password == "" {
		return fmt.Errorf("x login password is required")
	}
	return chromedp.Run(ctx,
		chromedp.WaitVisible(`input[name="password"]`, chromedp.ByQuery),
		chromedp.SendKeys(`input[name="password"]`, password+"\n", chromedp.ByQuery),
	)
}

func waitForLoggedIn(ctx context.Context) error {
	for {
		status, err := currentLoginState(ctx)
		if err == nil && status == "ok" {
			return nil
		}
		flowState, flowErr := currentLoginFlowState(ctx)
		if flowErr == nil {
			switch flowState {
			case "challenge required", "invalid credentials":
				return &AuthError{Reason: flowState}
			}
		}
		if ctx.Err() != nil {
			if status == "" {
				status = "unknown"
			}
			return &AuthError{Reason: status}
		}
		time.Sleep(2 * time.Second)
	}
}

func currentLoginFlowState(ctx context.Context) (string, error) {
	var status string
	js := `(() => {
		const body = (document.body && document.body.innerText || '').toLowerCase();
		if (document.querySelector('[data-testid="SideNav_AccountSwitcher_Button"]')) return 'ok';
		if (document.querySelector('input[name="password"]')) return 'password';
		if (body.includes('wrong password') || body.includes('incorrect password') || body.includes('invalid password')) return 'invalid credentials';
		if (body.includes('verification') || body.includes('challenge') || body.includes('unusual login') || body.includes('suspicious') || body.includes('captcha')) return 'challenge required';
		if (document.querySelector('input[name="text"]')) return 'identifier';
		return 'unknown';
	})()`
	err := chromedp.Run(ctx, chromedp.Evaluate(js, &status))
	return status, err
}

func createPost(ctx context.Context, text, videoPath string, progress ProgressFunc) (string, error) {
	if err := chromedp.Run(ctx, chromedp.Navigate("https://x.com/compose/post")); err != nil {
		return "", err
	}
	before, _ := statusLinks(ctx)
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`div[data-testid="tweetTextarea_0"]`, chromedp.ByQuery),
		chromedp.SendKeys(`div[data-testid="tweetTextarea_0"]`, text, chromedp.ByQuery),
		chromedp.SetUploadFiles(`input[data-testid="fileInput"]`, []string{videoPath}, chromedp.ByQuery),
	); err != nil {
		return "", err
	}
	reportProgress(progress, "upload", 0, "Video handed to X")
	if err := waitForTweetButtonEnabled(ctx, progress); err != nil {
		return "", err
	}
	if err := chromedp.Run(ctx, chromedp.Click(`button[data-testid="tweetButton"]`, chromedp.ByQuery)); err != nil {
		return "", err
	}
	return waitForNewStatusURL(ctx, before)
}

func createScheduledPost(ctx context.Context, text, videoPath string, schedule time.Time, timezone string, progress ProgressFunc) error {
	if timezone != "" {
		if err := setBrowserTimezone(ctx, timezone); err != nil {
			return fmt.Errorf("set browser timezone %q: %w", timezone, err)
		}
	}
	if err := chromedp.Run(ctx, chromedp.Navigate("https://x.com/compose/post")); err != nil {
		return err
	}
	tasks := chromedp.Tasks{
		chromedp.WaitVisible(`div[data-testid="tweetTextarea_0"]`, chromedp.ByQuery),
		chromedp.SendKeys(`div[data-testid="tweetTextarea_0"]`, text, chromedp.ByQuery),
		chromedp.SetUploadFiles(`input[data-testid="fileInput"]`, []string{videoPath}, chromedp.ByQuery),
	}
	if err := chromedp.Run(ctx, tasks); err != nil {
		return err
	}
	reportProgress(progress, "upload", 0, "Video handed to X")
	if err := waitForTweetButtonEnabled(ctx, progress); err != nil {
		return err
	}
	if err := clickScheduleOption(ctx); err != nil {
		return err
	}
	if err := fillScheduleDialog(ctx, newXScheduleFields(schedule)); err != nil {
		return err
	}
	if err := clickScheduleConfirm(ctx); err != nil {
		return err
	}
	if err := chromedp.Run(ctx,
		chromedp.WaitEnabled(`button[data-testid="tweetButton"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-testid="tweetButton"]`, chromedp.ByQuery),
		chromedp.Sleep(3*time.Second),
	); err != nil {
		return err
	}
	return nil
}

func waitForTweetButtonEnabled(ctx context.Context, progress ProgressFunc) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastPercent := -1
	lastMessage := ""
	for {
		percent, message := scrapeXUploadProgress(ctx)
		if percent >= 0 || message != "" {
			if percent != lastPercent || message != lastMessage {
				reportProgress(progress, "upload", percent, message)
				lastPercent = percent
				lastMessage = message
			}
		}
		enabled, err := tweetButtonEnabled(ctx)
		if err == nil && enabled {
			reportProgress(progress, "upload", 100, "X video upload/processing complete")
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func tweetButtonEnabled(ctx context.Context) (bool, error) {
	var enabled bool
	js := `(() => {
		const btn = document.querySelector('button[data-testid="tweetButton"]');
		if (!btn) return false;
		return !btn.disabled && btn.getAttribute('aria-disabled') !== 'true';
	})()`
	err := chromedp.Run(ctx, chromedp.Evaluate(js, &enabled))
	return enabled, err
}

func scrapeXUploadProgress(ctx context.Context) (int, string) {
	var raw string
	js := `(() => {
		const body = (document.body && document.body.innerText || '').replace(/\s+/g, ' ').trim();
		const percent = body.match(/(\d{1,3})\s*%/);
		const interesting = body.match(/([^.!?\n]*(upload|uploads|uploading|processing|processed|video)[^.!?\n]*)/i);
		const p = percent ? Math.min(100, parseInt(percent[1], 10)) : -1;
		const msg = interesting ? interesting[1].trim().slice(0, 180) : '';
		return p + '|' + msg;
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &raw)); err != nil {
		return -1, ""
	}
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return -1, strings.TrimSpace(raw)
	}
	percent, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		percent = -1
	}
	return percent, strings.TrimSpace(parts[1])
}

func reportProgress(progress ProgressFunc, stage string, percent int, message string) {
	if progress == nil {
		return
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	if message == "" {
		message = "Uploading video to X"
		if percent > 0 {
			message = fmt.Sprintf("Uploading video to X (%d%%)", percent)
		}
	}
	progress(stage, percent, message)
}

func createReply(ctx context.Context, postURL, text string) (string, error) {
	if err := chromedp.Run(ctx,
		chromedp.Navigate(postURL),
		chromedp.WaitVisible(`article`, chromedp.ByQuery),
	); err != nil {
		return "", err
	}
	before, _ := statusLinks(ctx)
	if err := openReplyComposer(ctx); err != nil {
		return "", err
	}
	tasks := chromedp.Tasks{
		chromedp.WaitVisible(`div[data-testid="tweetTextarea_0"]`, chromedp.ByQuery),
		chromedp.SendKeys(`div[data-testid="tweetTextarea_0"]`, text, chromedp.ByQuery),
		chromedp.WaitEnabled(`button[data-testid="tweetButtonInline"], button[data-testid="tweetButton"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-testid="tweetButtonInline"], button[data-testid="tweetButton"]`, chromedp.ByQuery),
	}
	if err := chromedp.Run(ctx, tasks); err != nil {
		return "", err
	}
	return waitForNewStatusURL(ctx, before)
}

func openReplyComposer(ctx context.Context) error {
	var visible bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelector('div[data-testid="tweetTextarea_0"]') !== null`, &visible)); err != nil {
		return err
	}
	if visible {
		return nil
	}
	return chromedp.Run(ctx,
		chromedp.WaitVisible(`button[data-testid="reply"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-testid="reply"]`, chromedp.ByQuery),
	)
}

func setBrowserTimezone(ctx context.Context, timezone string) error {
	return chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return emulation.SetTimezoneOverride(timezone).Do(ctx)
	}))
}

type xScheduleFields struct {
	Month      string `json:"month"`
	MonthShort string `json:"monthShort"`
	Day        string `json:"day"`
	Year       string `json:"year"`
	Hour       string `json:"hour"`
	Minute     string `json:"minute"`
	Period     string `json:"period"`
}

func newXScheduleFields(at time.Time) xScheduleFields {
	period := "AM"
	hour := at.Hour()
	if hour >= 12 {
		period = "PM"
	}
	hour = hour % 12
	if hour == 0 {
		hour = 12
	}
	return xScheduleFields{
		Month:      at.Month().String(),
		MonthShort: at.Format("Jan"),
		Day:        fmt.Sprintf("%d", at.Day()),
		Year:       fmt.Sprintf("%d", at.Year()),
		Hour:       fmt.Sprintf("%d", hour),
		Minute:     fmt.Sprintf("%02d", at.Minute()),
		Period:     period,
	}
}

func clickScheduleOption(ctx context.Context) error {
	var ok bool
	js := `(() => {
		const buttons = Array.from(document.querySelectorAll('button,[role="button"]'));
		const textFor = (el) => [
			el.getAttribute('aria-label'),
			el.getAttribute('title'),
			el.innerText,
			el.textContent
		].filter(Boolean).join(' ').toLowerCase();
		const btn = document.querySelector('[data-testid="scheduleOption"]') ||
			buttons.find((el) => textFor(el).includes('schedule'));
		if (!btn || btn.getAttribute('aria-disabled') === 'true' || btn.disabled) return false;
		btn.click();
		return true;
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("x schedule button not found")
	}
	return chromedp.Run(ctx, chromedp.Sleep(time.Second))
}

type scheduleDialogResult struct {
	OK      bool     `json:"ok"`
	Missing []string `json:"missing"`
}

func fillScheduleDialog(ctx context.Context, fields xScheduleFields) error {
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var result scheduleDialogResult
	js := fmt.Sprintf(`(() => {
		const wanted = %s;
		const norm = (v) => String(v || '').trim().toLowerCase().replace(/\s+/g, ' ');
		const setValue = (el, value) => {
			const proto = Object.getPrototypeOf(el);
			const desc = Object.getOwnPropertyDescriptor(proto, 'value');
			if (desc && desc.set) {
				desc.set.call(el, value);
			} else {
				el.value = value;
			}
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
		};
		const labels = (el) => {
			const out = [
				el.getAttribute('aria-label'),
				el.getAttribute('name'),
				el.getAttribute('placeholder')
			];
			const id = el.getAttribute('id');
			if (id && window.CSS && CSS.escape) {
				const label = document.querySelector('label[for="' + CSS.escape(id) + '"]');
				if (label) out.push(label.innerText);
			}
			const labelledBy = el.getAttribute('aria-labelledby');
			if (labelledBy) {
				for (const part of labelledBy.split(/\s+/)) {
					const label = document.getElementById(part);
					if (label) out.push(label.innerText);
				}
			}
			const nearest = el.closest('label,[data-testid]');
			if (nearest) out.push(nearest.innerText);
			let parent = el.parentElement;
			for (let i = 0; parent && i < 3; i++, parent = parent.parentElement) {
				const text = parent.innerText || parent.textContent || '';
				if (text && text.length <= 80) out.push(text);
			}
			return norm(out.filter(Boolean).join(' '));
		};
		const controls = Array.from(document.querySelectorAll('select,input'));
		const matchControl = (names) => controls.find((el) => {
			const label = labels(el);
			return names.some((name) => label.includes(norm(name)));
		});
		const choose = (names, values) => {
			const el = matchControl(names);
			if (!el) return false;
			if (el.tagName === 'SELECT') {
				const option = Array.from(el.options).find((opt) => {
					const text = norm(opt.textContent);
					const value = norm(opt.value);
					return values.some((want) => text === norm(want) || value === norm(want));
				});
				if (!option) return false;
				setValue(el, option.value);
				return true;
			}
			setValue(el, values[0]);
			return true;
		};
		const missing = [];
		if (!choose(['month'], [wanted.month, wanted.monthShort])) missing.push('month');
		if (!choose(['day', 'date'], [wanted.day])) missing.push('day');
		if (!choose(['year'], [wanted.year])) missing.push('year');
		if (!choose(['hour'], [wanted.hour])) missing.push('hour');
		if (!choose(['minute'], [wanted.minute, String(Number(wanted.minute))])) missing.push('minute');
		if (!choose(['am/pm', 'am pm', 'period', 'meridiem'], [wanted.period])) missing.push('am/pm');
		return { ok: missing.length === 0, missing };
	})()`, string(payload))
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &result)); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("x schedule dialog fields not found: %s", strings.Join(result.Missing, ", "))
	}
	return chromedp.Run(ctx, chromedp.Sleep(time.Second))
}

func clickScheduleConfirm(ctx context.Context) error {
	var ok bool
	js := `(() => {
		const buttons = Array.from(document.querySelectorAll('button,[role="button"]'));
		const textFor = (el) => [
			el.getAttribute('aria-label'),
			el.getAttribute('title'),
			el.innerText,
			el.textContent
		].filter(Boolean).join(' ').trim().toLowerCase();
		const btn = buttons.find((el) => {
			if (el.disabled || el.getAttribute('aria-disabled') === 'true') return false;
			const text = textFor(el);
			return text === 'confirm' || text === 'update' || text === 'done';
		});
		if (!btn) return false;
		btn.click();
		return true;
	})()`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ok)); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("x schedule confirmation button not found")
	}
	return chromedp.Run(ctx, chromedp.Sleep(time.Second))
}

func statusLinks(ctx context.Context) (map[string]bool, error) {
	var links []string
	js := `Array.from(document.querySelectorAll('a[href*="/status/"]')).map(a => a.href.split('?')[0])`
	if err := chromedp.Run(ctx, chromedp.Evaluate(js, &links)); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(links))
	for _, link := range links {
		if strings.Contains(link, "/status/") {
			out[link] = true
		}
	}
	return out, nil
}

func waitForNewStatusURL(ctx context.Context, before map[string]bool) (string, error) {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		links, err := statusLinks(ctx)
		if err == nil {
			for link := range links {
				if !before[link] {
					return link, nil
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("x post submitted but no status URL was detected")
}

func archiveDir(root string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "Singleton") || name == "DevToolsActivePort" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return nil, err
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func extractDir(raw []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func safeJoin(root, name string) (string, error) {
	name = filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	target := filepath.Join(root, name)
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	return target, nil
}
