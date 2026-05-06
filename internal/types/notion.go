package types

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/niftynei/go-notion"
)

type (
	NotionConfig struct {
		Token        string
		EmailDb      string
		PurchasesDb  string
		SpeakersDb   string
		ConfsDb      string
		ConfsTixDb   string
		DiscountsDb  string
		NewsletterDb string
		MissivesDb   string
		TokenDb      string
		HotelsDb     string
		VolunteerDb  string
		JobTypeDb    string
                ProposalDb     string
                SpeakerConfDb  string
                ConfTalkDb     string
                RecordingsDb   string
                ConfInfoDb     string
                VolInfoDb    string
                ShiftDb      string
                OrgDb        string
                SponsorshipsDb string
                SocialPostsDb  string
	}

	Notion struct {
		Config *NotionConfig
		Client notion.API
	}
)

// Notion's docs cap traffic at "an average of 3 requests per second"
// with some bursts allowed. We size the token bucket to stay under
// that, with a small burst so a single dashboard load (which fans out
// 10-30 reads) clears in a few seconds rather than hitting 429s.
const (
	notionRPS   = 3
	notionBurst = 5
)

// notionRequestLogger is set by the app at startup so the rate-limited
// transport can emit one line per Notion API call (method, path, total
// duration, time spent waiting for a rate-limit token). Stays nil in
// CLIs / tests, where we don't want the noise.
var notionRequestLogger func(format string, args ...interface{})

// SetNotionRequestLogger installs a logger for per-request Notion timing.
// Pass app.Infos.Printf or similar at startup.
func SetNotionRequestLogger(f func(format string, args ...interface{})) {
	notionRequestLogger = f
}

// notionCallCount tracks total Notion HTTP calls observed by the
// rate-limited transport — exposed via the cache-stats endpoint to
// help diagnose unexpected fan-out.
var notionCallCount uint64

func NotionCallCount() uint64 {
	return atomic.LoadUint64(&notionCallCount)
}

// notionRecentCalls is a circular buffer of the last 64 Notion API
// calls — surfaced via /api/cache-stats to spot which endpoints are
// hitting Notion in the dashboard hot path.
var (
	notionRecentMu    sync.Mutex
	notionRecentCalls []string
)

func RecordNotionCall(line string) {
	notionRecentMu.Lock()
	defer notionRecentMu.Unlock()
	notionRecentCalls = append(notionRecentCalls, line)
	if len(notionRecentCalls) > 64 {
		notionRecentCalls = notionRecentCalls[len(notionRecentCalls)-64:]
	}
}

func RecentNotionCalls() []string {
	notionRecentMu.Lock()
	defer notionRecentMu.Unlock()
	out := make([]string, len(notionRecentCalls))
	copy(out, notionRecentCalls)
	return out
}

func (n *Notion) Setup(token string) {
	n.Client = notion.NewClient(notion.Settings{
		Token: token,
		HTTPClient: &http.Client{
			Transport: newRateLimitedTransport(notionRPS, notionBurst),
		},
	})
}

// rateLimitedTransport is an http.RoundTripper that gates every request
// through a token bucket. On 429 responses it honors Retry-After and
// replays the request once — covers the case where rate limits leak in
// from other processes hitting Notion with the same integration token.
type rateLimitedTransport struct {
	base   http.RoundTripper
	tokens chan struct{}
}

func newRateLimitedTransport(rps, burst int) *rateLimitedTransport {
	rl := &rateLimitedTransport{
		base:   http.DefaultTransport,
		tokens: make(chan struct{}, burst),
	}
	for i := 0; i < burst; i++ {
		rl.tokens <- struct{}{}
	}
	go func() {
		t := time.NewTicker(time.Second / time.Duration(rps))
		defer t.Stop()
		for range t.C {
			select {
			case rl.tokens <- struct{}{}:
			default:
			}
		}
	}()
	return rl
}

func (r *rateLimitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	overall := time.Now()
	waitStart := time.Now()
	if err := r.wait(req); err != nil {
		return nil, err
	}
	wait := time.Since(waitStart)
	rtStart := time.Now()
	resp, err := r.base.RoundTrip(req)
	atomic.AddUint64(&notionCallCount, 1)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	RecordNotionCall(fmt.Sprintf("%s %s → %d (wait=%s rt=%s)",
		req.Method, req.URL.Path, status, wait.Truncate(time.Millisecond), time.Since(rtStart).Truncate(time.Millisecond)))
	if notionRequestLogger != nil {
		notionRequestLogger("notion %s %s → %d (wait=%s rt=%s total=%s)",
			req.Method, req.URL.Path, status, wait, time.Since(rtStart), time.Since(overall))
	}
	if err != nil || resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return resp, err
	}
	// 429 — honor Retry-After (seconds), then retry once. Skip the
	// retry on writes whose bodies can't be replayed safely.
	delay := 5 * time.Second
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if n, err := strconv.Atoi(ra); err == nil && n > 0 {
			delay = time.Duration(n) * time.Second
		}
	}
	resp.Body.Close()
	if req.Body != nil && req.GetBody == nil {
		return nil, &retryAfterError{after: delay}
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		req.Body = body
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
	if err := r.wait(req); err != nil {
		return nil, err
	}
	return r.base.RoundTrip(req)
}

func (r *rateLimitedTransport) wait(req *http.Request) error {
	select {
	case <-r.tokens:
		return nil
	case <-req.Context().Done():
		return req.Context().Err()
	}
}

// retryAfterError is returned when a 429 lands on a request we can't
// safely retry (write with un-cloneable body). Surface it so the caller
// can decide whether to back off and retry at a higher level.
type retryAfterError struct{ after time.Duration }

func (e *retryAfterError) Error() string {
	return "notion rate limited; retry after " + e.after.String()
}
