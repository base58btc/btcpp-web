// Package xposter drives x.com through Chrome for the recordings uploader.
package xposter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"btcpp-web/external/secureblob"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type Config struct {
	ProfileObject string
	EncryptionKey string
	Headed        bool
	PostTimeout   time.Duration
	AuthWait      time.Duration
	Logf          func(string, ...any)
}

type Client struct {
	cfg Config
	key []byte
}

var profileMu sync.Mutex

type PostParams struct {
	Text      string
	ReplyText string
	VideoPath string
}

type PostResult struct {
	PostURL  string
	ReplyURL string
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
	err := c.withProfile(ctx, false, func(profileDir string) error {
		return c.withBrowser(ctx, profileDir, func(bctx context.Context) error {
			s, err := detectLogin(bctx)
			status = s
			return err
		})
	})
	if err != nil {
		return "", err
	}
	return status, nil
}

func (c *Client) Bootstrap(ctx context.Context) error {
	if !c.cfg.Headed {
		return fmt.Errorf("x bootstrap requires X_BROWSER_HEADED=true on a machine with a display")
	}
	waitCtx, cancel := context.WithTimeout(ctx, c.cfg.AuthWait)
	defer cancel()
	return c.withProfile(waitCtx, true, func(profileDir string) error {
		return c.withBrowser(waitCtx, profileDir, func(bctx context.Context) error {
			if err := chromedp.Run(bctx, chromedp.Navigate("https://x.com/home")); err != nil {
				return err
			}
			for {
				status, err := currentLoginState(bctx)
				if err == nil && status == "ok" {
					return nil
				}
				if waitCtx.Err() != nil {
					if status == "" {
						status = "unknown"
					}
					return &AuthError{Reason: status}
				}
				time.Sleep(2 * time.Second)
			}
		})
	})
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
	err := c.withProfile(ctx, false, func(profileDir string) error {
		return c.withBrowser(ctx, profileDir, func(bctx context.Context) error {
			if err := ensureLoggedIn(bctx); err != nil {
				return err
			}
			postURL, err := createPost(bctx, p.Text, p.VideoPath)
			if err != nil {
				return err
			}
			result.PostURL = postURL
			if p.ReplyText != "" {
				replyURL, err := createReply(bctx, postURL, p.ReplyText)
				if err != nil {
					return err
				}
				result.ReplyURL = replyURL
			}
			return nil
		})
	})
	return result, err
}

func (c *Client) withProfile(ctx context.Context, allowCreate bool, fn func(profileDir string) error) error {
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
	archived, err := archiveDir(profileDir)
	if err != nil {
		return fmt.Errorf("archive x profile: %w", err)
	}
	if err := secureblob.Save(c.cfg.ProfileObject, archived, c.key); err != nil {
		return fmt.Errorf("save x profile archive: %w", err)
	}
	return nil
}

func (c *Client) withBrowser(parent context.Context, profileDir string, fn func(context.Context) error) error {
	timeout := c.cfg.PostTimeout
	if c.cfg.Headed {
		timeout = c.cfg.AuthWait
	}
	ctx, cancelTimeout := context.WithTimeout(parent, timeout)
	defer cancelTimeout()

	opts := chromeOptions(profileDir, c.cfg.Headed)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	bctx, cancelBrowser := chromedp.NewContext(allocCtx)
	if err := installBrowserShims(bctx); err != nil {
		cancelBrowser()
		return err
	}
	err := fn(bctx)
	if closeErr := chromedp.Cancel(bctx); closeErr != nil && err == nil {
		err = closeErr
	}
	return err
}

func chromeOptions(profileDir string, headed bool) []chromedp.ExecAllocatorOption {
	if headed {
		return []chromedp.ExecAllocatorOption{
			chromedp.UserDataDir(profileDir),
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
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
Object.defineProperty(navigator, 'webdriver', { get: () => undefined });
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

func ensureLoggedIn(ctx context.Context) error {
	status, err := detectLogin(ctx)
	if err != nil {
		return err
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

func createPost(ctx context.Context, text, videoPath string) (string, error) {
	if err := chromedp.Run(ctx, chromedp.Navigate("https://x.com/compose/post")); err != nil {
		return "", err
	}
	before, _ := statusLinks(ctx)
	tasks := chromedp.Tasks{
		chromedp.WaitVisible(`div[data-testid="tweetTextarea_0"]`, chromedp.ByQuery),
		chromedp.SendKeys(`div[data-testid="tweetTextarea_0"]`, text, chromedp.ByQuery),
		chromedp.SetUploadFiles(`input[data-testid="fileInput"]`, []string{videoPath}, chromedp.ByQuery),
		chromedp.Sleep(8 * time.Second),
		chromedp.WaitEnabled(`button[data-testid="tweetButton"]`, chromedp.ByQuery),
		chromedp.Click(`button[data-testid="tweetButton"]`, chromedp.ByQuery),
	}
	if err := chromedp.Run(ctx, tasks); err != nil {
		return "", err
	}
	return waitForNewStatusURL(ctx, before)
}

func createReply(ctx context.Context, postURL, text string) (string, error) {
	if err := chromedp.Run(ctx,
		chromedp.Navigate(postURL),
		chromedp.WaitVisible(`article`, chromedp.ByQuery),
	); err != nil {
		return "", err
	}
	before, _ := statusLinks(ctx)
	tasks := chromedp.Tasks{
		chromedp.WaitVisible(`div[data-testid="tweetTextarea_0"]`, chromedp.ByQuery),
		chromedp.SendKeys(`div[data-testid="tweetTextarea_0"]`, text, chromedp.ByQuery),
		chromedp.Sleep(1 * time.Second),
		chromedp.Click(`button[data-testid="tweetButtonInline"], button[data-testid="tweetButton"]`, chromedp.ByQuery),
	}
	if err := chromedp.Run(ctx, tasks); err != nil {
		return "", err
	}
	return waitForNewStatusURL(ctx, before)
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
