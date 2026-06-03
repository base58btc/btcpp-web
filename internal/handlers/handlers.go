package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/coingecko"
	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/auth"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/ics"
	"btcpp-web/internal/imgproc"
	"btcpp-web/internal/missives"
	"btcpp-web/internal/types"
	"btcpp-web/internal/volunteers"

	"github.com/gorilla/mux"
	"github.com/gorilla/schema"

	qrcode "github.com/skip2/go-qrcode"

	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/webhook"
)

var pages []string = []string{"index", "vegas25", "terms", "privacy"}

const (
	maxFormBodyBytes      = 1 << 20  // 1 MiB
	maxMultipartBodyBytes = 12 << 20 // 12 MiB
	maxUploadFileBytes    = 10 << 20 // 10 MiB
	maxWebhookBodyBytes   = 1 << 20  // 1 MiB
)

var errUploadTooLarge = errors.New("uploaded file is too large")

func limitRequestBody(w http.ResponseWriter, r *http.Request, max int64) {
	r.Body = http.MaxBytesReader(w, r.Body, max)
}

func fieldGroup(name string, v interface{}, isRange bool) EmailFieldGroup {
	fields := getStructFields(v)
	prefix := name + "."
	if isRange {
		prefix = "."
	}
	items := make([]string, len(fields))
	for i, f := range fields {
		items[i] = prefix + f
	}
	return EmailFieldGroup{Name: name, Items: items, IsRange: isRange}
}

func getStructFields(v interface{}) []string {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	var fields []string
	for i := 0; i < t.NumField(); i++ {
		fields = append(fields, t.Field(i).Name)
	}
	return fields
}

func newFormDecoder() *schema.Decoder {
	dec := schema.NewDecoder()
	dec.IgnoreUnknownKeys(true)
	dec.RegisterConverter("", func(value string) reflect.Value {
		return reflect.ValueOf(strings.TrimSpace(value))
	})
	dec.RegisterConverter(types.Twitter{}, func(value string) reflect.Value {
		return reflect.ValueOf(types.ParseTwitter(value))
	})
	return dec
}

/* Thank you StackOverflow https://stackoverflow.com/a/50581032 */
func findAndParseTemplates(rootDir string, funcMap template.FuncMap) (*template.Template, error) {
	cleanRoot := filepath.Clean(rootDir)
	pfx := len(cleanRoot) + 1
	root := template.New("")

	err := filepath.Walk(cleanRoot, func(path string, info os.FileInfo, e1 error) error {
		if !info.IsDir() && strings.HasSuffix(path, ".tmpl") {
			if e1 != nil {
				return e1
			}

			b, e2 := ioutil.ReadFile(path)
			if e2 != nil {
				return e2
			}

			name := path[pfx:]
			t := root.New(name).Funcs(funcMap)
			_, e2 = t.Parse(string(b))
			if e2 != nil {
				return e2
			}
		}

		return nil
	})

	return root, err
}

func loadTemplates(ctx *config.AppContext) error {

	var err error
	funcMap := template.FuncMap{
		"safeURL": func(s string) template.URL {
			u := strings.TrimSpace(s)
			switch {
			case u == "":
				return ""
			case strings.HasPrefix(u, "/"):
				return template.URL(u)
			case strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://"):
				return template.URL(u)
			case strings.HasPrefix(u, "data:image/png;base64,"):
				return template.URL(u)
			default:
				return ""
			}
		},
		"css": func(s string) template.HTML {
			return template.HTML(fmt.Sprintf(`<style type="text/css">%s</style>`, s))
		},
		"isLast": func(index int, count int) bool {
			return index+1 == count
		},
		"ishtml": func(s string) template.HTML {
			return template.HTML(s)
		},
		"mul": func(a, b int) int {
			return a * b
		},
		"div": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"add": func(a, b int) int {
			return a + b
		},
		"inSlice": func(needle string, haystack []string) bool {
			for _, s := range haystack {
				if s == needle {
					return true
				}
			}
			return false
		},
		"hasPrefix": strings.HasPrefix,
		"trim":      strings.TrimSpace,
		// dict builds a map[string]any from variadic key/value pairs
		// — enables passing named params to template blocks (e.g.
		// {{ template "cal_picker" (dict "Title" .Name "Start" ...) }}).
		// Errors out at template-exec time on an odd number of args
		// or a non-string key, so misuse fails loudly instead of
		// silently truncating.
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict requires an even number of arguments")
			}
			m := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				k, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict key %d not a string", i)
				}
				m[k] = values[i+1]
			}
			return m, nil
		},
		// mapVenue resolves the raw venue slug ("one"/"two"/...) to
		// the human-readable label ("Main Stage" / ...). Thin
		// template-side wrapper around ics.MapVenue so the cal_picker
		// block can render a meaningful Location instead of leaking
		// the internal slug.
		"mapVenue": ics.MapVenue,
		// jsonStr returns a JSON-encoded, double-quoted string —
		// used by event_jsonld.tmpl to safely embed user-supplied
		// titles / descriptions / venue names into a <script
		// type="application/ld+json"> block. template.JS bypasses
		// html/template's JS-context escaping; the JSON itself is
		// already script-safe.
		"jsonStr": func(s string) template.JS {
			b, _ := json.Marshal(s)
			return template.JS(b)
		},
		// jsonDate formats a time.Time / *time.Time as a
		// JSON-encoded RFC 3339 string, or `null` when zero / nil.
		"jsonDate": func(t interface{}) template.JS {
			format := func(tt time.Time) template.JS {
				if tt.IsZero() {
					return template.JS("null")
				}
				b, _ := json.Marshal(tt.Format(time.RFC3339))
				return template.JS(b)
			}
			switch v := t.(type) {
			case time.Time:
				return format(v)
			case *time.Time:
				if v == nil {
					return template.JS("null")
				}
				return format(*v)
			}
			return template.JS("null")
		},
		"confSocialImage": confSocialImage,
		"absoluteSEOURL":  absoluteSEOURL,
		// rfc3339 formats a time.Time / *time.Time as RFC 3339.
		// Returns "" for a nil pointer or zero time so templates can
		// safely emit it into a data-* attribute without leaking
		// "0001-01-01T00:00:00Z". Used by the cal_picker block to
		// hand structured timestamps to the JS picker.
		"rfc3339": func(t interface{}) string {
			switch v := t.(type) {
			case nil:
				return ""
			case time.Time:
				if v.IsZero() {
					return ""
				}
				return v.Format(time.RFC3339)
			case *time.Time:
				if v == nil || v.IsZero() {
					return ""
				}
				return v.Format(time.RFC3339)
			default:
				return ""
			}
		},
		"dollars": func(cents int64) string {
			// "%.2f" with the dollars+cents split keeps negative
			// values rendering correctly (e.g. -$1.50). Used by
			// the dashboard affiliate stats — values stored in
			// cents, displayed as $X.XX.
			whole := cents / 100
			frac := cents % 100
			if frac < 0 {
				frac = -frac
			}
			return fmt.Sprintf("%d.%02d", whole, frac)
		},
		"sats": func(sats int64) string {
			// Group with thousands separators so "1,234,567 sats"
			// reads more easily than "1234567". Negative values
			// keep the minus before the digits.
			return groupSatsCommas(sats)
		},
		"satsBitcoin": func(sats int64) template.HTML {
			// Renders sats as BTC decimal notation ("0.00012345"),
			// with the "0." prefix + leading zeros wrapped in a
			// text-gray-400 span so only the significant digits
			// inherit the surrounding paragraph color (green for
			// saved, indigo for earned).
			return formatBitcoinAmount(sats)
		},
		"siteStats": func() siteStatsView {
			return formatSiteStats(getters.FetchSiteStats(ctx))
		},
		"ge": func(a, b int) bool {
			return a >= b
		},
		"mod": func(a, b int) int {
			if b == 0 {
				return 0
			}
			return a % b
		},
		"iterRange": func(start, end int) []int {
			if end <= start {
				return nil
			}
			out := make([]int, 0, end-start+1)
			for i := start; i <= end; i++ {
				out = append(out, i)
			}
			return out
		},
		"ganttLeft": func(times *types.Times, dayMin, dayMax int) float64 {
			if times == nil {
				return 0
			}
			startMin := float64(times.Start.Hour()*60 + times.Start.Minute())
			dayStartMin := float64(dayMin * 60)
			dayWidth := float64((dayMax - dayMin) * 60)
			if dayWidth == 0 {
				return 0
			}
			return (startMin - dayStartMin) / dayWidth * 100
		},
		"ganttWidth": func(times *types.Times, dayMin, dayMax int) float64 {
			if times == nil || times.End == nil {
				return 0
			}
			startMin := float64(times.Start.Hour()*60 + times.Start.Minute())
			endMin := float64(times.End.Hour()*60 + times.End.Minute())
			dayWidth := float64((dayMax - dayMin) * 60)
			if dayWidth == 0 {
				return 0
			}
			return (endMin - startMin) / dayWidth * 100
		},
		"hourPct": func(hour, dayMin, dayMax int) float64 {
			width := float64(dayMax - dayMin)
			if width == 0 {
				return 0
			}
			return float64(hour-dayMin) / width * 100
		},
		"shiftStartHHMM": func(s *types.WorkShift) string {
			if s == nil || s.ShiftTime == nil {
				return ""
			}
			return s.ShiftTime.Start.Format("15:04")
		},
		"shiftEndHHMM": func(s *types.WorkShift) string {
			if s == nil || s.ShiftTime == nil || s.ShiftTime.End == nil {
				return ""
			}
			return s.ShiftTime.End.Format("15:04")
		},
		"spacesURL": func(key string) string {
			return spaces.PublicURL(key)
		},
		"formatHourMin":  FormatHourMin,
		"hourLabels":     HourLabels,
		"venueChipClass": VenueChipClasses,
		"venueLabel": func(raw string) string {
			// Resolves the raw venue slug ("one" / "two" / "three")
			// to the human-readable stage label. Falls back to the
			// raw value when the mapping doesn't recognize it, so
			// custom venues from older confs still render
			// something sensible.
			if label := ics.MapVenue(raw); label != "" {
				return label
			}
			return raw
		},
		"navConfs": func() NavConfList {
			return buildNavConfList(ctx)
		},
		"sponsorTiers": func(conf *types.Conf) []*SponsorTier {
			if conf == nil {
				return nil
			}
			return SponsorTiersForConf(ctx, conf.Ref)
		},
		"sponsorBanner": func(conf *types.Conf) []*types.Sponsorship {
			if conf == nil {
				return nil
			}
			return SponsorBannerForConf(ctx, conf.Ref)
		},
		"speakerPhoto": func(photo string) string {
			if photo == "" {
				return spaces.PublicURL("speakers/default.avif")
			}
			return spaces.PublicURL("speakers/" + photo)
		},
		"talkClipart": func(filename string) string {
			// Empty filename → empty URL so templates render a
			// broken/empty image rather than a "talks/" path that
			// 404s. Most call sites already gate on the field
			// being non-empty before rendering the <img> at all.
			if filename == "" {
				return ""
			}
			return spaces.PublicURL("talks/" + filename)
		},
		"inviteLink": func(p *types.Proposal) string {
			if p == nil {
				return ""
			}
			return helpers.InviteLink(ctx, p.ID, p.InviteToken)
		},
		"formatTime": formatRunOfShowTime,
		"inDev": func() bool {
			return !ctx.Env.Prod
		},
		"isFuture": func(t time.Time) bool {
			return t.After(time.Now())
		},
	}
	ctx.TemplateCache, err = findAndParseTemplates("templates", funcMap)
	return err
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if item == x {
			return true
		}
	}
	return false
}

func findTicket(app *config.AppContext, tixID string) (*types.ConfTicket, *types.Conf) {
	confs, err := getters.FetchConfsCached(app)
	if err != nil {
		app.Err.Printf("unable to find ticket?? %s", err)
		return nil, nil
	}

	for _, conf := range confs {
		for _, tix := range conf.Tickets {
			if tix.ID == tixID {
				return tix, conf
			}
		}
	}

	return nil, nil
}

func determineTixPrice(ctx *config.AppContext, tixSlug string) (*types.Conf, *types.ConfTicket, uint, bool, error) {

	tixParts := strings.Split(tixSlug, "+")
	if len(tixParts) != 3 {
		return nil, nil, 0, false, fmt.Errorf("not enough ticket parts?? needed 3. %s", tixSlug)
	}

	tix, conf := findTicket(ctx, tixParts[0])
	if tix == nil {
		return nil, nil, 0, false, fmt.Errorf("Unable to find tix %s", tixParts[0])
	}
	tixTypeOpts := []string{"default", "local"}
	if !contains(tixTypeOpts, tixParts[1]) {
		return nil, nil, 0, false, fmt.Errorf("type %s not in list %v", tixParts[1], tixTypeOpts)
	}
	isLocal := tixParts[1] == "local"

	currencyTypeOpts := []string{"btc", "fiat"}
	if !contains(currencyTypeOpts, tixParts[2]) {
		return nil, nil, 0, false, fmt.Errorf("type %s not in list %v", tixParts[2], currencyTypeOpts)
	}
	if tixParts[2] == "btc" {
		if isLocal {
			return conf, tix, tix.Local, true, nil
		}
		return conf, tix, tix.BTC, true, nil
	}

	if isLocal {
		return conf, tix, tix.Local, false, nil
	}
	return conf, tix, tix.USD, false, nil
}

/* Find ticket where current sold + date > inputs */
func findCurrTix(conf *types.Conf, soldCount uint) *types.ConfTicket {
	now := time.Now()
	/* Sort the tickets! */
	tixs := types.ConfTickets(conf.Tickets)
	sort.Sort(&tixs)
	for _, tix := range tixs {
		if tix.Expires.Start.Before(now) {
			continue
		}
		if tix.Max <= soldCount {
			continue
		}
		return tix
	}

	/* No tix available! */
	return nil
}

/* Find ticket where current sold + date > inputs */
func findMaxTix(conf *types.Conf) *types.ConfTicket {
	/* Sort the tickets! */
	tixs := types.ConfTickets(conf.Tickets)
	sort.Sort(&tixs)

	if len(tixs) <= 0 {
		return nil
	}

	maxTix := tixs[0]
	for _, tix := range tixs {
		if tix.USD > maxTix.USD {
			maxTix = tix
		}
	}

	return maxTix
}

// Routes sets up the routes for the application
// siteStatsView is the about-page-friendly shape of the cached site
// stats, exposed to templates via the {{ siteStats }} function.
type siteStatsView struct {
	Confs     int    // raw integer — no rounding (e.g. "14")
	Talks     string // rounded down to nearest 50 with "+" suffix (e.g. "400+")
	Attendees string // same, formatted as "X.Yk+" once over 1000 (e.g. "3.2k+")
}

func formatSiteStats(s getters.SiteStatsValues) siteStatsView {
	return siteStatsView{
		Confs:     s.PastConfs,
		Talks:     formatRoundedDownPlus(s.PastTalks),
		Attendees: formatRoundedDownPlus(s.Attendees),
	}
}

// formatRoundedDownPlus floors n to the nearest 50, then renders with a
// "+" suffix. Above 1000 it switches to "X.Yk+" (one decimal, trailing
// zero trimmed).
func formatRoundedDownPlus(n int) string {
	if n < 50 {
		return strconv.Itoa(n)
	}
	r := (n / 50) * 50
	if r >= 1000 {
		whole := r / 1000
		hundreds := (r % 1000) / 100
		if hundreds == 0 {
			return fmt.Sprintf("%dk+", whole)
		}
		return fmt.Sprintf("%d.%dk+", whole, hundreds)
	}
	return fmt.Sprintf("%d+", r)
}

// statusRecorder wraps http.ResponseWriter so requestLog can read the
// final status code after the handler returns. WriteHeader stores the
// code and forwards; if the handler never calls WriteHeader explicitly,
// we default to 200 (matching net/http's behavior).
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// requestLog is a middleware that logs each incoming request's start
// and completion (method, path, status, duration). Combined with the
// per-Notion-call timing emitted by the rate-limited transport, this
// gives a full timeline for any slow request.
func requestLog(ctx *config.AppContext, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx.Infos.Printf("→ %s %s", r.Method, r.URL.Path)
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(sr, r)
		ctx.Infos.Printf("← %s %s [%d] %s", r.Method, r.URL.Path, sr.status, time.Since(start))
	})
}

func Routes(app *config.AppContext) (http.Handler, error) {
	r := mux.NewRouter()

	err := loadTemplates(app)
	if err != nil {
		return r, err
	}

	err = addFaviconRoutes(r)
	if err != nil {
		return r, err
	}

	/* Handle 404s */
	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handle404(w, r, app)
	})

	// Set up the routes, we'll have one page per course
	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		RenderPage(w, r, app, "index")
	}).Methods("GET")

	// SEO endpoints — robots policy at site root + dynamic sitemap
	// rebuilt from the confs cache on each request.
	r.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		Robots(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		Sitemap(w, r, app)
	}).Methods("GET")

	/* List of 'normie' pages */
	for _, page := range pages {
		/* Normie Pages */
		renderPage := page
		r.HandleFunc("/"+renderPage, func(w http.ResponseWriter, r *http.Request) {
			RenderPage(w, r, app, renderPage)
		}).Methods("GET")
	}

	/* Theme aliases — keyword URLs that map to a specific edition
	   ("ecash" was Berlin 24, "mempool" was ATX 25, etc.). Kept
	   alive for legacy share links and for vanity URLs that don't
	   match a conf tag. Self-aliases (berlin23 → /conf/berlin23,
	   etc.) used to live here too but now resolve natively via the
	   /{conf} catch-all registered near the end of this router. */
	r.HandleFunc("/ecash", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/berlin24", http.StatusMovedPermanently)
	}).Methods("GET")
	r.HandleFunc("/mempool", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/atx25", http.StatusMovedPermanently)
	}).Methods("GET")
	r.HandleFunc("/lightning", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/berlin25", http.StatusMovedPermanently)
	}).Methods("GET")
	r.HandleFunc("/exploits", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/floripa26", http.StatusMovedPermanently)
	}).Methods("GET")
	r.HandleFunc("/talks", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}).Methods("GET")

	/* /conf/* legacy paths — 301 to the new short form. Captures
	   /conf/{tag}, /conf/{tag}/talks, /conf/{tag}/talk/{anchor}/calendar.ics,
	   /conf/{tag}/success. The handler relocates the leading "/conf"
	   prefix; any preserved query string + hash fragment carries
	   through (the fragment never reaches the server but the
	   browser preserves it across a 301). */
	r.HandleFunc("/conf/{conf}", func(w http.ResponseWriter, r *http.Request) {
		redirectStripConfPrefix(w, r)
	}).Methods("GET")
	r.HandleFunc("/conf/{conf}/talks", func(w http.ResponseWriter, r *http.Request) {
		redirectStripConfPrefix(w, r)
	}).Methods("GET")
	r.HandleFunc("/conf/{conf}/talk/{anchor}/calendar.ics", func(w http.ResponseWriter, r *http.Request) {
		redirectStripConfPrefix(w, r)
	}).Methods("GET")
	r.HandleFunc("/conf/{conf}/success", func(w http.ResponseWriter, r *http.Request) {
		redirectStripConfPrefix(w, r)
	}).Methods("GET")

	r.HandleFunc("/volunteer", func(w http.ResponseWriter, r *http.Request) {
		RenderVolunteers(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/volunteer/{conf}", func(w http.ResponseWriter, r *http.Request) {
		RenderVolunteerConf(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/talk", func(w http.ResponseWriter, r *http.Request) {
		RenderSpeakers(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/talk/{conf}", func(w http.ResponseWriter, r *http.Request) {
		RenderSpeakerConf(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/contact", func(w http.ResponseWriter, r *http.Request) {
		ContactPage(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/sponsor", func(w http.ResponseWriter, r *http.Request) {
		SponsorPage(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/tix/{tix}/collect-email", func(w http.ResponseWriter, r *http.Request) {
		HandleCheckout(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/tix/{tix}/checkout", func(w http.ResponseWriter, r *http.Request) {
		HandleCheckout(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/tix/{tix}/apply-discount", func(w http.ResponseWriter, r *http.Request) {
		HandleDiscount(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/tix/{tix}", func(w http.ResponseWriter, r *http.Request) {
		HandleTixSelection(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/conf-reload", func(w http.ResponseWriter, r *http.Request) {
		ReloadConf(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/check-in/{ticket}", func(w http.ResponseWriter, r *http.Request) {
		CheckIn(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/i/{conf}/sendcal", func(w http.ResponseWriter, r *http.Request) {
		if id := requireConfAdmin(w, r, app); id == nil {
			return
		}
		SendCals(w, r, app)
	}).Methods("GET", "POST")

	AddMediaRoutes(r, app)

	r.HandleFunc("/ticket/{ticket}", func(w http.ResponseWriter, r *http.Request) {
		Ticket(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/ticket/{ticket}/pdf", func(w http.ResponseWriter, r *http.Request) {
		TicketPDF(w, r, app)
	}).Methods("GET")

	/* Register routes for newsletters */
	missives.RegisterNewsletterHandlers(r, app)
	emails.RegisterEndpoints(r, app)

	/* Setup stripe! */
	stripe.Key = app.Env.StripeKey
	r.HandleFunc("/callback/stripe", func(w http.ResponseWriter, r *http.Request) {
		StripeCallback(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/callback/opennode", func(w http.ResponseWriter, r *http.Request) {
		OpenNodeCallback(w, r, app)
	}).Methods("GET", "POST")

	/* Internal pages */
	r.HandleFunc("/{conf}/volcoord", func(w http.ResponseWriter, r *http.Request) {
		VolAdmin(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/volcoord/send-orientation", func(w http.ResponseWriter, r *http.Request) {
		SendVolOrientation(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/volcoord/orientation", func(w http.ResponseWriter, r *http.Request) {
		VolAdminScheduleOrientation(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/volcoord/sendcal", func(w http.ResponseWriter, r *http.Request) {
		if id := requireConfVolcoord(w, r, app); id == nil {
			return
		}
		SendVolCals(w, r, app)

		params := mux.Vars(r)
		confTag := params["conf"]
		http.Redirect(w, r, "/"+confTag+"/volcoord?flash=Shift+calendar+invites+sent", http.StatusFound)
	}).Methods("GET", "POST")

	r.HandleFunc("/{conf}/volcoord/promote", func(w http.ResponseWriter, r *http.Request) {
		VolAdminPromote(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/auto-assign", func(w http.ResponseWriter, r *http.Request) {
		VolAdminAutoAssign(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/shifts", func(w http.ResponseWriter, r *http.Request) {
		VolAdminShifts(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/volcoord/shifts/new", func(w http.ResponseWriter, r *http.Request) {
		VolAdminCreateShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/shifts/gen", func(w http.ResponseWriter, r *http.Request) {
		VolAdminGenWorkShifts(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/shifts/{shiftRef}/reschedule", func(w http.ResponseWriter, r *http.Request) {
		VolShiftReschedule(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/shifts/{shiftRef}/update", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/vol/{volRef}", func(w http.ResponseWriter, r *http.Request) {
		VolAdminDetails(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/volcoord/vol/{volRef}/status", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateStatus(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/vol/{volRef}/availability", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateAvailability(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/vol/{volRef}/work-prefs", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateWorkPrefs(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/vol/{volRef}/add-shift", func(w http.ResponseWriter, r *http.Request) {
		VolAdminAddShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/vol/{volRef}/remove-shift", func(w http.ResponseWriter, r *http.Request) {
		VolAdminRemoveShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/vol/{volRef}/scheduled", func(w http.ResponseWriter, r *http.Request) {
		VolAdminMarkScheduled(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/email", func(w http.ResponseWriter, r *http.Request) {
		VolAdminBulkEmail(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/volcoord/decline-selected", func(w http.ResponseWriter, r *http.Request) {
		VolAdminDeclineSelected(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		Dashboard(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		Login(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		AuthLanding(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		LogoutHandler(w, r, app)
	}).Methods("POST")

	// /dashboard/affiliate/* MUST be registered before
	// /dashboard/{confTag}/edit below, otherwise mux's first-match
	// rule has /dashboard/affiliate/edit eaten by the speakerconf
	// route (confTag="affiliate" → DashboardEditSpeakerConf can't
	// find the conf, silently bounces the visitor back to
	// /dashboard with no flash).
	r.HandleFunc("/dashboard/affiliate/new", func(w http.ResponseWriter, r *http.Request) {
		AffiliateNew(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/dashboard/affiliate/new", func(w http.ResponseWriter, r *http.Request) {
		AffiliateCreate(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/dashboard/affiliate/edit", func(w http.ResponseWriter, r *http.Request) {
		AffiliateEdit(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/dashboard/affiliate/edit", func(w http.ResponseWriter, r *http.Request) {
		AffiliateUpdate(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/dashboard/affiliate/disable", func(w http.ResponseWriter, r *http.Request) {
		AffiliateDisable(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/dashboard/{confTag}/edit", func(w http.ResponseWriter, r *http.Request) {
		DashboardEditSpeakerConf(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/api/orgs/search", func(w http.ResponseWriter, r *http.Request) {
		OrgSearch(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/api/speakers/search", func(w http.ResponseWriter, r *http.Request) {
		SpeakerSearch(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/api/speakers/{speakerID}/roles", func(w http.ResponseWriter, r *http.Request) {
		SpeakerRolesGet(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		GlobalAdminDashboard(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/admin/roles", func(w http.ResponseWriter, r *http.Request) {
		SpeakerRolesUpdate(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/admin/missives", func(w http.ResponseWriter, r *http.Request) {
		TemplatedMissivesAdmin(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/admin/missives", func(w http.ResponseWriter, r *http.Request) {
		TemplatedMissivesSave(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/admin/missives/upload-image", func(w http.ResponseWriter, r *http.Request) {
		TemplatedMissivesUploadImage(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/admin/missives/test-send", func(w http.ResponseWriter, r *http.Request) {
		TemplatedMissivesTestSend(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/admin/missives/schedule", func(w http.ResponseWriter, r *http.Request) {
		TemplatedMissivesSchedule(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/api/cache-stats", func(w http.ResponseWriter, r *http.Request) {
		id := auth.RequireOptional(r, app)
		if id == nil || !id.IsGlobalAdmin() {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := map[string]interface{}{
			"caches":       getters.CacheStats(),
			"recent_calls": types.RecentNotionCalls(),
		}
		json.NewEncoder(w).Encode(out)
	}).Methods("GET")

	r.HandleFunc("/dashboard/talks/{proposalID}/edit", func(w http.ResponseWriter, r *http.Request) {
		DashboardEditProposal(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/dashboard/talks/{proposalID}/details", func(w http.ResponseWriter, r *http.Request) {
		DashboardTalkDetails(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/dashboard/talks/{proposalID}/withdraw", func(w http.ResponseWriter, r *http.Request) {
		DashboardWithdraw(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/dashboard/talks/{proposalID}/accept", func(w http.ResponseWriter, r *http.Request) {
		DashboardAcceptInvite(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/dashboard/talks/{proposalID}/decline", func(w http.ResponseWriter, r *http.Request) {
		DashboardDeclineInvite(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/dashboard/talks/{proposalID}/confirm", func(w http.ResponseWriter, r *http.Request) {
		DashboardConfirmTalk(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/dashboard/invite/{proposalID}", func(w http.ResponseWriter, r *http.Request) {
		DashboardInviteCoSpeaker(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/dashboard/talks/{proposalID}/speakers/{speakerConfID}/remove", func(w http.ResponseWriter, r *http.Request) {
		DashboardRemoveCoSpeaker(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/dashboard/speaker", func(w http.ResponseWriter, r *http.Request) {
		DashboardEditSpeaker(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/invite-speaker/{proposalID}", func(w http.ResponseWriter, r *http.Request) {
		InviteSpeaker(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/invite-speaker/{proposalID}/decline", func(w http.ResponseWriter, r *http.Request) {
		InviteSpeakerDecline(w, r, app)
	}).Methods("POST")

	// Backwards compat: existing magic-link emails point at /vols/shift.
	// Forward them to /dashboard, preserving the HMAC + email query params.
	r.HandleFunc("/vols/shift", func(w http.ResponseWriter, r *http.Request) {
		target := "/dashboard"
		if raw := r.URL.RawQuery; raw != "" {
			target += "?" + raw
		}
		http.Redirect(w, r, target, http.StatusFound)
	}).Methods("GET", "POST")

	r.HandleFunc("/dashboard/vol/{shiftRef}/calendar.ics", func(w http.ResponseWriter, r *http.Request) {
		DashboardVolShiftICS(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/dashboard/vol/{conf}/shifts/resend-invites", func(w http.ResponseWriter, r *http.Request) {
		DashboardVolShiftsResend(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/shift/{conf}", func(w http.ResponseWriter, r *http.Request) {
		VolunteerShiftSignup(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/vols/shift/{conf}/select", func(w http.ResponseWriter, r *http.Request) {
		VolunteerSelectShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/shift/{conf}/remove", func(w http.ResponseWriter, r *http.Request) {
		VolunteerRemoveShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/shift/{conf}/submit", func(w http.ResponseWriter, r *http.Request) {
		VolunteerSubmitShifts(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/shift/{conf}/decline", func(w http.ResponseWriter, r *http.Request) {
		VolunteerDecline(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/shift/{conf}/availability", func(w http.ResponseWriter, r *http.Request) {
		VolunteerUpdateAvailability(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/shift/{conf}/work-prefs", func(w http.ResponseWriter, r *http.Request) {
		VolunteerUpdateWorkPrefs(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/gifts", func(w http.ResponseWriter, r *http.Request) {
		AdminGifts(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/gifts/clipart.zip", func(w http.ResponseWriter, r *http.Request) {
		AdminGiftsClipartZip(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/comp-tickets", func(w http.ResponseWriter, r *http.Request) {
		AdminCompTickets(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/{conf}/admin/discounts", func(w http.ResponseWriter, r *http.Request) {
		AdminDiscounts(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/{conf}/admin/recordings", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminList(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/recordings/oauth/youtube/start", func(w http.ResponseWriter, r *http.Request) {
		RecordingsYTOAuthStart(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/admin/recordings/oauth/youtube/callback", func(w http.ResponseWriter, r *http.Request) {
		RecordingsYTOAuthCallback(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/recordings/oauth/youtube/callback", func(w http.ResponseWriter, r *http.Request) {
		RecordingsYTOAuthCallback(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/recordings/oauth/youtube/disconnect", func(w http.ResponseWriter, r *http.Request) {
		RecordingsYTOAuthDisconnect(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/x/auth-check", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminXAuthCheck(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminDetail(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/recordings/{id}/upload-yt", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminUploadYT(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/schedule", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminSchedule(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminUploadSourceFile(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/x-copy", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminSaveXCopy(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/post-x", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminPostXNow(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/schedule-x", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminScheduleX(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/x", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminSaveXLink(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/retry-x", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminRetryX(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/recordings/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminJobStatus(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/recordings/{id}/x-status", func(w http.ResponseWriter, r *http.Request) {
		RecordingsAdminXJobStatus(w, r, app)
	}).Methods("GET")

	// Dev-only smoke endpoint for the self-hosted ICS pipeline.
	// Production registrations of the route are blocked at the
	// handler boundary (TrialCalInvite checks ctx.Env.Prod).
	r.HandleFunc("/trial-cal-invite", func(w http.ResponseWriter, r *http.Request) {
		TrialCalInvite(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/admin/orgs", func(w http.ResponseWriter, r *http.Request) {
		OrgList(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/admin/orgs/new", func(w http.ResponseWriter, r *http.Request) {
		OrgNew(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/admin/orgs/new", func(w http.ResponseWriter, r *http.Request) {
		OrgCreate(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/admin/orgs/upload-logo", func(w http.ResponseWriter, r *http.Request) {
		OrgLogoUpload(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/admin/orgs/{ref}", func(w http.ResponseWriter, r *http.Request) {
		OrgDetail(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/admin/orgs/{ref}", func(w http.ResponseWriter, r *http.Request) {
		OrgSave(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/sponsors", func(w http.ResponseWriter, r *http.Request) {
		SponsorshipsList(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/admin/sponsors/new", func(w http.ResponseWriter, r *http.Request) {
		SponsorshipCreate(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/social", func(w http.ResponseWriter, r *http.Request) {
		SocialAdmin(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/admin/social/post", func(w http.ResponseWriter, r *http.Request) {
		SocialPost(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/speakers", func(w http.ResponseWriter, r *http.Request) {
		SpeakerAdmin(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/admin/speakers/new", func(w http.ResponseWriter, r *http.Request) {
		SpeakerAdminNew(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/{conf}/admin/speakers/{speakerID}/refresh-cards", func(w http.ResponseWriter, r *http.Request) {
		AdminSpeakerRefreshCards(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/speakers/{speakerID}/edit", func(w http.ResponseWriter, r *http.Request) {
		SpeakerAdminEdit(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/{conf}/admin/speakerconfs/{speakerConfID}/edit", func(w http.ResponseWriter, r *http.Request) {
		SpeakerConfAdminEdit(w, r, app)
	}).Methods("GET", "POST")

	r.HandleFunc("/{conf}/admin/speakers/email", func(w http.ResponseWriter, r *http.Request) {
		SpeakerAdminBulkEmail(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/registrations", func(w http.ResponseWriter, r *http.Request) {
		RegistrationsAdmin(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/admin/registrations/email", func(w http.ResponseWriter, r *http.Request) {
		RegistrationsAdminBulkEmail(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/applicants", func(w http.ResponseWriter, r *http.Request) {
		ProposalAdmin(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/admin", func(w http.ResponseWriter, r *http.Request) {
		OrganizerDashboard(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/", func(w http.ResponseWriter, r *http.Request) {
		OrganizerDashboard(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/review", func(w http.ResponseWriter, r *http.Request) {
		ReviewProposals(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/review/{proposalID}/{action}", func(w http.ResponseWriter, r *http.Request) {
		ReviewProposalAction(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/proposal/{proposalID}/invite", func(w http.ResponseWriter, r *http.Request) {
		AdminProposalInviteLink(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/proposal/{proposalID}/edit", func(w http.ResponseWriter, r *http.Request) {
		AdminEditProposal(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/{conf}/admin/proposal/{proposalID}/speakers/attach", func(w http.ResponseWriter, r *http.Request) {
		AdminEditProposalAttachSpeaker(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/proposal/{proposalID}/speakers/{speakerConfID}/remove", func(w http.ResponseWriter, r *http.Request) {
		AdminProposalRemoveSpeaker(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/invite-speaker", func(w http.ResponseWriter, r *http.Request) {
		AdminInviteSpeaker(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/invite-speaker", func(w http.ResponseWriter, r *http.Request) {
		AdminInviteSpeakerSubmit(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/invite-speaker/sent", func(w http.ResponseWriter, r *http.Request) {
		AdminInviteSpeakerSent(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/admin/hotels", func(w http.ResponseWriter, r *http.Request) {
		HotelsAdmin(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/hotels", func(w http.ResponseWriter, r *http.Request) {
		HotelsAdminSave(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/hotels/upload-img", func(w http.ResponseWriter, r *http.Request) {
		HotelImageUpload(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/schedule", func(w http.ResponseWriter, r *http.Request) {
		ScheduleConf(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/cliparts", func(w http.ResponseWriter, r *http.Request) {
		AdminCliparts(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/cliparts/{proposalID}", func(w http.ResponseWriter, r *http.Request) {
		AdminClipartsUpload(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/social-cards.zip", func(w http.ResponseWriter, r *http.Request) {
		AdminSocialCardsZip(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/{conf}/admin/run-of-show", func(w http.ResponseWriter, r *http.Request) {
		RunOfShowAdmin(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/admin/schedule/sendcal-updates", func(w http.ResponseWriter, r *http.Request) {
		ScheduleSendCalUpdates(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/schedule/place", func(w http.ResponseWriter, r *http.Request) {
		SchedulePlace(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/schedule/unplace", func(w http.ResponseWriter, r *http.Request) {
		ScheduleUnplace(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/schedule/resize", func(w http.ResponseWriter, r *http.Request) {
		ScheduleResize(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/schedule/add-hackathon", func(w http.ResponseWriter, r *http.Request) {
		ScheduleAddHackathon(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/schedule/add-talk", func(w http.ResponseWriter, r *http.Request) {
		ScheduleAddTalk(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/applicants/email", func(w http.ResponseWriter, r *http.Request) {
		ProposalAdminBulkEmail(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/{conf}/admin/applicants/accept", func(w http.ResponseWriter, r *http.Request) {
		ProposalAdminAccept(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/applicants/resend-tickets", func(w http.ResponseWriter, r *http.Request) {
		AdminResendSpeakerTickets(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/applicants/{proposalID}/cancel", func(w http.ResponseWriter, r *http.Request) {
		AdminCancelTalk(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/applicants/{proposalID}/refresh-card", func(w http.ResponseWriter, r *http.Request) {
		AdminProposalRefreshCard(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/proposals/{proposalID}/sendcal", func(w http.ResponseWriter, r *http.Request) {
		AdminProposalSendCal(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/applicants/sendcal-all", func(w http.ResponseWriter, r *http.Request) {
		AdminProposalSendCalAll(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/{conf}/admin/speakers/sendcal", func(w http.ResponseWriter, r *http.Request) {
		if id := requireConfAdmin(w, r, app); id == nil {
			return
		}
		SendCals(w, r, app)

		params := mux.Vars(r)
		confTag := params["conf"]
		http.Redirect(w, r, "/"+confTag+"/admin/speakers?flash=Calendar+invites+sent", http.StatusFound)
	}).Methods("GET", "POST")

	// 301-redirect every legacy admin URL (/admin/{tag}/...,
	// /vols/admin/{tag}/..., etc.) to the new /{conf}/{role}/...
	// shape. Registered last so it doesn't shadow live routes.
	RegisterAdminRedirects(r)

	// Public conf routes — canonical short form `/{tag}`. Registered
	// last among single-segment routes so the literal entries above
	// (/dashboard, /login, /talk, /sponsor, /privacy, the theme
	// aliases, ...) win first. Unknown {conf} falls through to a
	// clean 404 via the handlers' FindConf branch.
	r.HandleFunc("/{conf}", func(w http.ResponseWriter, r *http.Request) {
		RenderConf(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/talks", func(w http.ResponseWriter, r *http.Request) {
		RenderTalks(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/talk/{anchor}/calendar.ics", func(w http.ResponseWriter, r *http.Request) {
		TalkPublicICS(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/{conf}/success", func(w http.ResponseWriter, r *http.Request) {
		RenderConfSuccess(w, r, app)
	}).Methods("GET")

	// Create a file server to serve static files from the "static" directory.
	// Wrap with a Cache-Control: max-age=3600 header so browsers
	// can serve cached copies without revalidating. http.FileServer
	// already emits Last-Modified, so deployments still invalidate
	// stale assets within the hour via conditional GET / 304s.
	fs := http.FileServer(http.Dir("static"))
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", staticCache(fs)))

	return requestLog(app, noIndexRobots(r)), nil
}

func getFaviconHandler(name string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, fmt.Sprintf("static/favicon/%s", name))
	}
}

func addFaviconRoutes(r *mux.Router) error {
	files, err := ioutil.ReadDir("static/favicon/")
	if err != nil {
		return err
	}

	/* If asked for a favicon, we'll serve it up */
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		r.HandleFunc(fmt.Sprintf("/%s", file.Name()), getFaviconHandler(file.Name())).Methods("GET")
	}

	return nil
}

func listConfs(w http.ResponseWriter, ctx *config.AppContext) []*types.Conf {
	var confs types.ConfList
	var err error
	confs, err = getters.FetchConfsCached(ctx)
	if err != nil {
		// FIXME add an internal error page
		http.Error(w, "Unable to load confereneces, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/conf-reload conf load failed ! %s", err.Error())
		return nil
	}

	sort.Sort(&confs)
	return confs
}

func listVolunteerConfs(w http.ResponseWriter, ctx *config.AppContext) []*types.Conf {
	confs := listConfs(w, ctx)
	if confs == nil {
		return nil
	}
	var out []*types.Conf
	for _, conf := range confs {
		if conf != nil && conf.VolunteerOpen() {
			out = append(out, conf)
		}
	}
	return out
}

func listJobs(w http.ResponseWriter, ctx *config.AppContext) []*types.JobType {
	var jobs types.JobsList
	var err error
	jobs, err = getters.FetchJobsCached(ctx)
	if err != nil {
		// FIXME add an internal error page
		http.Error(w, "Unable to load jobs, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("jobs load failed ! %s", err.Error())
		return nil
	}

	sort.Sort(&jobs)
	return jobs
}

func handle404(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(http.StatusNotFound)
	ctx.Infos.Printf("404'd: %s", r.URL.Path)

	RenderPage(w, r, ctx, "404")
}

// discountSessionKey is the SCS session key under which a per-conf
// discount code from a /{tag}?code= visit is stashed. Per-conf
// scoping keeps codes from one event from leaking into another's
// checkout flow when a visitor browses multiple confs in the same
// session.
func discountSessionKey(confTag string) string {
	return "disc:" + confTag
}

// affiliateSessionKey is the parallel slot for silent (%0) affiliate
// codes — the buyer never sees a discount UI, but the code rides
// through to checkout via a hidden form input so the affiliate still
// gets credit.
func affiliateSessionKey(confTag string) string {
	return "aff:" + confTag
}

// affiliateMath returns (saved, earned) for one checkout. Inputs +
// outputs share a single unit — sats in this codebase, but the math
// is unit-agnostic. preDiscountPerTicket is the per-ticket list
// price BEFORE any discount; paidTotal is what the buyer actually
// paid; count is the number of tickets. Inputs should be in the same
// unit, usually fiat cents. The 20% ceiling is fixed: affiliates earn
// whatever's left after the buyer's actual savings come out of that
// ceiling. Both outputs are floored at zero to avoid negatives leaking
// into Notion.
func affiliateMath(preDiscountPerTicket, count, paidTotal int64) (saved, earned int64) {
	original := preDiscountPerTicket * count
	ceiling := original * 20 / 100
	saved = original - paidTotal
	if saved < 0 {
		saved = 0
	}
	earned = ceiling - saved
	if earned < 0 {
		earned = 0
	}
	return saved, earned
}

// recordAffiliateUsageFromCheckout writes one AffiliateUsage row to
// Notion when a successful checkout consumed a discount code that
// has an AffiliateEmail set. The list price + paid total arrive in
// fiat cents (whatever currency the tier was priced in). Saved/Earned
// are split in fiat cents first, then converted to sats. Doing the
// split before BTC conversion keeps a %20 buyer discount from leaving
// tiny EarnedSats remainders due to spot-price or rounding drift.
//
// preDiscountCentsStr is a string from webhook metadata (Stripe map
// / OpenNode struct); missing or unparseable means we skip recording
// rather than guessing. CoinGecko fetch failures also skip — a
// missing row is recoverable (re-run a backfill); a bogus row is
// not. Failures are logged, never fatal.
func recordAffiliateUsageFromCheckout(ctx *config.AppContext, conf *types.Conf, entry *types.Entry, preDiscountCentsStr string) {
	if conf == nil || entry == nil || entry.DiscountRef == "" {
		return
	}
	disc, err := getters.GetDiscountByRef(ctx, entry.DiscountRef)
	if err != nil || disc == nil || disc.AffiliateEmail == "" {
		// Non-affiliate code — nothing to record. Errors are
		// silent because the discount might just be missing
		// from cache mid-refresh.
		return
	}
	preDiscountCents, err := strconv.ParseInt(strings.TrimSpace(preDiscountCentsStr), 10, 64)
	if err != nil || preDiscountCents <= 0 {
		ctx.Err.Printf("affiliate usage skip %s: missing pre-discount-cents (%q)", disc.CodeName, preDiscountCentsStr)
		return
	}
	count := int64(len(entry.Items))
	if count <= 0 {
		return
	}
	currency := strings.TrimSpace(entry.Currency)
	if currency == "" {
		ctx.Err.Printf("affiliate usage skip %s: empty entry.Currency", disc.CodeName)
		return
	}
	savedCents, earnedCents := affiliateMath(preDiscountCents, count, entry.Total)
	savedSats, err := coingecko.CentsToSats(savedCents, currency)
	if err != nil {
		ctx.Err.Printf("affiliate usage skip %s: coingecko saved cents→sats (%s): %s", disc.CodeName, currency, err)
		return
	}
	earnedSats, err := coingecko.CentsToSats(earnedCents, currency)
	if err != nil {
		ctx.Err.Printf("affiliate usage skip %s: coingecko earned cents→sats (%s): %s", disc.CodeName, currency, err)
		return
	}
	err = getters.RecordAffiliateUsage(ctx, getters.AffiliateUsageInput{
		CodeName:       disc.CodeName,
		AffiliateEmail: disc.AffiliateEmail,
		ConfTag:        conf.Tag,
		SavedSats:      savedSats,
		EarnedSats:     earnedSats,
		TicketsCount:   uint(count),
	})
	if err != nil {
		ctx.Err.Printf("affiliate usage record %s for %s: %s", disc.CodeName, disc.AffiliateEmail, err)
	}
}

// groupSatsCommas formats an int64 sat amount with thousands
// separators, e.g. 1234567 → "1,234,567". Negative values keep the
// minus before the grouped digits.
func groupSatsCommas(sats int64) string {
	neg := sats < 0
	if neg {
		sats = -sats
	}
	str := strconv.FormatInt(sats, 10)
	n := len(str)
	if n <= 3 {
		if neg {
			return "-" + str
		}
		return str
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(str[:pre])
		if n > pre {
			b.WriteByte(',')
		}
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(str[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// formatBitcoinAmount renders a sat amount as a fixed-precision
// BTC decimal string ("0.00012345"), with the leading zeros + the
// "0." prefix wrapped in <span class="text-gray-400"> so the
// surrounding paragraph color (e.g. text-green-700) only reaches
// the significant digits. Amounts ≥ 1 BTC start with the integer
// part and render in full color, no leading-zero span.
func formatBitcoinAmount(sats int64) template.HTML {
	neg := sats < 0
	if neg {
		sats = -sats
	}
	whole := sats / 100_000_000
	frac := sats % 100_000_000
	full := fmt.Sprintf("%d.%08d", whole, frac)

	// First non-zero digit (the decimal point and leading zeros all
	// stay in the grey prefix; everything from the first 1-9 onward
	// inherits the paragraph color).
	splitIdx := -1
	for i := 0; i < len(full); i++ {
		c := full[i]
		if c >= '1' && c <= '9' {
			splitIdx = i
			break
		}
	}
	prefix := ""
	if neg {
		prefix = "-"
	}
	if splitIdx < 0 {
		// 0.00000000 — fully zero.
		return template.HTML(fmt.Sprintf(`%s<span class="text-gray-400">%s</span>`, prefix, full))
	}
	if splitIdx == 0 {
		// ≥ 1 BTC — starts with a significant digit.
		return template.HTML(prefix + full)
	}
	return template.HTML(fmt.Sprintf(`%s<span class="text-gray-400">%s</span>%s`, prefix, full[:splitIdx], full[splitIdx:]))
}

func calcTixHMAC(ctx *config.AppContext, conf *types.Conf, tixPrice uint, discountPrice uint, discountCode string) string {
	mac := hmac.New(sha256.New, ctx.Env.HMACKey[:])
	mac.Write([]byte(conf.Ref))
	priceBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(priceBytes, uint64(tixPrice))
	mac.Write(priceBytes)
	binary.LittleEndian.PutUint64(priceBytes, uint64(discountPrice))
	mac.Write(priceBytes)
	mac.Write([]byte(discountCode))
	return hex.EncodeToString(mac.Sum(nil))
}

// effectiveDiscountCode picks which of the two form-carried codes
// drives checkout: a buyer-typed Discount wins over a silent
// AffiliateCode. The override semantic — "type a different code
// and the prior affiliate's credit is dropped" — falls out of this:
// the discount-ref recorded on the entry is whatever the effective
// code resolves to, so RecordAffiliateUsage only fires for the
// code that was actually applied.
func effectiveDiscountCode(typedCode, affiliateCode string) string {
	if strings.TrimSpace(typedCode) != "" {
		return typedCode
	}
	return affiliateCode
}

func ReloadConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	/* Refresh the confs */
	getters.WaitFetch(ctx)
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		http.Error(w, "Unable to load confereneces, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/conf-reload conf load failed ! %s", err.Error())
		return
	}
	for _, conf := range confs {
		getters.UpdateSoldTix(ctx, conf)
	}

	/* We redirect to home on success */
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// acceptedSpeakersForConf returns the deduped speaker list for the
// conf page's "Who's Coming" section. Unions two sources so the list
// is complete even when one side is sparsely populated:
//
//  1. Speakers attached to ConfTalk-backed Talks for this conf with
//     Proposal.Status == Accepted/Scheduled. This is the source the
//     previous filterSpeakers used and remains the primary feed for
//     events whose Proposal rows don't have ScheduleFor set
//     (older confs / hand-entered ConfTalks pre-dating the
//     proposal-flow).
//
//  2. Accepted/Scheduled Proposals whose ScheduleFor.Tag matches
//     this conf. Picks up speakers attached to a freshly-Accepted
//     proposal whose ConfTalk hasn't been provisioned yet (accept
//     pipeline failure, status flipped manually in Notion, etc).
//
// Any status other than Accepted/Scheduled is filtered out (Applied
// / InReview / Waitlisted / Invited / WeDecline / TheyDecline /
// Rejected). Speaker views get Company / OrgLogo overlaid from the
// per-conf SpeakerConf row so templates referencing Speaker.Company
// render the conf-specific affiliation rather than the stale top-
// level Speaker.Company.
func acceptedSpeakersForConf(ctx *config.AppContext, confTag string, talks []*types.Talk) types.Speakers {
	var speakers types.Speakers
	seen := make(map[string]bool)

	// Source 1: speakers from ConfTalk-backed Talks (the existing
	// pipeline). Talks already carry conf-overlaid Speaker views
	// from talkFromConfTalk, so we just dedupe and append.
	for _, talk := range talks {
		if talk == nil {
			continue
		}
		if talk.Status != StatusAccepted && talk.Status != "Scheduled" {
			continue
		}
		for _, sp := range talk.Speakers {
			if sp == nil || seen[sp.ID] {
				continue
			}
			seen[sp.ID] = true
			speakers = append(speakers, sp)
		}
	}

	// Source 2: Accepted/Scheduled proposals scheduled for this conf.
	// Best-effort — if the proposals cache read errors we still
	// return the talks-derived list rather than blanking the page.
	//
	// Cached Proposals only have SpeakerConfRefs (raw page IDs) — the
	// Speakers []*SpeakerConf slice is populated only by callers that
	// run resolveProposalSpeakers (e.g. LoadTalksFromConfTalks). Walk
	// the refs directly via the SpeakerConf cache so this works on
	// proposals that haven't been provisioned a ConfTalk yet (which
	// is exactly the case this source is meant to catch).
	proposals, err := getters.FetchProposalsCached(ctx)
	if err != nil {
		ctx.Err.Printf("acceptedSpeakersForConf %s proposals: %s", confTag, err)
		return speakers
	}
	for _, p := range proposals {
		if p == nil {
			continue
		}
		if p.Status != StatusAccepted && p.Status != "Scheduled" {
			continue
		}
		if p.ScheduleFor == nil || p.ScheduleFor.Tag != confTag {
			continue
		}
		for _, ref := range p.SpeakerConfRefs {
			sc := getters.FetchSpeakerConfByID(ref)
			if sc == nil || sc.Speaker == nil {
				continue
			}
			if seen[sc.Speaker.ID] {
				continue
			}
			seen[sc.Speaker.ID] = true
			view := *sc.Speaker
			view.Company = sc.Company
			view.OrgLogo = sc.OrgPhoto
			speakers = append(speakers, &view)
		}
	}
	return speakers
}

func RenderTalks(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	allTalks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	// Page renders every approved talk — Accepted (admin draft) and
	// Scheduled (cal invite sent). Declined/Rejected variants drop
	// off so retracted talks don't linger on the public list.
	var talks types.TalkTime
	for _, t := range allTalks {
		if t == nil {
			continue
		}
		if t.Status == StatusAccepted || t.Status == StatusScheduled {
			talks = append(talks, t)
		}
	}

	var evSpeakers types.Speakers
	evSpeakers = acceptedSpeakersForConf(ctx, conf.Tag, talks)

	sort.Sort(talks)
	sort.Sort(evSpeakers)

	confCopy := *conf
	confCopy.HasAgenda = anyScheduledTalk(allTalks)
	conf = &confCopy

	err = ctx.TemplateCache.ExecuteTemplate(w, "sched.tmpl", &ConfPage{
		Talks:         talks,
		EventSpeakers: evSpeakers,
		Conf:          conf,
		Year:          helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/talks ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func RenderConfSuccess(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	// Clear the stashed discount + silent-affiliate codes now that
	// the visitor has completed checkout — otherwise a subsequent
	// ticket purchase from the same browser session would silently
	// re-apply the code, even if the original link's owner only
	// intended one use. Per-conf, so other confs' stashed codes
	// stay put.
	ctx.Session.Remove(r.Context(), discountSessionKey(conf.Tag))
	ctx.Session.Remove(r.Context(), affiliateSessionKey(conf.Tag))

	err = ctx.TemplateCache.ExecuteTemplate(w, "success.tmpl", &SuccessPage{
		Conf: conf,
		Year: helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/success ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func RenderSpeakers(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	confs := listConfs(w, ctx)
	err := ctx.TemplateCache.ExecuteTemplate(w, "embeds/speaker_select.tmpl", &VolunteerPage{
		Confs: confs,
		Year:  helpers.CurrentYear(),
	})

	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/speakers ExecuteTemplate failed ! %s", err.Error())
		return
	}
}

func contentTypeFromFilename(filename string) string {
	ext := filepath.Ext(filename) // e.g., ".png"
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return "application/octet-stream" // fallback
	}
	return mimeType
}

func processFileUpload(ctx *config.AppContext, r *http.Request, field string) (string, error) {
	file, handler, err := r.FormFile(field)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// Read the file data
	fileData, err := ioutil.ReadAll(file)
	if err != nil {
		return "", err
	}

	filename := handler.Filename
	contentType := contentTypeFromFilename(filename)

	return getters.UploadFile(ctx, contentType, filename, fileData)
}

// readMultipartFile reads a single named file from a multipart form and
// returns its bytes + content type + lowercase file extension. It does not
// upload anywhere — caller decides what to do with the bytes. Returns
// http.ErrMissingFile when the field is absent (typical for optional
// uploads).
func readMultipartFile(r *http.Request, field string) (raw []byte, contentType string, ext string, err error) {
	file, handler, err := r.FormFile(field)
	if err != nil {
		return nil, "", "", err
	}
	defer file.Close()
	raw, err = ioutil.ReadAll(io.LimitReader(file, maxUploadFileBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(raw)) > maxUploadFileBytes {
		return nil, "", "", errUploadTooLarge
	}
	if len(raw) == 0 {
		return nil, "", "", errors.New("empty upload")
	}
	filename := handler.Filename
	contentType = detectedImageContentType(raw, filename)
	if contentType == "" {
		return nil, "", "", errors.New("unsupported image type")
	}
	ext = strings.ToLower(filepath.Ext(filename))
	if ext == "" || contentTypeFromFilename(filename) != contentType {
		ext = extForImageContentType(contentType)
	}
	return raw, contentType, ext, nil
}

func detectedImageContentType(raw []byte, filename string) string {
	detected := http.DetectContentType(raw)
	if allowedUploadImageType(detected) {
		return detected
	}
	if strings.EqualFold(filepath.Ext(filename), ".avif") && isAVIF(raw) {
		return "image/avif"
	}
	return ""
}

func isAVIF(raw []byte) bool {
	if len(raw) < 12 || string(raw[4:8]) != "ftyp" {
		return false
	}
	for i := 8; i+4 <= len(raw); i += 4 {
		brand := string(raw[i : i+4])
		if brand == "avif" || brand == "avis" {
			return true
		}
	}
	return false
}

func allowedUploadImageType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif":
		return true
	default:
		return false
	}
}

func extForImageContentType(contentType string) string {
	switch strings.ToLower(contentType) {
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/avif":
		return ".avif"
	default:
		return ".jpg"
	}
}

// uploadSpeakerPic uploads PicFile to Notion (returning the file ID) and also
// returns the raw bytes + content type + extension so the caller can mirror
// the original to Spaces and generate AVIF derivatives.
func uploadSpeakerPic(ctx *config.AppContext, r *http.Request) (notionID string, raw []byte, contentType string, ext string, err error) {
	file, handler, err := r.FormFile("PicFile")
	if err != nil {
		return "", nil, "", "", err
	}
	defer file.Close()

	raw, err = ioutil.ReadAll(file)
	if err != nil {
		return "", nil, "", "", err
	}

	filename := handler.Filename
	contentType = contentTypeFromFilename(filename)
	ext = strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		ext = ".jpg"
	}

	notionID, err = getters.UploadFile(ctx, contentType, filename, raw)
	return notionID, raw, contentType, ext, err
}

func RenderSpeakerConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	if !conf.Active {
		handle404(w, r, ctx)
		return
	}

	confs := listConfs(w, ctx)

	switch r.Method {
	case http.MethodGet:

		// Optional magic-link auth — when present and valid, pre-fill the
		// form with the speaker's existing data so they don't re-type
		// contact info / pfp / shirt size / etc.
		var knownSpeaker *types.Speaker
		var encodedHMAC, encodedEmail string
		var subscribed, returning bool
		if email, h, err := validateVolEmail(r, ctx); err == nil {
			encodedHMAC = h
			encodedEmail = r.URL.Query().Get("em")
			speakers, lerr := getters.GetSpeakersByEmail(ctx, email)
			if lerr == nil && len(speakers) == 1 {
				knownSpeaker = speakers[0]
			}
			// Best-effort lookups: failures just leave the
			// checkbox visible. The form still works.
			if s, err := getters.IsSubscribedTo(ctx, email, "newsletter"); err == nil {
				subscribed = s
			}
			if reg, err := getters.EmailHasRegistration(ctx, email); err == nil {
				returning = reg
			}
		}

		daylist := conf.DaysList("days-", true)
		err = ctx.TemplateCache.ExecuteTemplate(w, "embeds/talk.tmpl", &SpeakerPage{
			Conf:                   conf,
			Confs:                  confs,
			ConfItems:              helpers.GetOtherConfs(confs, *conf),
			DueDate:                conf.DateBeforeStart(conf.TalksDueDays()),
			DaysList:               daylist[1:],
			RSVPFor:                daylist[0].ItemDesc,
			PresentationType:       helpers.GetPresentationTypes(),
			RecordingOptions:       helpers.GetRecordingOptions(),
			KnownSpeaker:           knownSpeaker,
			HMAC:                   encodedHMAC,
			Email:                  encodedEmail,
			IsNewsletterSubscriber: subscribed,
			IsReturningAttendee:    returning,
			Year:                   helpers.CurrentYear(),
		})

		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/volunteer/%s ExecuteTemplate failed ! %s", conf.Tag, err.Error())
			return
		}
		return
	case http.MethodPost:
		limitRequestBody(w, r, maxMultipartBodyBytes)
		err = r.ParseMultipartForm(maxUploadFileBytes)
		if err != nil {
			ctx.Err.Printf("/talk/{conf} unable to parse multipart form %s", err)
			w.Write([]byte(helpers.ErrSpeakerApp("Error parsing form.")))
			return
		}

		dec := newFormDecoder()
		var talkapp types.TalkApp
		err = dec.Decode(&talkapp, r.PostForm)
		if err != nil {
			ctx.Err.Printf("/speaker/{conf} unable to decode form %s", err)
			w.Write([]byte(helpers.ErrSpeakerApp("Unable to register you: form parsing error")))
			return
		}
		trimTalkApp(&talkapp)

		/* ten divided by two is five */
		if talkapp.Captcha != 5 {
			w.Write([]byte(helpers.ErrSpeakerApp("Incorrect captcha. The answer is 5.")))
			return
		}

		talkapp.ParseAvailability("days-", r.PostForm)
		dinneropt := r.PostForm.Get("DinnerOpt")
		talkapp.DinnerRSVP = dinneropt == "Yes"
		talkapp.OtherEvents = helpers.ParseFormConfs("conf-", r.PostForm, confs)

		/* Read PicFile bytes (no Notion upload — cropped JPEG goes
		   only to Spaces). Optional for returning speakers — when
		   the form was rendered with KnownSpeaker, the upload field
		   is hidden and Submit will keep the existing Photo. */
		picRaw, picContentType, picExt, err := readMultipartFile(r, "PicFile")
		hasNewPic := err == nil && len(picRaw) > 0
		if err != nil && err != http.ErrMissingFile {
			ctx.Err.Printf("/talk/{conf} unable to read speaker profile pic %s", err)
			w.Write([]byte(helpers.ErrSpeakerApp("Error uploading pfp.")))
			return
		}
		if hasNewPic {
			picShortID := imgproc.ShortID(picRaw)
			talkapp.NormPhoto = picShortID + picExt
		}

		/* Read OrgLogoFile if present (optional). */
		logoRaw, logoContentType, logoExt, logoErr := readMultipartFile(r, "OrgLogoFile")
		hasLogo := logoErr == nil && len(logoRaw) > 0
		if logoErr != nil && logoErr != http.ErrMissingFile {
			ctx.Err.Printf("/talk/{conf} unable to read org logo %s", logoErr)
			w.Write([]byte(helpers.ErrSpeakerApp("Error uploading org logo.")))
			return
		}
		if hasLogo {
			logoShortID := imgproc.ShortID(logoRaw)
			talkapp.OrgLogo = logoShortID + logoExt
		}

		if talkapp.ScheduleFor == nil {
			talkapp.ScheduleFor = conf
		}

		ctx.Infos.Printf("parsed talkapp: %v", talkapp)

		submitResult, err := newSubmitPipeline(ctx).Submit(&talkapp)
		if err != nil {
			ctx.Err.Printf("/talk/{conf} submit pipeline failed %s", err)
			if errors.Is(err, ErrDuplicateSpeakerEmail) {
				w.Write([]byte(helpers.ErrSpeakerApp("That email already has multiple speaker records — please contact us to resolve.")))
			} else {
				w.Write([]byte(helpers.ErrSpeakerApp("Unable to register you.")))
			}
			return
		}

		/* Mirror photo to Spaces — fire-and-forget so we don't block
		   the user behind ffmpeg encodes. Skip when no new pic. */
		if hasNewPic {
			go newPhotoPipeline(ctx).mirrorPicToSpaces(picRaw, picContentType, picExt)
		}
		if hasLogo {
			go newPhotoPipeline(ctx).mirrorOrgLogoToSpaces(logoRaw, logoContentType, logoExt)
		}

		/* Subscribe the applicant to the talkapp + per-conf
		   talkapp lists (and the general newsletter when they
		   opted in). We bypass NewSubs here so the
		   subscription is recorded without firing the legacy
		   list-welcome missives — the OnlyFor "talkapp"
		   letter below is what they actually get. */
		newslist := missives.MakeApplicationSublist(conf.Tag, "talkapp", talkapp.Subscribe)
		if _, err := getters.SubscribeEmailList(ctx, talkapp.Email, newslist); err != nil {
			ctx.Err.Printf("!!! Unable to subscribe to newsletter %s: %v", err, talkapp)
		}

		/* Send the application-received ack via the OnlyFor
		   "talkapp" letter. */
		sendTalkAppLetter(ctx, conf, submitResult, talkapp.Email)

		/* When the form was submitted from a magic-link-authed
		   context (the dashboard's "Propose another talk" link
		   sets ?hr= & ?em= on the form action), bounce the user
		   back to the dashboard rather than dropping them on a
		   standalone success page. HTMX consumes HX-Redirect to
		   navigate the whole page. */
		if encHMAC := r.URL.Query().Get("hr"); encHMAC != "" {
			encEmail := r.URL.Query().Get("em")
			flash := url.QueryEscape("Thanks — your talk proposal is in.")
			w.Header().Set("HX-Redirect",
				fmt.Sprintf("/dashboard?hr=%s&em=%s&flash=%s", encHMAC, encEmail, flash))
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Write([]byte(helpers.SuccessApp("Your speaker application has been submitted! We'll be in touch.")))
		return
	}

}

func RenderVolunteers(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	confs := listVolunteerConfs(w, ctx)
	err := ctx.TemplateCache.ExecuteTemplate(w, "embeds/volunteer_select.tmpl", &VolunteerPage{
		Confs: confs,
		Year:  helpers.CurrentYear(),
	})

	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/volunteers ExecuteTemplate failed ! %s", err.Error())
		return
	}
}

func RenderVolunteerConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	if !conf.Active {
		handle404(w, r, ctx)
		return
	}
	if !conf.VolunteerOpen() {
		handle404(w, r, ctx)
		return
	}

	jobs := listJobs(w, ctx)
	confs := listVolunteerConfs(w, ctx)

	switch r.Method {
	case http.MethodGet:
		// Pre-fill from the user's Speakers row when the
		// form is opened from a /dashboard "Sign up to
		// volunteer" link (hr+em query params verified).
		// Silent fallback when params are absent / wrong /
		// the user has no Speakers row — public visitors
		// get the blank form.
		//
		// Hometown rides along from the SpeakerConf for
		// THIS conf when one exists (the speaker has
		// already volunteered Hometown there); falls back
		// to blank otherwise.
		var prefill *types.Speaker
		var prefillHome string
		if email, _, vErr := validateVolEmail(r, ctx); vErr == nil {
			sps, scs, sErr := getters.GetSpeakerConfsByEmail(ctx, email)
			if sErr == nil && len(sps) > 0 {
				prefill = sps[0]
			}
			if sErr == nil {
				for _, sc := range scs {
					if sc == nil || sc.ComingFrom == "" {
						continue
					}
					for _, p := range sc.Proposals {
						if p != nil && p.ScheduleFor != nil && p.ScheduleFor.Ref == conf.Ref {
							prefillHome = sc.ComingFrom
							break
						}
					}
					if prefillHome != "" {
						break
					}
				}
			}
		}
		err = ctx.TemplateCache.ExecuteTemplate(w, "embeds/volunteer.tmpl", &VolunteerPage{
			Conf:            conf,
			Confs:           confs,
			YesJobs:         helpers.BuildJobs("yjob-", jobs, true),
			NoJobs:          helpers.BuildJobs("njob-", jobs, false),
			ConfItems:       helpers.GetOtherConfs(confs, *conf),
			DaysList:        conf.DaysList("days-", true),
			Prefill:         prefill,
			PrefillHometown: prefillHome,
			Year:            helpers.CurrentYear(),
		})

		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/volunteer/%s ExecuteTemplate failed ! %s", conf.Tag, err.Error())
			return
		}
		return
	case http.MethodPost:
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		dec := newFormDecoder()
		var vol types.Volunteer
		err = dec.Decode(&vol, r.PostForm)
		if err != nil {
			ctx.Err.Printf("/volunteer/{conf} unable to decode form %s", err)
			w.Write([]byte(helpers.ErrVolApp("Unable to register you.")))
			return
		}
		trimVolunteer(&vol)

		/* ten divided by two is five */
		if vol.Captcha != 5 {
			w.Write([]byte(helpers.ErrVolApp("Incorrect captcha. The answer is 5.")))
			return
		}

		vol.ParseAvailability("days-", r.PostForm)
		vol.OtherEvents = helpers.ParseFormConfs("conf-", r.PostForm, confs)
		vol.WorkYes = helpers.ParseFormJobs("yjob-", r.PostForm, jobs)
		vol.WorkNo = helpers.ParseFormJobs("njob-", r.PostForm, jobs)

		if len(vol.ScheduleFor) == 0 {
			vol.ScheduleFor = append(vol.ScheduleFor, conf)
		}

		err = getters.RegisterVolunteer(ctx, &vol)
		if err != nil {
			ctx.Err.Printf("/volunteer/{conf} unable to register volunteer %s", err)
			w.Write([]byte(helpers.ErrVolApp("Unable to register you.")))
			return
		}

		/* Send application acknowledgment email */
		volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
		if err != nil {
			ctx.Err.Printf("/volunteer/{conf} unable to fetch volinfos %s", err)
			w.Write([]byte(helpers.ErrVolApp("Unable to register you.")))
			return
		}

		_, err = emails.OnlyForVolApp(ctx, &vol, conf, volinfo)
		if err != nil {
			ctx.Err.Printf("/volunteer/{conf} unable to send ack email: %s", err)
		}

		/* Register to mailing lists :) */
		/* Note: this also sends pre-saved missives for the vol app list! */
		newslist := missives.MakeApplicationSublist(conf.Tag, "volapp", vol.Subscribe)
		err = missives.NewSubs(ctx, vol.Email, newslist)

		if err != nil {
			ctx.Err.Printf("!!! Unable to subscribe to newsletter %s: %v", err, vol)
		}

		w.Write([]byte(helpers.SuccessApp("Your volunteer application has been submitted! We'll be in touch.")))
		return
	}

}

func RenderConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	// Stash a ?code= query in the session so the checkout page
	// can apply it without the visitor copy-pasting. The slot
	// depends on whether the code is buyer-facing (any non-zero
	// percent / fixed-amount discount) or a silent affiliate
	// referral (a `%0` code, which doesn't reduce the buyer's
	// price but still credits the affiliate at checkout):
	//
	//   - Buyer-facing → disc:{tag}     — pre-fills the visible
	//     Discount input + drives the price preview.
	//   - Silent affiliate → aff:{tag}  — invisible in the UI;
	//     a hidden form field carries it through checkout so
	//     the affiliate gets credited.
	//
	// Multi-conf stashing applies to both slots: a code valid for
	// vienna+nairobi auto-applies on either's checkout.
	//
	// Lookup is best-effort. An unknown / expired code falls back
	// to disc:{tag} for the landing conf (the checkout error path
	// surfaces the rejection there).
	if code := strings.TrimSpace(r.URL.Query().Get("code")); code != "" {
		disc, _ := getters.FindDiscount(ctx, code)
		stashKey := discountSessionKey
		if disc != nil && disc.DiscType == '%' && disc.Amount == 0 {
			stashKey = affiliateSessionKey
		}
		ctx.Session.Put(r.Context(), stashKey(conf.Tag), code)
		if disc != nil {
			allConfs, _ := getters.FetchConfsCached(ctx)
			if len(disc.ConfRef) > 0 {
				// Code is pinned to specific confs — stash
				// for each one in the list.
				tagByRef := make(map[string]string, len(allConfs))
				for _, c := range allConfs {
					if c != nil {
						tagByRef[c.Ref] = c.Tag
					}
				}
				for _, ref := range disc.ConfRef {
					tag := tagByRef[ref]
					if tag == "" || tag == conf.Tag {
						continue
					}
					ctx.Session.Put(r.Context(), stashKey(tag), code)
				}
			} else {
				// Universal code (no ConfRef) — stash for
				// every active event so a visitor browsing
				// multiple confs in one session still gets
				// auto-apply on each one's checkout.
				for _, c := range allConfs {
					if c == nil || !c.Active || c.Tag == conf.Tag {
						continue
					}
					ctx.Session.Put(r.Context(), stashKey(c.Tag), code)
				}
			}
		}
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	var evSpeakers types.Speakers
	evSpeakers = acceptedSpeakersForConf(ctx, conf.Tag, talks)
	sort.Sort(evSpeakers)

	soldCount := getters.SoldTixCached(ctx, conf)

	buckets, err := bucketTalks(conf, talks)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to bucket '%s' talks from Notion!! %s", conf.Tag, err.Error())
		return
	}

	days, err := talkDays(ctx, conf, talks)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to make days '%s' from talks from Notion!! %s", conf.Tag, err.Error())
		return
	}

	// Per-day schedule strip data (doors / lunch / coffee). Best-effort
	// — a fetch failure leaves AgendaDays without time strips, which the
	// template handles by collapsing to chrono-only.
	var infosByDay map[int]*types.ConfInfo
	if cis, err := getters.ListConfInfos(ctx, conf.Tag); err != nil {
		ctx.Err.Printf("/%s ListConfInfos failed (continuing): %s", conf.Tag, err)
	} else {
		infosByDay = confInfosByDay(cis)
	}
	agendaDays := buildAgendaDays(conf, talks, infosByDay)

	// Flatten AgendaDays into a single chrono-ordered slice for the
	// JSON-LD subEvent[] emission. Each day's .All is already
	// status-filtered + sorted by buildAgendaDays.
	var scheduledSessions []*types.Session
	for _, d := range agendaDays {
		if d == nil {
			continue
		}
		scheduledSessions = append(scheduledSessions, d.All...)
	}

	// Populate countdown bounds + HasAgenda for the conf_nav widget
	// + agenda section. Shallow-copy first so we don't mutate the
	// cached Conf seen by other readers (FetchConfsCached returns a
	// shared slice).
	confCopy := *conf
	confCopy.CountdownStart, confCopy.CountdownEnd = computeCountdownBounds(&confCopy, infosByDay)
	confCopy.HasAgenda = anyScheduledTalk(talks)
	conf = &confCopy

	confHotels := helpers.HotelsForConf(ctx, conf)

	currTix := findCurrTix(conf, soldCount)
	maxTix := findMaxTix(conf)

	var tixLeft uint
	if currTix == nil {
		tixLeft = 0
	} else {
		tixLeft = currTix.Max - soldCount
	}
	tmplTag := fmt.Sprintf("conf/%s.tmpl", conf.Tag)
	err = ctx.TemplateCache.ExecuteTemplate(w, tmplTag, &ConfPage{
		Conf:              conf,
		Hotels:            confHotels,
		Tix:               currTix,
		MaxTix:            maxTix,
		Sold:              soldCount,
		TixLeft:           tixLeft,
		Talks:             talks,
		EventSpeakers:     evSpeakers,
		Buckets:           buckets,
		Days:              days,
		AgendaDays:        agendaDays,
		ScheduledSessions: scheduledSessions,
		Year:              helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func getSponsorOpps() []types.CheckItem {
	return []types.CheckItem{
		{ItemID: "opp-event", ItemDesc: "Event Sponsorship"},
		{ItemID: "opp-hackathon", ItemDesc: "Hackathon Sponsorship"},
		{ItemID: "opp-workshop", ItemDesc: "Workshop Sponsorship"},
		{ItemID: "opp-happy-hour", ItemDesc: "Happy Hour / After Party"},
		{ItemID: "opp-lanyard", ItemDesc: "Lanyards / Swag"},
		{ItemID: "opp-other", ItemDesc: "Other"},
	}
}

func SponsorPage(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	confs := listConfs(w, ctx)
	if confs == nil {
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Build conf items from all active future confs
		var confItems []types.CheckItem
		for _, c := range confs {
			if !c.Active || !c.InFuture() {
				continue
			}
			confItems = append(confItems, types.CheckItem{
				ItemID:   "conf-" + c.Ref,
				ItemDesc: c.Desc + " " + c.DateDesc,
			})
		}

		err := ctx.TemplateCache.ExecuteTemplate(w, "embeds/sponsor.tmpl", &SponsorFormPage{
			Confs:       confs,
			ConfItems:   confItems,
			SponsorOpps: getSponsorOpps(),
			Year:        helpers.CurrentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page", http.StatusInternalServerError)
			ctx.Err.Printf("/sponsor ExecuteTemplate failed: %s", err.Error())
		}
		return
	case http.MethodPost:
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		name := strings.TrimSpace(r.FormValue("Name"))
		phone := strings.TrimSpace(r.FormValue("Phone"))
		email := strings.TrimSpace(r.FormValue("Email"))
		signal := strings.TrimSpace(r.FormValue("Signal"))
		telegram := strings.TrimSpace(r.FormValue("Telegram"))
		contactAt := strings.TrimSpace(r.FormValue("ContactAt"))
		org := strings.TrimSpace(r.FormValue("Org"))
		orgSite := strings.TrimSpace(r.FormValue("OrgSite"))
		orgTwitter := types.ParseTwitter(r.FormValue("OrgTwitter")).Handle
		orgNostr := strings.TrimSpace(r.FormValue("OrgNostr"))
		budget := strings.TrimSpace(r.FormValue("Budget"))
		discoveredVia := strings.TrimSpace(r.FormValue("DiscoveredVia"))
		comments := strings.TrimSpace(r.FormValue("Comments"))
		captcha := strings.TrimSpace(r.FormValue("Captcha"))

		if captcha != "5" {
			w.Write([]byte(helpers.ErrApp("Incorrect captcha. The answer is 5.", "sponsors")))
			return
		}

		if email == "" || !strings.Contains(email, "@") {
			w.Write([]byte(helpers.ErrApp("Please provide a valid email address.", "sponsors")))
			return
		}

		if name == "" || org == "" {
			w.Write([]byte(helpers.ErrApp("Name and organization are required.", "sponsors")))
			return
		}

		// Collect selected conferences
		var selectedConfs []string
		for key := range r.PostForm {
			if strings.HasPrefix(key, "conf-") {
				confRef := strings.TrimPrefix(key, "conf-")
				for _, c := range confs {
					if c.Ref == confRef {
						selectedConfs = append(selectedConfs, c.Desc+" "+c.DateDesc)
						break
					}
				}
			}
		}

		// Collect selected sponsor opps
		var selectedOpps []string
		for _, opp := range getSponsorOpps() {
			if r.FormValue(opp.ItemID) != "" {
				selectedOpps = append(selectedOpps, opp.ItemDesc)
			}
		}

		htmlBody := fmt.Sprintf(
			"<h3>Sponsor Inquiry</h3>"+
				"<p><strong>Name:</strong> %s</p>"+
				"<p><strong>Email:</strong> %s</p>"+
				"<p><strong>Phone:</strong> %s</p>"+
				"<p><strong>Signal:</strong> %s</p>"+
				"<p><strong>Telegram:</strong> %s</p>"+
				"<p><strong>Best way to contact:</strong> %s</p>"+
				"<hr/>"+
				"<p><strong>Organization:</strong> %s</p>"+
				"<p><strong>Website:</strong> %s</p>"+
				"<p><strong>X:</strong> %s</p>"+
				"<p><strong>Nostr:</strong> %s</p>"+
				"<hr/>"+
				"<p><strong>Budget:</strong> %s</p>"+
				"<p><strong>Events:</strong> %s</p>"+
				"<p><strong>Interested in:</strong> %s</p>"+
				"<p><strong>Discovered via:</strong> %s</p>"+
				"<hr/>"+
				"<p><strong>Comments:</strong></p><p>%s</p>",
			template.HTMLEscapeString(name), template.HTMLEscapeString(email), template.HTMLEscapeString(phone), template.HTMLEscapeString(signal), template.HTMLEscapeString(telegram), template.HTMLEscapeString(contactAt),
			template.HTMLEscapeString(org), template.HTMLEscapeString(orgSite), template.HTMLEscapeString(orgTwitter), template.HTMLEscapeString(orgNostr),
			template.HTMLEscapeString(budget), template.HTMLEscapeString(strings.Join(selectedConfs, ", ")), template.HTMLEscapeString(strings.Join(selectedOpps, ", ")),
			template.HTMLEscapeString(discoveredVia), template.HTMLEscapeString(comments))

		textBody := fmt.Sprintf(
			"Sponsor Inquiry\n\nName: %s\nEmail: %s\nPhone: %s\nSignal: %s\nTelegram: %s\nBest way to contact: %s\n\n"+
				"Organization: %s\nWebsite: %s\nX: %s\nNostr: %s\n\n"+
				"Budget: %s\nEvents: %s\nInterested in: %s\nDiscovered via: %s\n\nComments:\n%s",
			name, email, phone, signal, telegram, contactAt,
			org, orgSite, orgTwitter, orgNostr,
			budget, strings.Join(selectedConfs, ", "), strings.Join(selectedOpps, ", "),
			discoveredVia, comments)

		mail := &emails.Mail{
			JobKey:   fmt.Sprintf("sponsor-%s-%d", email, time.Now().Unix()),
			Email:    "sponsor@btcpp.dev",
			ReplyTo:  email,
			Title:    fmt.Sprintf("Sponsor Inquiry: %s (%s)", org, name),
			SendAt:   time.Now(),
			HTMLBody: []byte(htmlBody),
			TextBody: []byte(textBody),
		}

		err := emails.ComposeAndSendMail(ctx, mail)
		if err != nil {
			ctx.Err.Printf("/sponsor failed to send email: %s", err.Error())
			w.Write([]byte(helpers.ErrApp("Unable to send your inquiry. Please try again.", "sponsors")))
			return
		}

		// Send a copy to the submitter
		copyMail := &emails.Mail{
			JobKey:   fmt.Sprintf("sponsor-copy-%s-%d", email, time.Now().Unix()),
			Email:    email,
			ReplyTo:  "sponsor@btcpp.dev",
			Title:    fmt.Sprintf("Your Sponsor Inquiry: %s", org),
			SendAt:   time.Now(),
			HTMLBody: []byte("<p>Thanks for your interest in sponsoring bitcoin++! Here's a copy of your inquiry:</p><hr/>" + htmlBody),
			TextBody: []byte("Thanks for your interest in sponsoring bitcoin++! Here's a copy of your inquiry:\n\n" + textBody),
		}

		err = emails.ComposeAndSendMail(ctx, copyMail)
		if err != nil {
			ctx.Err.Printf("/sponsor failed to send copy to %s: %s", email, err.Error())
			// Don't fail the whole submission for a copy failure
		}

		ctx.Infos.Printf("Sponsor inquiry from %s (%s) at %s", name, email, org)
		w.Write([]byte(helpers.SuccessApp("Your sponsor inquiry has been sent! We'll get back to you soon.")))
		return
	}
}

func ContactPage(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	switch r.Method {
	case http.MethodGet:
		err := ctx.TemplateCache.ExecuteTemplate(w, "embeds/contact.tmpl", &struct{ Year uint }{
			Year: helpers.CurrentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page", http.StatusInternalServerError)
			ctx.Err.Printf("/contact ExecuteTemplate failed: %s", err.Error())
		}
		return
	case http.MethodPost:
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}

		name := r.FormValue("Name")
		phone := r.FormValue("Phone")
		email := r.FormValue("Email")
		contactAt := r.FormValue("ContactAt")
		message := r.FormValue("Message")
		captcha := r.FormValue("Captcha")

		if captcha != "5" {
			w.Write([]byte(helpers.ErrApp("Incorrect captcha. The answer is 5.", "hello")))
			return
		}

		if email == "" || !strings.Contains(email, "@") {
			w.Write([]byte(helpers.ErrApp("Please provide a valid email address.", "hello")))
			return
		}

		if name == "" || message == "" {
			w.Write([]byte(helpers.ErrApp("Name and message are required.", "hello")))
			return
		}

		htmlBody := fmt.Sprintf(
			"<h3>Contact Form Submission</h3>"+
				"<p><strong>Name:</strong> %s</p>"+
				"<p><strong>Email:</strong> %s</p>"+
				"<p><strong>Phone:</strong> %s</p>"+
				"<p><strong>Best way to contact:</strong> %s</p>"+
				"<hr/>"+
				"<p>%s</p>",
			template.HTMLEscapeString(name), template.HTMLEscapeString(email), template.HTMLEscapeString(phone), template.HTMLEscapeString(contactAt), template.HTMLEscapeString(message))

		textBody := fmt.Sprintf(
			"Contact Form Submission\n\nName: %s\nEmail: %s\nPhone: %s\nBest way to contact: %s\n\n%s",
			name, email, phone, contactAt, message)

		mail := &emails.Mail{
			JobKey:   fmt.Sprintf("contact-%s-%d", email, time.Now().Unix()),
			Email:    "hello@btcpp.dev",
			ReplyTo:  email,
			Title:    fmt.Sprintf("Contact Form: %s", name),
			SendAt:   time.Now(),
			HTMLBody: []byte(htmlBody),
			TextBody: []byte(textBody),
		}

		err := emails.ComposeAndSendMail(ctx, mail)
		if err != nil {
			ctx.Err.Printf("/contact failed to send email: %s", err.Error())
			w.Write([]byte(helpers.ErrApp("Unable to send your message. Please try again.", "hello")))
			return
		}

		ctx.Infos.Printf("Contact form submitted by %s (%s)", name, email)
		w.Write([]byte(helpers.SuccessApp("Your message has been sent! We'll get back to you soon.")))
		return
	}
}

func RenderPage(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, page string) {

	confList := listConfs(w, ctx)
	if confList == nil {
		return
	}

	// Single Notion call (empty tag = all rows) so the homepage's
	// countdown widget on each conf card has the same per-day-strip
	// bounds the per-conf page uses. Bucket by tag → day for cheap
	// per-conf lookup.
	infosByTag := map[string]map[int]*types.ConfInfo{}
	if cis, err := getters.ListConfInfos(ctx, ""); err != nil {
		ctx.Err.Printf("/%s ListConfInfos for index countdown (continuing): %s", page, err)
	} else {
		for _, ci := range cis {
			if ci == nil || ci.Day < 1 || ci.ConfTag == "" {
				continue
			}
			m, ok := infosByTag[ci.ConfTag]
			if !ok {
				m = map[int]*types.ConfInfo{}
				infosByTag[ci.ConfTag] = m
			}
			m[ci.Day] = ci
		}
	}

	// Shallow-copy each conf before populating the runtime-only
	// CountdownStart/End fields so the shared FetchConfsCached
	// slice stays untouched.
	enriched := make([]*types.Conf, 0, len(confList))
	for _, c := range confList {
		if c == nil {
			continue
		}
		copy := *c
		copy.CountdownStart, copy.CountdownEnd = computeCountdownBounds(&copy, infosByTag[copy.Tag])
		enriched = append(enriched, &copy)
	}

	data := struct {
		Confs []*types.Conf
		Year  uint
	}{
		Confs: enriched,
		Year:  helpers.CurrentYear(),
	}

	template := fmt.Sprintf("embeds/%s.tmpl", page)
	err := ctx.TemplateCache.ExecuteTemplate(w, template, &data)

	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s ExecuteTemplate failed ! %s", page, err.Error())
	}
}

type TicketTmpl struct {
	QRCodeURI string
	Domain    string
	CSS       string
	Type      string
	Conf      *types.Conf
}

func Ticket(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	ticket := params["ticket"]

	tixType, _ := helpers.GetSessionKey("type", r)
	confRef, _ := helpers.GetSessionKey("conf", r)

	/* make it pretty */
	if tixType == "genpop" {
		tixType = "general"
	}

	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/ticket-pdf unable to load confs! %s", err)
		return
	}

	conf := helpers.FindConfByRef(confs, confRef)
	if conf == nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/ticket-pdf unable to find conf! %s", confRef)
		return
	}

	/* URL */
	url := fmt.Sprintf("%s/check-in/%s", ctx.Env.GetURI(), ticket)

	/* Turn the URL into a QR code! */
	qrpng, err := qrcode.Encode(url, qrcode.Medium, 256)
	qrcode := base64.StdEncoding.EncodeToString(qrpng)

	/* Turn the QR code into a data URI! */
	dataURI := fmt.Sprintf("data:image/png;base64,%s", qrcode)

	tix := &TicketTmpl{
		QRCodeURI: dataURI,
		CSS:       helpers.MiniCss(),
		Domain:    ctx.Env.GetDomain(),
		Type:      tixType,
		Conf:      conf,
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "emails/ticket.tmpl", tix)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Infos.Printf("/ticket-pdf ExecuteTemplate failed ! %s", err.Error())
	}
}

// TicketPDF renders the same ticket HTML view as /ticket/{ref} but pipes
// it through headless Chrome to produce a downloadable PDF. Used by the
// dashboard "Download ticket" button so users get a saveable file
// instead of a browser tab they have to print themselves.
func TicketPDF(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	ticket := mux.Vars(r)["ticket"]
	tixType := r.URL.Query().Get("type")
	confRef := r.URL.Query().Get("conf")

	// Build the internal URL chrome will fetch — same URL pattern as the
	// HTML view, just with auto-print disabled (the HTML page is public,
	// no auth needed).
	q := url.Values{}
	if tixType != "" {
		q.Set("type", tixType)
	}
	if confRef != "" {
		q.Set("conf", confRef)
	}
	internalURL := fmt.Sprintf("%s/ticket/%s?%s", ctx.Env.GetURI(), ticket, q.Encode())

	pdfBytes, err := helpers.BuildChromePdf(ctx, &helpers.PDFPage{
		URL:    internalURL,
		Width:  8.5,
		Height: 11,
	})
	if err != nil {
		http.Error(w, "Could not generate ticket PDF", http.StatusInternalServerError)
		ctx.Err.Printf("/ticket/%s/pdf chromedp failed: %s", ticket, err)
		return
	}

	// Friendly filename: ticket-{conf-tag}-{first8ofref}.pdf
	confName := "btcpp"
	if confRef != "" {
		if confs, _ := getters.FetchConfsCached(ctx); confs != nil {
			for _, c := range confs {
				if c != nil && c.Ref == confRef && c.Tag != "" {
					confName = c.Tag
					break
				}
			}
		}
	}
	shortRef := ticket
	if len(shortRef) > 8 {
		shortRef = shortRef[:8]
	}
	filename := fmt.Sprintf("ticket-%s-%s.pdf", confName, shortRef)

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(pdfBytes)))
	w.Write(pdfBytes)
}

// SendCals fans the self-hosted ICS calendar pipeline across every
// scheduled talk for a conf. Replaces the previous Google Calendar
// API call with internal RFC-5545 generation; CalNotif is now the
// "UID:Sequence:Hashbytes" triple maintained by DispatchTalkICSForTalk.
//
// Idempotent: a re-click that doesn't change any talk's start/end/
// title hash will skip emails entirely (no SEQUENCE bump, no
// duplicate invitation in recipients' calendars). Re-running after
// a schedule edit fans out an UPDATE with seq+1.
func SendCals(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil || conf == nil {
		handle404(w, r, ctx)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	for _, talk := range talks {
		if talk.Sched == nil || talk.Sched.End == nil {
			ctx.Err.Printf("Can't send cals for %s talk: no end time??", talk.Name)
			continue
		}
		if err := DispatchTalkICSForTalk(ctx, talk, conf, kindRequest, false); err != nil {
			ctx.Err.Printf("send cals %q: %s", talk.Name, err)
		}
	}
}

func CheckIn(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	switch r.Method {
	case http.MethodGet:
		CheckInGet(w, r, ctx)
		return
	case http.MethodPost:
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		pin := r.Form.Get("pin")
		if pin != ctx.Env.RegistryPin {
			w.WriteHeader(http.StatusBadRequest)
			err := ctx.TemplateCache.ExecuteTemplate(w, "checkin.tmpl", &CheckInPage{
				NeedsPin: true,
				Msg:      "Wrong pin",
				Year:     helpers.CurrentYear(),
			})
			if err != nil {
				http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
				ctx.Err.Printf("/check-in ExecuteTemplate failed ! %s", err.Error())
				return
			}
			ctx.Err.Printf("/check-in wrong pin submitted! %s", pin)
			return
		}

		/* Set pin?? */
		ctx.Session.Put(r.Context(), "pin", pin)
		CheckInGet(w, r, ctx)
	}
}

func CheckInGet(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* Check for logged in */
	pin := ctx.Session.GetString(r.Context(), "pin")

	if pin == "" {
		w.Header().Set("x-missing-field", "pin")
		w.WriteHeader(http.StatusBadRequest)
		err := ctx.TemplateCache.ExecuteTemplate(w, "checkin.tmpl", &CheckInPage{
			NeedsPin: true,
			Year:     helpers.CurrentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/check-in ExecuteTemplate failed ! %s", err.Error())
		}
		return
	}

	if pin != ctx.Env.RegistryPin {
		w.WriteHeader(http.StatusUnauthorized)
		err := ctx.TemplateCache.ExecuteTemplate(w, "checkin.tmpl", &CheckInPage{
			Msg:  "Wrong registration PIN",
			Year: helpers.CurrentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/check-in ExecuteTemplate failed ! %s", err.Error())
		}
		return
	}

	params := mux.Vars(r)
	ticket := params["ticket"]

	tix_type, ok, err := getters.CheckIn(ctx, ticket)
	if !ok && err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to check-in %s: %s", ticket, err.Error())
		return
	}

	var msg string
	if err != nil {
		msg = err.Error()
		ctx.Infos.Println("check-in problem:", msg)
	}
	err = ctx.TemplateCache.ExecuteTemplate(w, "checkin.tmpl", &CheckInPage{
		TicketType: tix_type,
		Msg:        msg,
		Year:       helpers.CurrentYear(),
	})

	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/check-in ExecuteTemplate failed ! %s", err.Error())
	}
}

func ticketMatch(tickets []string, desc string) bool {
	for _, tix := range tickets {
		if strings.Contains(desc, tix) {
			return true
		}
	}

	return false
}

func computeHash(key, id string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(id))
	return hex.EncodeToString(mac.Sum(nil))
}

func validHash(key, id, msgMAC string) bool {
	actual := computeHash(key, id)
	return hmac.Equal([]byte(msgMAC), []byte(actual))
}

var decoder = newFormDecoder()

func OpenNodeCallback(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	limitRequestBody(w, r, maxWebhookBodyBytes)
	err := r.ParseForm()
	if err != nil {
		ctx.Err.Printf("Error reading request body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var ev ChargeEvent
	decoder.IgnoreUnknownKeys(true)
	err = decoder.Decode(&ev, r.PostForm)
	if err != nil {
		ctx.Err.Printf("Unable to unmarshal: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	/* Check the hashed order is ok */
	if !validHash(ctx.Env.OpenNode.Key, ev.ID, ev.HashedOrder) {
		ctx.Err.Printf("Invalid request from opennode %s %s", ev.ID, ev.HashedOrder)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	/* Go get the actual event data */
	charge, err := GetCharge(ctx, ev.ID)
	if err != nil {
		ctx.Err.Printf("Unable to fetch charge: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if ev.Status != "paid" {
		ctx.Infos.Printf("User did not complete charge. charge-id: %s status: %s email: %s conf-ref: %s", ev.ID, ev.Status, charge.Metadata.Email, charge.Metadata.ConfRef)
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx.Infos.Println("opennode charge!", charge)
	entry := types.Entry{
		ID:          charge.ID,
		ConfRef:     charge.Metadata.ConfRef,
		Total:       int64(charge.FiatVal * 100),
		Currency:    charge.Metadata.Currency,
		Created:     charge.CreatedAt,
		Email:       charge.Metadata.Email,
		DiscountRef: charge.Metadata.DiscountRef,
	}

	if err != nil {
		ctx.Err.Printf("Failed to fetch charge %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	tixType := "genpop"
	if charge.Metadata.TixLocal {
		tixType = "local"
	}
	for i := 0; i < int(charge.Metadata.Quantity); i++ {
		item := types.Item{
			Total: int64(charge.FiatVal * 100),
			Desc:  charge.Description,
			Type:  tixType,
		}
		entry.Items = append(entry.Items, item)
	}

	if len(entry.Items) == 0 {
		ctx.Infos.Println("No valid items bought")
		w.WriteHeader(http.StatusOK)
		return
	}

	err = getters.AddTickets(ctx, &entry, "opennode")

	if err != nil {
		ctx.Err.Printf("!!! Unable to add ticket %s: %v", err, entry)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	/* Add to mailing list + schedule mails */
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("opennode callback: unable to load confs! %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	conf := helpers.FindConfByRef(confs, entry.ConfRef)
	err = missives.NewTicketSub(ctx, entry.Email, conf.Tag, tixType, charge.Metadata.Subscribe)

	if err != nil {
		ctx.Err.Printf("!!! Unable to subscribe to newsletter %s: %v", err, entry)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Increment discount usage counter
	if entry.DiscountRef != "" {
		err = getters.IncrementDiscountUses(ctx, entry.DiscountRef, uint(len(entry.Items)))
		if err != nil {
			ctx.Err.Printf("Failed to increment discount uses: %s", err)
		}
		preDiscountStr := ""
		if charge.Metadata.PreDiscountCents > 0 {
			preDiscountStr = strconv.FormatInt(charge.Metadata.PreDiscountCents, 10)
		}
		recordAffiliateUsageFromCheckout(ctx, conf, &entry, preDiscountStr)
	}

	ctx.Infos.Println("Added ticket!", entry.ID)
	w.WriteHeader(http.StatusOK)
}

func HandleTixSelection(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	tixSlug := params["tix"]

	if tixSlug == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Both fiat and btc go to the same checkout page
	http.Redirect(w, r, fmt.Sprintf("/tix/%s/checkout", tixSlug), http.StatusSeeOther)
}

func getPrice(pricestr string) (uint, error) {
	price, err := strconv.ParseUint(pricestr, 10, 32)
	return uint(price), err
}

func HandleDiscount(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	tixSlug := params["tix"]

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	discountCode := r.Form.Get("Discount")
	affiliateCode := r.Form.Get("AffiliateCode")
	discountPrice, err := getPrice(r.Form.Get("DiscountPrice"))
	if err != nil {
		ctx.Err.Printf("/tix/%s/apply-discount massively blew up: %s", tixSlug, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if tixSlug == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	conf, tix, tixPrice, _, err := determineTixPrice(ctx, tixSlug)
	if err != nil {
		/* FIXME: have this return an error message, not a status code error */
		ctx.Err.Printf("/tix/%s/apply-discount unable to determine tix price: %s", tixSlug, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Effective code: typed wins over silent affiliate. The HMAC
	// + recorded discount-ref both follow the effective code.
	effectiveCode := effectiveDiscountCode(discountCode, affiliateCode)
	var discountRef string
	errStr := ""
	if effectiveCode != "" {
		var discount *types.DiscountCode
		discountPrice, discount, err = getters.CalcDiscount(ctx, conf.Ref, effectiveCode, tixPrice, 1)
		if discount != nil {
			discountRef = discount.Ref
		}
		if err != nil {
			ctx.Err.Printf("/tix/%s/apply-discount discount not available: %s", tixSlug, err)
			// Silent affiliate codes that fail validation
			// stay invisible — drop them and proceed at full
			// price rather than surfacing an error the buyer
			// didn't trigger.
			if effectiveCode == affiliateCode && discountCode == "" {
				affiliateCode = ""
				discountPrice = tixPrice
			} else {
				errStr = err.Error()
			}
		}
	} else {
		discountPrice = tixPrice
	}

	w.Header().Set("Content-Type", "text/html")
	err = ctx.TemplateCache.ExecuteTemplate(w, "tix_details.tmpl", &TixFormPage{
		Conf:          conf,
		Tix:           tix,
		TixSlug:       tixSlug,
		TixPrice:      tixPrice,
		Discount:      discountCode,
		AffiliateCode: affiliateCode,
		DiscountPrice: discountPrice,
		DiscountRef:   discountRef,
		Err:           errStr,
		HMAC:          calcTixHMAC(ctx, conf, tixPrice, discountPrice, effectiveCode),
		Count:         uint(1),
		Year:          helpers.CurrentYear(),
	})

	if err != nil {
		http.Error(w, "Unable to load template, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/tix/%s/apply-discount templ exec failed %s", tixSlug, err.Error())
		return
	}
}

func HandleCheckout(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	tixSlug := params["tix"]

	if tixSlug == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	conf, tix, tixPrice, processBTC, err := determineTixPrice(ctx, tixSlug)
	if err != nil {
		ctx.Err.Printf("/tix/%s/checkout unable to determine tix price: %s", tixSlug, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Determine payment method from the slug
	paymentMethod := "fiat"
	if processBTC {
		paymentMethod = "btc"
	}

	switch r.Method {
	case http.MethodGet:

		// `?q=` on the checkout URL takes precedence (admin debug
		// flow); otherwise fall back to two parallel session
		// slots that get populated when a visitor lands via a
		// shared link:
		//
		//   - disc:{tag}  → buyer-facing discount code, pre-fills
		//     the visible Discount input + drives the price.
		//   - aff:{tag}   → silent (`%0`) affiliate referral; the
		//     visible Discount stays empty and no "saved $X" line
		//     shows, but a hidden AffiliateCode field carries the
		//     code through the form so checkout still credits the
		//     affiliate.
		//
		// The visible code wins at submit time — see HandleDiscount
		// + the POST branch's effective-code resolution.
		discountCode, _ := helpers.GetSessionKey("q", r)
		if discountCode == "" {
			discountCode = ctx.Session.GetString(r.Context(), discountSessionKey(conf.Tag))
		}
		affiliateCode := ctx.Session.GetString(r.Context(), affiliateSessionKey(conf.Tag))

		discountPrice := tixPrice
		var errStr string
		var discountRef string
		// The "effective" code for this render is the visible one
		// when present, else the silent affiliate one. CalcDiscount
		// runs against either — both produce a valid, finalized
		// price (a `%0` affiliate just leaves the price unchanged).
		effective := discountCode
		if effective == "" {
			effective = affiliateCode
		}
		if effective != "" {
			var discount *types.DiscountCode
			discountPrice, discount, err = getters.CalcDiscount(ctx, conf.Ref, effective, tixPrice, 1)
			if err != nil {
				ctx.Err.Printf("/tix/%s/checkout discount not available: %s", tixSlug, err)
				// Silent affiliate codes that fail validation
				// shouldn't show a buyer-facing error — drop
				// the affiliate ref and proceed at full price.
				if effective == affiliateCode && discountCode == "" {
					affiliateCode = ""
					discountPrice = tixPrice
				} else {
					errStr = err.Error()
				}
			}
			if discount != nil {
				discountRef = discount.Ref
			}
		}
		err = ctx.TemplateCache.ExecuteTemplate(w, "collect-email.tmpl", &TixFormPage{
			Conf:          conf,
			Tix:           tix,
			TixSlug:       tixSlug,
			TixPrice:      tixPrice,
			Discount:      discountCode,
			AffiliateCode: affiliateCode,
			DiscountPrice: discountPrice,
			DiscountRef:   discountRef,
			Err:           errStr,
			HMAC:          calcTixHMAC(ctx, conf, tixPrice, discountPrice, effective),
			Count:         uint(1),
			Year:          helpers.CurrentYear(),
			PaymentMethod: paymentMethod,
		})
		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/tix/%s/checkout templ exec failed %s", tixSlug, err.Error())
			return
		}
		return
	case http.MethodPost:
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		dec := newFormDecoder()
		var form types.TixForm
		err = dec.Decode(&form, r.PostForm)
		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/tix/%s/checkout unable to decode form %s", tixSlug, err)
			return
		}

		if form.Email == "" || form.Count < 1 {
			http.Redirect(w, r, fmt.Sprintf("/tix/%s/checkout", tixSlug), http.StatusSeeOther)
			return
		}

		// Resolve the effective discount code: the typed one wins
		// over the silent affiliate carry-through. Anyone typing a
		// different code at checkout drops the prior affiliate's
		// credit because the discount-ref written to Stripe
		// metadata follows whichever code is actually applied.
		effectiveCode := effectiveDiscountCode(form.Discount, form.AffiliateCode)

		/*  Validate HMAC over the effective code (matches the
		 *  HMAC computed on render, which signed over the same
		 *  effective code — typed vs. silent — that resolves on
		 *  this submit). */
		expectedHMAC := calcTixHMAC(ctx, conf, tixPrice, form.DiscountPrice, effectiveCode)
		if !hmac.Equal([]byte(expectedHMAC), []byte(form.HMAC)) {
			ctx.Err.Printf("/tix/%s/checkout hmac mismatch. %s != %s", tixSlug, expectedHMAC, form.HMAC)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Webhook side reads form.DiscountRef to decide whether
		// to fire RecordAffiliateUsage. Make sure it points at
		// the EFFECTIVE code's row, not whatever the GET render
		// originally stamped (a buyer who switched codes mid-
		// checkout should produce a discount-ref tied to their
		// chosen code, not to the silent affiliate they walked in
		// with).
		if effectiveCode != "" {
			if disc, derr := getters.FindDiscount(ctx, effectiveCode); derr == nil && disc != nil {
				form.DiscountRef = disc.Ref
			}
		} else {
			form.DiscountRef = ""
		}
		// Keep form.Discount in sync with the effective code so
		// downstream Stripe metadata + entry.DiscountRef agree.
		form.Discount = effectiveCode

		isLocal := tixPrice == tix.Local

		if form.PaymentMethod == "btc" {
			OpenNodeInit(w, r, ctx, conf, tix, form.DiscountPrice, tixPrice, &form, isLocal)
		} else {
			StripeInitWithDiscount(w, r, ctx, conf, tix, form.DiscountPrice, tixPrice, &form, isLocal)
		}
		return
	default:
		http.NotFound(w, r)
		return
	}
}

func OpenNodeInit(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, tix *types.ConfTicket, tixPrice, preDiscountPrice uint, tixForm *types.TixForm, isLocal bool) {
	payment, err := getters.InitOpenNodeCheckout(ctx, tixPrice, preDiscountPrice, tix, conf, isLocal, tixForm.Count, tixForm.Email, tixForm.DiscountRef, tixForm.Subscribe)

	if err != nil {
		http.Error(w, "unable to init btc payment", http.StatusInternalServerError)
		ctx.Err.Printf("opennode payment init failed: %s", err.Error())
		return
	}

	/* FIXME: v2: implement on-site btc checkout */
	/* for now we go ahead and just redirect to opennode, see you latrrr */
	http.Redirect(w, r, payment.HostedCheckoutURL, http.StatusSeeOther)
}

func StripeInitWithDiscount(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, tix *types.ConfTicket, tixPrice, preDiscountPrice uint, form *types.TixForm, isLocal bool) {
	domain := ctx.Env.GetURI()
	priceAsCents := int64(tixPrice * 100)
	confDesc := fmt.Sprintf("%d ticket(s) for %s", form.Count, conf.Desc)
	metadata := make(map[string]string)
	metadata["conf-tag"] = conf.Tag
	metadata["conf-ref"] = conf.Ref
	metadata["tix-id"] = tix.ID
	metadata["discount-ref"] = form.DiscountRef
	metadata["subscribe"] = fmt.Sprintf("%t", form.Subscribe)
	// Pre-discount per-ticket price in cents — webhook reads this
	// to compute originalCents (× ticket count) for the affiliate
	// math. Sourced from the original tier price (USD / BTC / Local)
	// the buyer selected, NOT the `tixPrice` arg, because callers pass
	// tixPrice = form.DiscountPrice (the *post*-discount value).
	metadata["pre-discount-cents"] = strconv.FormatInt(int64(preDiscountPrice)*100, 10)
	if isLocal {
		metadata["tix-local"] = "yes"
	}
	params := &stripe.CheckoutSessionParams{
		CustomerEmail: stripe.String(form.Email),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Description: stripe.String(confDesc),
						Name:        stripe.String(conf.Desc),
						Metadata:    metadata,
					},
					UnitAmount: stripe.Int64(priceAsCents),
					Currency:   stripe.String(tix.Currency),
				},
				Quantity: stripe.Int64(int64(form.Count)),
			}},
		Metadata:     metadata,
		Mode:         stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL:   stripe.String(domain + "/" + conf.Tag + "/success"),
		CancelURL:    stripe.String(domain + "/" + conf.Tag),
		AutomaticTax: &stripe.CheckoutSessionAutomaticTaxParams{Enabled: stripe.Bool(true)},
	}

	s, err := session.New(params)
	if err != nil {
		ctx.Err.Printf("!!! Unable to create stripe session: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, s.URL, http.StatusSeeOther)
}

func StripeInit(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, tix *types.ConfTicket, tixPrice uint) {

	domain := ctx.Env.GetURI()
	priceAsCents := int64(tixPrice * 100)
	confDesc := fmt.Sprintf("1 ticket for the %s", conf.Desc)
	metadata := make(map[string]string)
	metadata["conf-tag"] = conf.Tag
	metadata["conf-ref"] = conf.Ref
	metadata["tix-id"] = tix.ID
	metadata["pre-discount-cents"] = strconv.FormatInt(priceAsCents, 10)
	if tixPrice == tix.Local {
		metadata["tix-local"] = "yes"
	}
	params := &stripe.CheckoutSessionParams{
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Description: stripe.String(confDesc),
						Name:        stripe.String(conf.Desc),
						Metadata:    metadata,
					},
					UnitAmount: stripe.Int64(priceAsCents),
					Currency:   stripe.String(tix.Currency),
				},
				Quantity: stripe.Int64(1),
			}},
		Metadata:            metadata,
		Mode:                stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL:          stripe.String(domain + "/" + conf.Tag + "/success"),
		CancelURL:           stripe.String(domain + "/" + conf.Tag),
		AutomaticTax:        &stripe.CheckoutSessionAutomaticTaxParams{Enabled: stripe.Bool(true)},
		AllowPromotionCodes: stripe.Bool(true),
	}

	s, err := session.New(params)
	if err != nil {
		ctx.Err.Printf("!!! Unable to create stripe session: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, s.URL, http.StatusSeeOther)
}

func StripeCallback(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	const MaxBodyBytes = int64(65536)
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		ctx.Err.Printf("Error reading request body: %v", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	event, err := webhook.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), ctx.Env.StripeEndpointSec)

	if err != nil {
		ctx.Err.Println("Error verifying webhook sig", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch event.Type {
	case "checkout.session.completed":
		var checkout stripe.CheckoutSession
		err := json.Unmarshal(event.Data.Raw, &checkout)
		if err != nil {
			ctx.Err.Printf("Error parsing webhook JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		confRef, ok := checkout.Metadata["conf-ref"]
		if !ok {
			ctx.Infos.Println("No conf-ref present")
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		confs, err := getters.FetchConfsCached(ctx)
		if err != nil {
			ctx.Err.Printf("Stripe callback: unable to load confs! %s", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conf := helpers.FindConfByRef(confs, confRef)
		if conf == nil {
			ctx.Err.Printf("Couldn't find conf %s", confRef)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		discountRef, _ := checkout.Metadata["discount-ref"]

		entry := types.Entry{
			ID:          checkout.ID,
			ConfRef:     conf.Ref,
			Total:       checkout.AmountTotal,
			Currency:    string(checkout.Currency),
			Created:     time.Unix(checkout.Created, 0).UTC(),
			Email:       checkout.CustomerDetails.Email,
			DiscountRef: discountRef,
		}

		itemParams := &stripe.CheckoutSessionListLineItemsParams{
			Session: stripe.String(checkout.ID),
		}

		_, isLocal := checkout.Metadata["tix-local"]
		var tixType string
		if isLocal {
			tixType = "local"
		} else {
			tixType = "genpop"
		}
		items := session.ListLineItems(itemParams)
		for items.Next() {
			si := items.LineItem()
			var i int64
			for i = 0; i < si.Quantity; i++ {
				item := types.Item{
					Total: si.AmountTotal,
					Desc:  si.Description,
					Type:  tixType,
				}
				entry.Items = append(entry.Items, item)
			}
		}

		if err := items.Err(); err != nil {
			ctx.Err.Println(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		if len(entry.Items) == 0 {
			ctx.Infos.Println("No valid items bought")
			w.WriteHeader(http.StatusOK)
			return
		}

		err = getters.AddTickets(ctx, &entry, "stripe")

		if err != nil {
			ctx.Err.Printf("!!! Unable to add ticket: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ctx.Infos.Printf("Added %d tickets!!", len(entry.Items))

		// Increment discount usage counter
		if entry.DiscountRef != "" {
			err = getters.IncrementDiscountUses(ctx, entry.DiscountRef, uint(len(entry.Items)))
			if err != nil {
				ctx.Err.Printf("Failed to increment discount uses: %s", err)
			}
			recordAffiliateUsageFromCheckout(ctx, conf, &entry, checkout.Metadata["pre-discount-cents"])
		}

		/* Add to mailing list + send mails */
		err = missives.NewTicketSub(ctx, entry.Email, conf.Tag, tixType, false)

		if err != nil {
			ctx.Err.Printf("!!! Unable to subscribe to newsletter %s: %v", err, entry)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

	default:
		ctx.Infos.Printf("Unhandled event type: %s", event.Type)
	}

	w.WriteHeader(http.StatusOK)
}

type EmailForm struct {
	Email string
}

func RenderFindShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	switch r.Method {
	case http.MethodGet:
		err := ctx.TemplateCache.ExecuteTemplate(w, "volunteers/findshift.tmpl", &VolShiftPage{
			Year: helpers.CurrentYear(),
		})

		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/volunteers/findshift ExecuteTemplate failed ! %s", err.Error())
			return
		}
	case http.MethodPost:
		limitRequestBody(w, r, maxFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		dec := newFormDecoder()
		var form EmailForm
		err := dec.Decode(&form, r.PostForm)
		if err != nil {
			ctx.Err.Printf("/vols/shift unable to decode email form %s", err)
			w.Write([]byte(helpers.ErrVolApp("Unable to send you email link.")))
			return
		}

		_, err = emails.OnlyForLogin(ctx, form.Email)
		if err != nil {
			http.Error(w, "Unable to send login link via email", http.StatusInternalServerError)
			ctx.Err.Printf("/volunteers/findshift onlyforvollogin failed ! %s", err.Error())
			return
		}

		/* We redirect to home on success */
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

func calcStats(apps []*types.Volunteer) *ApplicationStats {

	pending, accepted, totalShifts := 0, 0, 0
	for _, app := range apps {
		switch app.Status {
		case "Applied":
		case "PendingShifts":
		case "Waitlist":
			pending += 1
		case "Scheduled":
			accepted += 1
		}
		totalShifts += len(app.WorkShifts)
	}

	return &ApplicationStats{
		Applied:     len(apps),
		Pending:     pending,
		Accepted:    accepted,
		TotalShifts: totalShifts,
	}
}

func validateVolEmail(r *http.Request, ctx *config.AppContext) (string, string, error) {
	encodedHMAC := r.URL.Query().Get("hr")
	encodedEmail := r.URL.Query().Get("em")

	if encodedHMAC == "" || encodedEmail == "" {
		return "", "", fmt.Errorf("missing credentials")
	}

	emailval, err := base64.RawURLEncoding.DecodeString(encodedEmail)
	if err != nil {
		return "", "", err
	}

	hashResult, err := base64.RawURLEncoding.DecodeString(encodedHMAC)
	if err != nil {
		return "", "", err
	}
	email := string(emailval)
	hmacVal := string(hashResult)

	if !helpers.VerifyEmailHMAC(ctx, hmacVal, email) {
		return "", "", fmt.Errorf("invalid HMAC")
	}

	return email, encodedHMAC, nil
}

func VolunteerShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* We put a hash + email in the link */
	email, encodedHMAC, err := validateVolEmail(r, ctx)
	if err != nil {
		ctx.Infos.Printf("/vols/shift HMAC validation failed: %s", err.Error())
		RenderFindShift(w, r, ctx)
		return
	}
	ctx.Infos.Printf("/vols/shift validated email: %s", email)

	/* Find volunteer signups */
	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/vol/shift listvolunteerapps failed ! %s", err.Error())
		return
	}

	// fixme: add "sign up to volunteer" state :)
	if len(volapps) == 0 {
		handle404(w, r, ctx)
		return
	}

	// Populate WorkShifts and per-conf VolInfo for each volunteer application
	volInfosByConf, err := getters.GetVolInfoMap(ctx)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/vol/shift getvolinfomap failed ! %s", err.Error())
		return
	}

	for _, vol := range volapps {
		conf := vol.ScheduleFor[0]
		confShifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
		if err != nil {
			ctx.Err.Printf("/vol/shift failed to get shifts for conf %s: %s", conf.Tag, err.Error())
			continue
		}
		vol.WorkShifts = getSelectedShifts(vol, confShifts)
	}

	encodedEmail := r.URL.Query().Get("em")
	confs := listConfs(w, ctx)
	err = ctx.TemplateCache.ExecuteTemplate(w, "volunteers/shift.tmpl", &VolShiftPage{
		Name:     volapps[0].Name,
		Hometown: volapps[0].Hometown,
		Email:    encodedEmail,
		HMAC:     encodedHMAC,
		Stats:    calcStats(volapps),
		VolApps:  volapps,
		Confs:    confs,
		VolInfos: volInfosByConf,
		Year:     helpers.CurrentYear(),
	})

	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/vol/shift ExecuteTemplate failed ! %s", err.Error())
		return
	}
}

func buildShiftDisplays(vol *types.Volunteer, shifts []*types.WorkShift, selectedShifts []*types.WorkShift) map[string][]*ShiftDisplay {
	grouped := make(map[string][]*ShiftDisplay)

	for _, shift := range shifts {
		if shift.ShiftTime == nil {
			continue
		}

		dayKey := shift.DayOf()
		display := &ShiftDisplay{
			Shift:       shift,
			IsAvailable: vol.AvailableOn(shift),
			IsEligible:  shift.Type == nil || !vol.WillNotWork(shift.Type),
			IsFull:      shift.IsFull(),
			IsSelected:  shift.IsAssigned(vol.Ref),
			Conflicts:   shift.Intersects(selectedShifts),
		}

		// Compute CanSelect and Reason
		if display.IsSelected {
			display.CanSelect = false
			display.Reason = "Already selected"
		} else if !display.IsAvailable {
			display.CanSelect = false
			display.Reason = "Not available this day"
		} else if !display.IsEligible {
			display.CanSelect = false
			display.Reason = "Job type not preferred"
		} else if display.IsFull {
			display.CanSelect = false
			display.Reason = "Shift is full"
		} else if display.Conflicts {
			display.CanSelect = false
			display.Reason = "Conflicts with selected shift"
		} else {
			display.CanSelect = true
		}

		grouped[dayKey] = append(grouped[dayKey], display)
	}

	// Sort each day's shifts by start time
	for _, dayShifts := range grouped {
		sort.Slice(dayShifts, func(i, j int) bool {
			return dayShifts[i].Shift.ShiftTime.Start.Before(dayShifts[j].Shift.ShiftTime.Start)
		})
	}

	return grouped
}

func getSelectedShifts(vol *types.Volunteer, shifts []*types.WorkShift) []*types.WorkShift {
	var selected []*types.WorkShift
	for _, shift := range shifts {
		if shift.IsAssigned(vol.Ref) {
			selected = append(selected, shift)
		}
	}

	// Sort by day and start time
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].ShiftTime == nil {
			return true
		}
		if selected[j].ShiftTime == nil {
			return false
		}
		return selected[i].ShiftTime.Start.Before(selected[j].ShiftTime.Start)
	})

	return selected
}

func findVolForConf(volapps []*types.Volunteer, confTag string) *types.Volunteer {
	for _, vol := range volapps {
		for _, conf := range vol.ScheduleFor {
			if conf.Tag == confTag {
				return vol
			}
		}
	}
	return nil
}

func VolunteerShiftSignup(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	// Get volunteer applications
	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/vols/shift/%s listvolunteerapps failed ! %s", confTag, err.Error())
		return
	}

	// Find the volunteer application for this conference
	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		ctx.Err.Printf("/vols/shift/%s no volunteer app for conf", confTag)
		handle404(w, r, ctx)
		return
	}

	// Check if volunteer is in Pending Shifts status
	if vol.Status != "PendingShifts" && vol.Status != "Scheduled" {
		ctx.Err.Printf("/vols/shift/%s volunteer not in Pending Shifts status: %s", confTag, vol.Status)
		handle404(w, r, ctx)
		return
	}

	// Get shifts for this conference
	confShifts, err := getters.GetShiftsForConf(ctx, confTag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/vols/shift/%s getshiftsforconf failed ! %s", confTag, err.Error())
		return
	}

	// Get currently selected shifts
	selectedShifts := getSelectedShifts(vol, confShifts)

	// Build display data
	shiftDisplays := buildShiftDisplays(vol, confShifts, selectedShifts)

	// Get conference info
	var conf *types.Conf
	for _, c := range vol.ScheduleFor {
		if c.Tag == confTag {
			conf = c
			break
		}
	}

	minShifts := 3
	canSubmit := len(selectedShifts) >= minShifts

	encodedHMAC := r.URL.Query().Get("hr")
	encodedEmail := r.URL.Query().Get("em")

	// Build form helpers (availability + work prefs editor) so the volunteer
	// can update them inline without going back to the application form.
	jobs := listJobs(w, ctx)
	yesJobs := helpers.BuildJobs("yjob-", jobs, false)
	noJobs := helpers.BuildJobs("njob-", jobs, false)

	yesSet := make(map[string]bool)
	for _, j := range vol.WorkYes {
		yesSet[j.Tag] = true
	}
	noSet := make(map[string]bool)
	for _, j := range vol.WorkNo {
		noSet[j.Tag] = true
	}
	for i := range yesJobs {
		yesJobs[i].Checked = yesSet[yesJobs[i].ItemID[len("yjob-"):]]
	}
	for i := range noJobs {
		noJobs[i].Checked = noSet[noJobs[i].ItemID[len("njob-"):]]
	}

	daysList := conf.DaysList("days-", true)
	availSet := make(map[string]bool)
	for _, d := range vol.Availability {
		availSet[d] = true
	}
	for i := range daysList {
		daysList[i].Checked = availSet[daysList[i].ItemID[len("days-"):]]
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "volunteers/shift_signup.tmpl", &ShiftSignupPage{
		Vol:            vol,
		Conf:           conf,
		AvailShifts:    shiftDisplays,
		SelectedShifts: selectedShifts,
		MinShifts:      minShifts,
		ShiftProgress:  len(selectedShifts),
		CanSubmit:      canSubmit,
		ConfRef:        confTag,
		Email:          encodedEmail,
		HMAC:           encodedHMAC,
		DaysList:       daysList,
		YesJobs:        yesJobs,
		NoJobs:         noJobs,
		Year:           helpers.CurrentYear(),
	})

	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/vols/shift/%s ExecuteTemplate failed ! %s", confTag, err.Error())
		return
	}
}

func VolunteerSelectShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	shiftRef := r.Form.Get("shiftRef")

	if shiftRef == "" {
		http.Error(w, "Missing shift reference", http.StatusBadRequest)
		return
	}

	// Get volunteer
	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		return
	}

	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Error(w, "Volunteer not found", http.StatusNotFound)
		return
	}

	// Assign volunteer to shift
	err = getters.AssignVolunteerToShift(ctx, vol.Ref, shiftRef)
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/select assign failed: %s", confTag, err.Error())
		http.Error(w, "Failed to assign shift", http.StatusInternalServerError)
		return
	}

	// Re-render the shift list
	renderShiftList(w, r, ctx, email, confTag)
}

func VolunteerRemoveShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	shiftRef := r.Form.Get("shiftRef")

	if shiftRef == "" {
		http.Error(w, "Missing shift reference", http.StatusBadRequest)
		return
	}

	// Get volunteer
	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		return
	}

	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Error(w, "Volunteer not found", http.StatusNotFound)
		return
	}

	// Prevent removal within two weeks of conference start
	if len(vol.ScheduleFor) > 0 && vol.ScheduleFor[0].WithinTwoWeeks() {
		http.Error(w, "Cannot modify shifts within two weeks of the conference", http.StatusBadRequest)
		return
	}

	// Remove volunteer from shift
	err = getters.RemoveVolunteerFromShift(ctx, vol.Ref, shiftRef)
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/remove failed: %s", confTag, err.Error())
		http.Error(w, "Failed to remove shift", http.StatusInternalServerError)
		return
	}

	// CANCEL ICS for this volunteer's calendar entry.
	// Best-effort — log on error, don't fail the remove.
	cancelShiftCalForVol(ctx, vol, shiftRef, confTag)

	// Re-render the shift list
	renderShiftList(w, r, ctx, email, confTag)
}

// cancelShiftCalForVol looks up the shift + conf and fires a
// CANCEL ICS to the given volunteer, removing the dropped shift
// from their calendar. No-op when the shift has no CalNotif
// (never invited) or the lookups fail. Logged-only; never fails
// the surrounding remove.
func cancelShiftCalForVol(ctx *config.AppContext, vol *types.Volunteer, shiftRef, confTag string) {
	if vol == nil || vol.Email == "" {
		return
	}
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("cancelShiftCalForVol confs: %s", err)
		return
	}
	var conf *types.Conf
	for _, c := range confs {
		if c != nil && c.Tag == confTag {
			conf = c
			break
		}
	}
	if conf == nil {
		return
	}
	shifts, err := getters.GetShiftsForConf(ctx, confTag)
	if err != nil {
		ctx.Err.Printf("cancelShiftCalForVol shifts %s: %s", confTag, err)
		return
	}
	for _, s := range shifts {
		if s != nil && s.Ref == shiftRef {
			if err := DispatchShiftICSCancelForVol(ctx, s, conf, vol.Email, vol.Name); err != nil {
				ctx.Err.Printf("cancelShiftCalForVol dispatch %q: %s", s.Name, err)
			}
			return
		}
	}
}

func renderShiftList(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, email, confTag string) {
	// Re-fetch data for updated display
	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		return
	}

	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Error(w, "Volunteer not found", http.StatusNotFound)
		return
	}

	confShifts, err := getters.GetShiftsForConf(ctx, confTag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		return
	}

	selectedShifts := getSelectedShifts(vol, confShifts)
	shiftDisplays := buildShiftDisplays(vol, confShifts, selectedShifts)

	var conf *types.Conf
	for _, c := range vol.ScheduleFor {
		if c.Tag == confTag {
			conf = c
			break
		}
	}

	minShifts := 3
	canSubmit := len(selectedShifts) >= minShifts

	encodedHMAC := r.URL.Query().Get("hr")
	encodedEmail := r.URL.Query().Get("em")

	err = ctx.TemplateCache.ExecuteTemplate(w, "volunteers/shift_list.tmpl", &ShiftSignupPage{
		Vol:            vol,
		Conf:           conf,
		AvailShifts:    shiftDisplays,
		SelectedShifts: selectedShifts,
		MinShifts:      minShifts,
		ShiftProgress:  len(selectedShifts),
		CanSubmit:      canSubmit,
		ConfRef:        confTag,
		Email:          encodedEmail,
		HMAC:           encodedHMAC,
		Year:           helpers.CurrentYear(),
	})

	if err != nil {
		ctx.Err.Printf("shift_list template failed: %s", err.Error())
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func VolunteerSubmitShifts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	// Get volunteer
	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		return
	}

	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Error(w, "Volunteer not found", http.StatusNotFound)
		return
	}

	// Get shifts and verify minimum
	confShifts, err := getters.GetShiftsForConf(ctx, confTag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		return
	}

	selectedShifts := getSelectedShifts(vol, confShifts)
	minShifts := 3

	if len(selectedShifts) < minShifts {
		http.Error(w, fmt.Sprintf("Must select at least %d shifts", minShifts), http.StatusBadRequest)
		return
	}

	// Run the full scheduled flow (status update, email, ticket, calendar)
	conf := vol.ScheduleFor[0]
	vol.WorkShifts = selectedShifts
	err = runScheduledFlow(ctx, vol, conf)
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/submit scheduled flow failed: %s", confTag, err.Error())
		http.Error(w, "Failed to schedule volunteer", http.StatusInternalServerError)
		return
	}

	// Redirect back to dashboard
	encodedHMAC := r.URL.Query().Get("hr")
	encodedEmail := r.URL.Query().Get("em")
	redirectURL := fmt.Sprintf("/vols/shift?hr=%s&em=%s", encodedHMAC, encodedEmail)
	w.Header().Set("HX-Redirect", redirectURL)
}

// runScheduledFlow runs the post-status-update logic that promotes a volunteer
// to "Scheduled": updates Notion status, sends the onboarding email, issues a
// ticket, subscribes to the volunteer newsletter, and sends calendar invites
// (if Google Calendar is connected). Caller must have already populated
// vol.WorkShifts with the assigned shifts. Failures in non-critical steps
// (email, calendar invites) are logged but don't abort the flow.
func runScheduledFlow(ctx *config.AppContext, vol *types.Volunteer, conf *types.Conf) error {
	// Update status
	err := getters.UpdateVolunteerStatus(ctx, vol.Ref, "Scheduled")
	if err != nil {
		return fmt.Errorf("status update: %w", err)
	}

	// Look up VolInfo for orientation details
	volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("scheduled flow: failed to get volinfo for %s: %s", conf.Tag, err)
		// continue without volinfo
	}

	// Send onboarding email
	_, err = emails.OnlyForVolShift(ctx, volinfo, vol)
	if err != nil {
		ctx.Err.Printf("scheduled flow: failed to send onboarding email to %s: %s", vol.Email, err)
	}

	// Issue volunteer ticket
	tixType := "volunteer"
	entry := types.Entry{
		ID:       vol.RegisID(),
		ConfRef:  conf.Ref,
		Currency: "USD",
		Created:  time.Now(),
		Email:    vol.Email,
		Items: []types.Item{
			types.Item{
				Total: 1,
				Desc:  conf.Desc,
				Type:  tixType,
			},
		},
	}

	err = getters.AddTickets(ctx, &entry, "volreg")
	if err != nil {
		return fmt.Errorf("add ticket: %w", err)
	}

	err = missives.NewTicketSub(ctx, vol.Email, conf.Tag, tixType, true)
	if err != nil {
		ctx.Err.Printf("scheduled flow: newsletter sub failed for %s: %s", vol.Email, err)
	}

	ctx.Infos.Println("Scheduled volunteer, ticket added:", entry.ID)

	// Self-hosted ICS calendar invites: one per shift this
	// volunteer just signed up for, plus one orientation invite if
	// the conf has volinfo.OrientTimes set. No more
	// google.IsLoggedIn() gate — the pipeline runs as long as the
	// mailer is reachable.
	recipient := ics.Attendee{Email: vol.Email, Name: vol.Name}
	for _, shift := range vol.WorkShifts {
		if shift == nil || shift.ShiftTime == nil || shift.ShiftTime.End == nil {
			continue
		}
		if err := DispatchShiftICS(ctx, shift, conf, []ics.Attendee{recipient}, kindRequest, false); err != nil {
			ctx.Err.Printf("scheduled flow: cal invite failed for shift %s: %s", shift.Name, err)
		}
	}

	if volinfo != nil && volinfo.OrientTimes != nil && volinfo.OrientTimes.End != nil {
		if err := DispatchOrientICS(ctx, conf, recipient, volinfo.OrientTimes.Start, *volinfo.OrientTimes.End, volinfo.OrientLink); err != nil {
			ctx.Err.Printf("scheduled flow: orientation cal invite failed: %s", err)
		}
	}

	return nil
}

func VolAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	vols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load volunteers", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/volcoord failed to get volunteers: %s", conf.Tag, err.Error())
		return
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/volcoord failed to get shifts: %s", conf.Tag, err.Error())
		return
	}

	// Populate WorkShifts for each volunteer
	for _, vol := range vols {
		vol.WorkShifts = getSelectedShifts(vol, shifts)
	}

	// Sort shifts by day and time, earliest first
	sort.SliceStable(shifts, func(i, j int) bool {
		a, b := shifts[i].ShiftTime, shifts[j].ShiftTime
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return a.Start.Before(b.Start)
	})

	// Sort volunteers by created time, most recent first
	sort.SliceStable(vols, func(i, j int) bool {
		a, b := vols[i].CreatedAt, vols[j].CreatedAt
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		return a.Before(*b)
	})

	// Compute dashboard stats from the *unfiltered* volunteer list so
	// the headline numbers don't shift when admins click filter chips.
	stats := computeVolAdminStats(vols, shifts)
	allVols := vols

	statusFilter := r.URL.Query().Get("status")

	// Filter by status if requested
	if statusFilter != "" {
		var filtered []*types.Volunteer
		for _, vol := range vols {
			if vol.Status == statusFilter {
				filtered = append(filtered, vol)
			}
		}
		vols = filtered
	}

	missiveList, err := getters.ListOnlyForLetters(ctx)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord failed to load missives: %s", conf.Tag, err.Error())
		// continue without missives
	}

	volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord failed to load volinfo: %s", conf.Tag, err.Error())
		// continue without volinfo
	}
	orientStartInput, orientEndInput := orientationInputValues(volinfo, conf)
	orientationRecipientCt := len(dedupeAttendees(append(scheduledVolunteerAttendees(allVols), orientationStaffRecipients(ctx, conf.Tag)...)))

	err = ctx.TemplateCache.ExecuteTemplate(w, "volunteers/admin.tmpl", &VolAdminPage{
		Conf:                   conf,
		Volunteers:             vols,
		Shifts:                 shifts,
		VolInfo:                volinfo,
		OrientationStartInput:  orientStartInput,
		OrientationEndInput:    orientEndInput,
		OrientationRecipientCt: orientationRecipientCt,
		StatusFilter:           statusFilter,
		Missives:               missiveList,
		FlashMessage:           r.URL.Query().Get("flash"),
		Year:                   helpers.CurrentYear(),
		DeclineTitle:           defaultVolDeclineTitle(conf),
		DeclineBody:            defaultVolDeclineBody(),
		Stats:                  stats,
		EmailCompose: &EmailComposeData{
			Title:            "Email Selected Volunteers",
			Description:      "Write a one-off email to volunteers. Uses Go template syntax.",
			TitlePlaceholder: "Subject line",
			BodyPlaceholder:  "Hi {{ .Volunteer.Name }},\n\nYour shifts for {{ .Conf.Desc }}...",
			Fields: []EmailFieldGroup{
				fieldGroup(".Volunteer", types.Volunteer{}, false),
				fieldGroup(".Conf", types.Conf{}, false),
				fieldGroup(".VolInfo", types.VolInfo{}, false),
			},
		},
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/volcoord template failed: %s", conf.Tag, err.Error())
	}
}

func VolAdminPromote(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	targetStatus := r.FormValue("target_status")
	fromStatus := r.FormValue("from_status")

	if targetStatus == "" || fromStatus == "" {
		http.Error(w, "Missing status parameters", http.StatusBadRequest)
		return
	}

	vols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load volunteers", http.StatusInternalServerError)
		return
	}

	promoted := 0
	for _, vol := range vols {
		if vol.Status != fromStatus {
			continue
		}

		err = getters.UpdateVolunteerStatus(ctx, vol.Ref, targetStatus)
		if err != nil {
			ctx.Err.Printf("/%s/volcoord/promote failed to update %s: %s", conf.Tag, vol.Name, err.Error())
			continue
		}

		// Send shift signup email when promoting to PendingShifts
		if targetStatus == "PendingShifts" {
			_, emailErr := emails.OnlyForVolSignup(ctx, vol, conf)
			if emailErr != nil {
				ctx.Err.Printf("/%s/volcoord/promote email failed for %s: %s", conf.Tag, vol.Email, emailErr)
			}
		}

		promoted++
	}

	// Redirect back to admin page
	http.Redirect(w, r, fmt.Sprintf("/%s/volcoord", conf.Tag), http.StatusSeeOther)
}

func VolAdminAutoAssign(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	vols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load volunteers", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/volcoord/auto-assign failed to get volunteers: %s", conf.Tag, err.Error())
		return
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/volcoord/auto-assign failed to get shifts: %s", conf.Tag, err.Error())
		return
	}

	// Only consider PendingShifts volunteers; pre-populate their existing assignments
	var eligibleVols []*types.Volunteer
	for _, vol := range vols {
		if vol.Status != "PendingShifts" {
			continue
		}
		vol.WorkShifts = getSelectedShifts(vol, shifts)
		eligibleVols = append(eligibleVols, vol)
	}

	err = volunteers.AssignShifts(ctx, eligibleVols, shifts)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/auto-assign failed: %s", conf.Tag, err.Error())
	}

	http.Redirect(w, r, fmt.Sprintf("/%s/volcoord", conf.Tag), http.StatusSeeOther)
}

func VolunteerDecline(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		return
	}

	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Error(w, "Volunteer not found", http.StatusNotFound)
		return
	}

	if vol.Status == "Declined" {
		http.Error(w, "Already declined", http.StatusBadRequest)
		return
	}

	if len(vol.ScheduleFor) == 0 {
		http.Error(w, "No conference associated", http.StatusBadRequest)
		return
	}
	conf := vol.ScheduleFor[0]

	// Prevent cancellation within two weeks of conference start
	if vol.Status == "Scheduled" && conf.WithinTwoWeeks() {
		http.Error(w, "Cannot cancel shifts within two weeks of the conference. Please reach out to the organizers if you can no longer attend.", http.StatusBadRequest)
		return
	}

	// Release any shift assignments
	confShifts, err := getters.GetShiftsForConf(ctx, confTag)
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/decline failed to load shifts: %s", confTag, err.Error())
	} else {
		releaseVolunteerShifts(ctx, conf, vol, confShifts, "vols/shift/decline")
	}

	// Update status to Declined
	err = getters.UpdateVolunteerStatus(ctx, vol.Ref, "Declined")
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/decline status update failed: %s", confTag, err.Error())
	}

	// Send cancellation email
	_, err = emails.OnlyForVolCancel(ctx, vol, conf)
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/decline email failed: %s", confTag, err)
	}

	// Revoke their ticket if one was issued
	ctx.Infos.Printf("revoking ticket with id %s", vol.RegisID())
	err = getters.RevokeTicket(ctx, vol.RegisID())
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/decline ticket revoke failed: %s", confTag, err.Error())
	}

	// Redirect back to dashboard
	encodedHMAC := r.URL.Query().Get("hr")
	encodedEmail := r.URL.Query().Get("em")
	redirectURL := fmt.Sprintf("/vols/shift?hr=%s&em=%s", encodedHMAC, encodedEmail)
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// volAdminLoadVol fetches a single volunteer for the conf, populates their
// volSelfRedirect returns the volunteer to their own shift signup page,
// preserving the HMAC + email query string.
func volSelfRedirect(w http.ResponseWriter, r *http.Request, confTag string) {
	encodedHMAC := r.URL.Query().Get("hr")
	encodedEmail := r.URL.Query().Get("em")
	http.Redirect(w, r, fmt.Sprintf("/vols/shift/%s?hr=%s&em=%s", confTag, encodedHMAC, encodedEmail), http.StatusSeeOther)
}

func VolunteerUpdateAvailability(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		return
	}
	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Error(w, "Volunteer not found", http.StatusNotFound)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var days []string
	for k := range r.PostForm {
		if strings.HasPrefix(k, "days-") {
			days = append(days, k[len("days-"):])
		}
	}

	err = getters.UpdateVolunteerAvailability(ctx, vol.Ref, days)
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/availability update failed: %s", confTag, err)
		http.Error(w, "Failed to update availability", http.StatusInternalServerError)
		return
	}

	volSelfRedirect(w, r, confTag)
}

func VolunteerUpdateWorkPrefs(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	email, _, err := validateVolEmail(r, ctx)
	if err != nil {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	volapps, err := getters.ListVolunteerApps(ctx, email)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		return
	}
	vol := findVolForConf(volapps, confTag)
	if vol == nil {
		http.Error(w, "Volunteer not found", http.StatusNotFound)
		return
	}

	jobs := listJobs(w, ctx)
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	yesJobs := helpers.ParseFormJobs("yjob-", r.PostForm, jobs)
	noJobs := helpers.ParseFormJobs("njob-", r.PostForm, jobs)

	yesRefs := make([]string, len(yesJobs))
	for i, j := range yesJobs {
		yesRefs[i] = j.Ref
	}
	noRefs := make([]string, len(noJobs))
	for i, j := range noJobs {
		noRefs[i] = j.Ref
	}

	err = getters.UpdateVolunteerWorkPrefs(ctx, vol.Ref, yesRefs, noRefs)
	if err != nil {
		ctx.Err.Printf("/vols/shift/%s/work-prefs update failed: %s", confTag, err)
		http.Error(w, "Failed to update work preferences", http.StatusInternalServerError)
		return
	}

	volSelfRedirect(w, r, confTag)
}

// WorkShifts from the current shift assignments, and returns it. Used by
// every per-volunteer admin handler. Returns nil and writes an error response
// on failure.
func volAdminLoadVol(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) (*types.Conf, *types.Volunteer, []*types.WorkShift) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return nil, nil, nil
	}

	params := mux.Vars(r)
	volRef := params["volRef"]

	// Use a direct page fetch (strongly consistent) so the page reflects any
	// edits made in a preceding write within the same redirect chain. The
	// QueryDatabase index used by ListVolunteersForConf is eventually
	// consistent and can return stale results immediately after a PATCH.
	vol, err := getters.FetchVolunteer(ctx, volRef)
	if err != nil {
		http.Error(w, "Unable to load volunteer", http.StatusInternalServerError)
		ctx.Err.Printf("vol admin: failed to fetch vol %s: %s", volRef, err.Error())
		return nil, nil, nil
	}
	if vol == nil {
		handle404(w, r, ctx)
		return nil, nil, nil
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		ctx.Err.Printf("vol admin: failed to load shifts for %s: %s", conf.Tag, err.Error())
		return nil, nil, nil
	}
	vol.WorkShifts = getSelectedShifts(vol, shifts)

	return conf, vol, shifts
}

func VolAdminDetails(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, vol, shifts := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	// Build form helpers, marking current values as Checked
	jobs := listJobs(w, ctx)
	yesJobs := helpers.BuildJobs("yjob-", jobs, false)
	noJobs := helpers.BuildJobs("njob-", jobs, false)

	// Mark which jobs are currently in WorkYes/WorkNo
	yesSet := make(map[string]bool)
	for _, j := range vol.WorkYes {
		yesSet[j.Tag] = true
	}
	noSet := make(map[string]bool)
	for _, j := range vol.WorkNo {
		noSet[j.Tag] = true
	}
	for i := range yesJobs {
		yesJobs[i].Checked = yesSet[yesJobs[i].ItemID[len("yjob-"):]]
	}
	for i := range noJobs {
		noJobs[i].Checked = noSet[noJobs[i].ItemID[len("njob-"):]]
	}

	daysList := conf.DaysList("days-", true)
	availSet := make(map[string]bool)
	for _, d := range vol.Availability {
		availSet[d] = true
	}
	for i := range daysList {
		daysList[i].Checked = availSet[daysList[i].ItemID[len("days-"):]]
	}

	// Build shift selection display data (mirrors the volunteer's own selection page)
	selectedShifts := getSelectedShifts(vol, shifts)
	shiftDisplays := buildShiftDisplays(vol, shifts, selectedShifts)

	// Sorted day keys so the table renders chronologically
	dayKeys := make([]string, 0, len(shiftDisplays))
	for k := range shiftDisplays {
		dayKeys = append(dayKeys, k)
	}
	sort.Slice(dayKeys, func(i, j int) bool {
		return shiftDisplays[dayKeys[i]][0].Shift.ShiftTime.Start.Before(
			shiftDisplays[dayKeys[j]][0].Shift.ShiftTime.Start)
	})

	// Unique job types appearing in this conf's shifts (for the type filter)
	seenJobs := make(map[string]bool)
	var jobTypes []*types.JobType
	for _, s := range shifts {
		if s.Type == nil || seenJobs[s.Type.Tag] {
			continue
		}
		seenJobs[s.Type.Tag] = true
		jobTypes = append(jobTypes, s.Type)
	}
	sort.Slice(jobTypes, func(i, j int) bool {
		return jobTypes[i].Title < jobTypes[j].Title
	})

	err := ctx.TemplateCache.ExecuteTemplate(w, "volunteers/vol_details.tmpl", &VolDetailsPage{
		Conf:           conf,
		Vol:            vol,
		AllShifts:      shifts,
		ShiftDisplays:  shiftDisplays,
		SelectedShifts: selectedShifts,
		DayKeys:        dayKeys,
		JobTypes:       jobTypes,
		YesJobs:        yesJobs,
		NoJobs:         noJobs,
		DaysList:       daysList,
		Statuses:       []string{"Applied", "Waitlist", "PendingShifts", "Scheduled", "Declined"},
		Year:           helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("vol admin details template failed: %s", err.Error())
	}
}

// computeVolAdminStats sums shift capacity vs assignments and counts
// volunteers still in pre-scheduled states. VolsNeeded is ceil-divided
// by VolShiftQuota so a 15-spot gap with a 3-shift quota reads as 5
// vols needed (matching the user-facing wording).
func computeVolAdminStats(vols []*types.Volunteer, shifts []*types.WorkShift) *VolAdminStats {
	s := &VolAdminStats{}
	for _, sh := range shifts {
		if sh == nil {
			continue
		}
		s.ShiftsTotal += int(sh.MaxVols)
		assigned := len(sh.AssigneesRef)
		if assigned > int(sh.MaxVols) {
			assigned = int(sh.MaxVols)
		}
		s.ShiftsFilled += assigned
	}
	s.ShiftsLeft = s.ShiftsTotal - s.ShiftsFilled
	if s.ShiftsLeft < 0 {
		s.ShiftsLeft = 0
	}
	for _, v := range vols {
		if v == nil {
			continue
		}
		if v.Status == "Applied" || v.Status == "PendingShifts" {
			s.UnscheduledVols++
		}
	}
	if s.ShiftsLeft > 0 && VolShiftQuota > 0 {
		s.VolsNeeded = (s.ShiftsLeft + VolShiftQuota - 1) / VolShiftQuota
	}
	return s
}

func volAdminRedirect(w http.ResponseWriter, r *http.Request, conf *types.Conf, volRef string) {
	// Honor an optional `return` form value so callers (e.g. the admin
	// list page's quick-action buttons) can stay on their current view
	// instead of bouncing into the vol_details page. Only accept paths
	// rooted at /vols/admin/<this-conf>/ to avoid open-redirect.
	if ret := r.FormValue("return"); ret != "" {
		prefix := fmt.Sprintf("/%s/volcoord", conf.Tag)
		if strings.HasPrefix(ret, prefix+"/") || ret == prefix || strings.HasPrefix(ret, prefix+"?") {
			http.Redirect(w, r, ret, http.StatusSeeOther)
			return
		}
	}
	http.Redirect(w, r, fmt.Sprintf("/%s/volcoord/vol/%s", conf.Tag, volRef), http.StatusSeeOther)
}

func VolAdminUpdateStatus(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	status := r.FormValue("status")
	if status == "" {
		http.Error(w, "Missing status", http.StatusBadRequest)
		return
	}

	err := getters.UpdateVolunteerStatus(ctx, vol.Ref, status)
	if err != nil {
		ctx.Err.Printf("vol admin update status failed: %s", err)
		http.Error(w, "Failed to update status", http.StatusInternalServerError)
		return
	}

	volAdminRedirect(w, r, conf, vol.Ref)
}

func VolAdminUpdateAvailability(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var days []string
	for k := range r.PostForm {
		if strings.HasPrefix(k, "days-") {
			days = append(days, k[len("days-"):])
		}
	}

	err := getters.UpdateVolunteerAvailability(ctx, vol.Ref, days)
	if err != nil {
		ctx.Err.Printf("vol admin update availability failed: %s", err)
		http.Error(w, "Failed to update availability", http.StatusInternalServerError)
		return
	}

	volAdminRedirect(w, r, conf, vol.Ref)
}

func VolAdminUpdateWorkPrefs(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	jobs := listJobs(w, ctx)
	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	yesJobs := helpers.ParseFormJobs("yjob-", r.PostForm, jobs)
	noJobs := helpers.ParseFormJobs("njob-", r.PostForm, jobs)

	yesRefs := make([]string, len(yesJobs))
	for i, j := range yesJobs {
		yesRefs[i] = j.Ref
	}
	noRefs := make([]string, len(noJobs))
	for i, j := range noJobs {
		noRefs[i] = j.Ref
	}

	err := getters.UpdateVolunteerWorkPrefs(ctx, vol.Ref, yesRefs, noRefs)
	if err != nil {
		ctx.Err.Printf("vol admin update work prefs failed: %s", err)
		http.Error(w, "Failed to update work preferences", http.StatusInternalServerError)
		return
	}

	volAdminRedirect(w, r, conf, vol.Ref)
}

func VolAdminAddShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	shiftRef := r.FormValue("shiftRef")
	if shiftRef == "" {
		http.Error(w, "Missing shiftRef", http.StatusBadRequest)
		return
	}

	err := getters.AssignVolunteerToShift(ctx, vol.Ref, shiftRef)
	if err != nil {
		ctx.Err.Printf("vol admin add shift failed: %s", err)
		http.Error(w, "Failed to add shift", http.StatusInternalServerError)
		return
	}

	volAdminRedirect(w, r, conf, vol.Ref)
}

func VolAdminRemoveShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	shiftRef := r.FormValue("shiftRef")
	if shiftRef == "" {
		http.Error(w, "Missing shiftRef", http.StatusBadRequest)
		return
	}

	err := getters.RemoveVolunteerFromShift(ctx, vol.Ref, shiftRef)
	if err != nil {
		ctx.Err.Printf("vol admin remove shift failed: %s", err)
		http.Error(w, "Failed to remove shift", http.StatusInternalServerError)
		return
	}

	// CANCEL ICS for this volunteer's calendar entry — vol
	// admin removed them from the shift, so it shouldn't sit
	// on their calendar.
	cancelShiftCalForVol(ctx, vol, shiftRef, conf.Tag)

	volAdminRedirect(w, r, conf, vol.Ref)
}

func VolAdminMarkScheduled(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	if len(vol.WorkShifts) == 0 {
		http.Error(w, "Cannot schedule a volunteer with zero assigned shifts", http.StatusBadRequest)
		return
	}

	// Make sure ScheduleFor is set so runScheduledFlow has the conf
	if len(vol.ScheduleFor) == 0 {
		vol.ScheduleFor = []*types.Conf{conf}
	}

	err := runScheduledFlow(ctx, vol, conf)
	if err != nil {
		ctx.Err.Printf("vol admin mark scheduled failed for %s: %s", vol.Ref, err)
		http.Error(w, "Failed to schedule volunteer", http.StatusInternalServerError)
		return
	}

	volAdminRedirect(w, r, conf, vol.Ref)
}

func VolAdminBulkEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	volRefs := r.Form["vol_refs"]

	testEmail := r.FormValue("test_email")
	isTest := r.FormValue("send_test") == "1" && testEmail != ""

	if len(volRefs) == 0 && !isTest {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=No+volunteers+selected", conf.Tag), http.StatusSeeOther)
		return
	}

	// Load volunteers + filter to selected
	allVols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load volunteers", http.StatusInternalServerError)
		return
	}

	refSet := make(map[string]bool, len(volRefs))
	for _, ref := range volRefs {
		refSet[ref] = true
	}

	var targets []*types.Volunteer
	for _, v := range allVols {
		if refSet[v.Ref] {
			targets = append(targets, v)
		}
	}

	// Pre-load shifts and volinfo so each send can include shift context
	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/email failed to load shifts: %s", conf.Tag, err.Error())
	}
	for _, v := range targets {
		v.WorkShifts = getSelectedShifts(v, shifts)
	}

	volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/email failed to load volinfo: %s", conf.Tag, err.Error())
	}

	sent := 0
	title := r.FormValue("title")
	body := r.FormValue("body")
	if title == "" || body == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=Title+and+body+required", conf.Tag), http.StatusSeeOther)
		return
	}

	if isTest {
		// Use first selected volunteer, or first available if none selected
		var testVol *types.Volunteer
		if len(targets) > 0 {
			testVol = targets[0]
		} else if len(allVols) > 0 {
			testVol = allVols[0]
		}
		if testVol == nil {
			http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=No+volunteers+available+for+test", conf.Tag), http.StatusSeeOther)
			return
		}
		tv := *testVol
		tv.Email = testEmail
		_, err := emails.SendCustomToVol(ctx, &tv, conf, volinfo, title, body)
		if err != nil {
			ctx.Err.Printf("/%s/volcoord/email test -> %s failed: %s", conf.Tag, testEmail, err)
			http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=Test+email+failed", conf.Tag), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=Test+sent+to+%s", conf.Tag, testEmail), http.StatusSeeOther)
		return
	}

	for _, v := range targets {
		_, err := emails.SendCustomToVol(ctx, v, conf, volinfo, title, body)
		if err != nil {
			ctx.Err.Printf("/%s/volcoord/email custom -> %s failed: %s", conf.Tag, v.Email, err)
			continue
		}
		sent++
	}

	flash := fmt.Sprintf("Sent+to+%d+of+%d+volunteers", sent, len(targets))
	http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, flash), http.StatusSeeOther)
}

func defaultVolDeclineTitle(conf *types.Conf) string {
	return fmt.Sprintf("Volunteer update for %s", conf.Desc)
}

func defaultVolDeclineBody() string {
	return "Hi {{ .Volunteer.Name }},\n\nThank you again for applying to volunteer at {{ .Conf.Desc }}. We had more volunteer interest than available shifts this time, so we are not able to add you to the volunteer roster for this event.\n\nWe would still love to have you join us as an attendee. You can use discount code `{{ .DiscountCode.CodeName }}` for a discounted ticket.\n\nThank you for being willing to help make bitcoin++ happen.\n\n- bitcoin++"
}

func VolAdminDeclineSelected(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	volRefs := r.Form["vol_refs"]
	testEmail := strings.TrimSpace(r.FormValue("decline_test_email"))
	isTest := r.FormValue("decline_send_test") == "1"
	if isTest && testEmail == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Test email is required")), http.StatusSeeOther)
		return
	}
	if len(volRefs) == 0 && !isTest {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("No volunteers selected")), http.StatusSeeOther)
		return
	}

	title := strings.TrimSpace(r.FormValue("decline_title"))
	body := strings.TrimSpace(r.FormValue("decline_body"))
	if title == "" || body == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Decline title and body are required")), http.StatusSeeOther)
		return
	}

	discount, err := validateVolDeclineDiscount(ctx, conf, r.FormValue("decline_discount_code"))
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape(err.Error())), http.StatusSeeOther)
		return
	}

	allVols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load volunteers", http.StatusInternalServerError)
		return
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/decline-selected failed to load shifts: %s", conf.Tag, err.Error())
	}

	refSet := make(map[string]bool, len(volRefs))
	for _, ref := range volRefs {
		refSet[ref] = true
	}

	var targets []*types.Volunteer
	var firstEligible *types.Volunteer
	for _, v := range allVols {
		if !volBulkDeclineStatusAllowed(v.Status) {
			continue
		}
		v.WorkShifts = getSelectedShifts(v, shifts)
		if firstEligible == nil {
			firstEligible = v
		}
		if refSet[v.Ref] {
			targets = append(targets, v)
		}
	}

	volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/decline-selected failed to load volinfo: %s", conf.Tag, err.Error())
	}

	if isTest {
		testVol := firstEligible
		if len(targets) > 0 {
			testVol = targets[0]
		}
		if testVol == nil {
			http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("No Applied or Pending Shifts volunteers available for test")), http.StatusSeeOther)
			return
		}
		tv := *testVol
		tv.Email = testEmail
		if _, err := emails.SendCustomToVolWithDiscount(ctx, &tv, conf, volinfo, discount, title, body); err != nil {
			ctx.Err.Printf("/%s/volcoord/decline-selected test -> %s failed: %s", conf.Tag, testEmail, err)
			http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Test decline email failed")), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Test decline email sent to "+testEmail)), http.StatusSeeOther)
		return
	}

	sent := 0
	declined := 0
	for _, v := range targets {
		if _, err := emails.SendCustomToVolWithDiscount(ctx, v, conf, volinfo, discount, title, body); err != nil {
			ctx.Err.Printf("/%s/volcoord/decline-selected custom -> %s failed: %s", conf.Tag, v.Email, err)
			continue
		}
		sent++

		releaseVolunteerShifts(ctx, conf, v, shifts, "volcoord/decline-selected")
		if err := getters.UpdateVolunteerStatus(ctx, v.Ref, "Declined"); err != nil {
			ctx.Err.Printf("/%s/volcoord/decline-selected status %s failed: %s", conf.Tag, v.Email, err)
			continue
		}
		declined++
	}

	flash := fmt.Sprintf("Sent decline email to %d of %d selected volunteers. Moved %d to Declined.", sent, len(targets), declined)
	http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape(flash)), http.StatusSeeOther)
}

func volBulkDeclineStatusAllowed(status string) bool {
	return status == "Applied" || status == "PendingShifts"
}

func validateVolDeclineDiscount(ctx *config.AppContext, conf *types.Conf, code string) (*types.DiscountCode, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("Discount code is required")
	}
	discount, err := getters.FindDiscount(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("Discount lookup failed: %w", err)
	}
	if discount == nil {
		return nil, fmt.Errorf("Discount code %q was not found", code)
	}
	if len(discount.ConfRef) > 0 {
		found := false
		for _, ref := range discount.ConfRef {
			if ref == conf.Ref {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("Discount code %q is not valid for %s", discount.CodeName, conf.Desc)
		}
	}
	if discount.MaxUses > 0 && discount.UsesCount >= discount.MaxUses {
		return nil, fmt.Errorf("Discount code %q has been fully redeemed", discount.CodeName)
	}
	if discount.IsDateExpired(time.Now().UTC()) {
		return nil, fmt.Errorf("Discount code %q is not active today", discount.CodeName)
	}
	return discount, nil
}

func releaseVolunteerShifts(ctx *config.AppContext, conf *types.Conf, vol *types.Volunteer, shifts []*types.WorkShift, label string) {
	selectedShifts := vol.WorkShifts
	if selectedShifts == nil {
		selectedShifts = getSelectedShifts(vol, shifts)
	}
	for _, shift := range selectedShifts {
		if shift == nil {
			continue
		}
		if err := getters.RemoveVolunteerFromShift(ctx, vol.Ref, shift.Ref); err != nil {
			ctx.Err.Printf("/%s/%s remove shift %s for %s failed: %s", conf.Tag, label, shift.Name, vol.Email, err)
			continue
		}
		if dErr := DispatchShiftICSCancelForVol(ctx, shift, conf, vol.Email, vol.Name); dErr != nil {
			ctx.Err.Printf("/%s/%s cancel-cal %q for %s: %s", conf.Tag, label, shift.Name, vol.Email, dErr)
		}
	}
}

// parseShiftFormTimes turns a date (YYYY-MM-DD or 01/02/2006) plus two HH:MM
// time strings into start/end time.Time values in the conference's timezone.
// End is rolled over to the next day if it's earlier than start (e.g. an
// overnight shift).
func parseShiftFormTimes(conf *types.Conf, dayStr, startStr, endStr string) (time.Time, time.Time, error) {
	// Accept either Notion's "01/02/2006" or HTML date input "2006-01-02"
	loc := conf.Loc()
	var day time.Time
	var err error
	if t, e := time.ParseInLocation("2006-01-02", dayStr, loc); e == nil {
		day = t
	} else if t, e := time.ParseInLocation("01/02/2006", dayStr, loc); e == nil {
		day = t
	} else {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid date %q", dayStr)
	}

	startHM, err := time.Parse("15:04", startStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid start time %q", startStr)
	}
	endHM, err := time.Parse("15:04", endStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid end time %q", endStr)
	}

	start := time.Date(day.Year(), day.Month(), day.Day(), startHM.Hour(), startHM.Minute(), 0, 0, loc)
	end := time.Date(day.Year(), day.Month(), day.Day(), endHM.Hour(), endHM.Minute(), 0, 0, loc)
	if !end.After(start) {
		end = end.Add(24 * time.Hour)
	}
	return start, end, nil
}

// findJobByTag locates a JobType by its Tag from the cached job list.
func findJobByTag(jobs []*types.JobType, tag string) *types.JobType {
	for _, j := range jobs {
		if j.Tag == tag {
			return j
		}
	}
	return nil
}

func VolAdminShifts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/volcoord/shifts failed to get shifts: %s", conf.Tag, err.Error())
		return
	}

	jobs, err := getters.FetchJobsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/shifts failed to fetch jobs: %s", conf.Tag, err.Error())
	}

	// Resolve all unique assignees → Volunteer for name display
	volMap := make(map[string]*types.Volunteer)
	allVols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/shifts failed to load vols: %s", conf.Tag, err.Error())
	}
	for _, v := range allVols {
		volMap[v.Ref] = v
	}

	// Per-day ConfInfo strip — used below to widen the gantt's
	// MinHour/MaxHour bounds with doors-open / doors-close so a
	// coord can drag a shift earlier than the existing earliest
	// shift (e.g. into pre-doors setup time) without having to
	// edit the form. Best-effort: a load error degrades to bounds
	// based purely on shift times. We also compute a conf-wide
	// "fallback" doors range (widest window across all days that
	// DO have Doors set) so a day that's missing its own Doors row
	// still gets widened to the conf-wide venue-open window.
	infoByDay := map[string]*types.ConfInfo{}
	fallbackDoorsMin, fallbackDoorsMax := -1, -1
	if infos, err := getters.ListConfInfos(ctx, conf.Tag); err != nil {
		ctx.Err.Printf("/%s/volcoord/shifts list confinfos (continuing): %s", conf.Tag, err.Error())
	} else {
		for _, ci := range infos {
			if ci == nil || ci.Day < 1 {
				continue
			}
			key := dayDateFor(conf, ci.Day).Format("01/02/2006")
			infoByDay[key] = ci
			if ci.Doors == nil {
				continue
			}
			sH := ci.Doors.Start.Hour()
			if fallbackDoorsMin < 0 || sH < fallbackDoorsMin {
				fallbackDoorsMin = sH
			}
			if ci.Doors.End != nil {
				eH := ci.Doors.End.Hour()
				if ci.Doors.End.Minute() > 0 {
					eH++
				}
				if eH > fallbackDoorsMax {
					fallbackDoorsMax = eH
				}
			}
		}
	}

	// Group shifts by day
	groups := make(map[string]*ShiftDayGroup)
	for _, shift := range shifts {
		if shift.ShiftTime == nil {
			continue
		}
		day := shift.DayOf()
		g, ok := groups[day]
		if !ok {
			g = &ShiftDayGroup{
				Date:     day,
				DateDesc: shift.DayOfDesc(),
				MinHour:  24,
				MaxHour:  0,
			}
			groups[day] = g
		}
		g.Shifts = append(g.Shifts, shift)
		startH := shift.ShiftTime.Start.Hour()
		if startH < g.MinHour {
			g.MinHour = startH
		}
		if shift.ShiftTime.End != nil {
			endH := shift.ShiftTime.End.Hour()
			if shift.ShiftTime.End.Minute() > 0 {
				endH++
			}
			if endH > g.MaxHour {
				g.MaxHour = endH
			}
		}
	}

	// Sort each day's shifts and finalize hour ranges
	var dayList []*ShiftDayGroup
	for _, g := range groups {
		sort.Slice(g.Shifts, func(i, j int) bool {
			return g.Shifts[i].ShiftTime.Start.Before(g.Shifts[j].ShiftTime.Start)
		})
		// Widen bounds with doors-open / doors-close from this
		// day's ConfInfo so the gantt covers the full venue-open
		// window even when no shift currently touches the edges.
		// Per-day Doors win when set; otherwise fall back to the
		// conf-wide widest doors window so a day without its own
		// Doors row (common: only Day 1 has a ConfInfo entry)
		// still gets widened.
		dayMin, dayMax := -1, -1
		if ci := infoByDay[g.Date]; ci != nil && ci.Doors != nil {
			dayMin = ci.Doors.Start.Hour()
			if ci.Doors.End != nil {
				dayMax = ci.Doors.End.Hour()
				if ci.Doors.End.Minute() > 0 {
					dayMax++
				}
			}
		}
		if dayMin < 0 {
			dayMin = fallbackDoorsMin
		}
		if dayMax < 0 {
			dayMax = fallbackDoorsMax
		}
		if dayMin >= 0 && dayMin < g.MinHour {
			g.MinHour = dayMin
		}
		if dayMax >= 0 && dayMax > g.MaxHour {
			g.MaxHour = dayMax
		}
		// Pad ranges so the gantt has a little headroom (applied
		// after the doors merge so the result is min(shift, doors)
		// - 1 on the left and max(shift, doors) + 1 on the right).
		if g.MinHour > 0 {
			g.MinHour--
		}
		if g.MaxHour < 24 {
			g.MaxHour++
		}
		if g.MaxHour <= g.MinHour {
			g.MaxHour = g.MinHour + 1
		}
		dayList = append(dayList, g)
	}
	sort.Slice(dayList, func(i, j int) bool {
		return dayList[i].Shifts[0].ShiftTime.Start.Before(dayList[j].Shifts[0].ShiftTime.Start)
	})

	err = ctx.TemplateCache.ExecuteTemplate(w, "volunteers/admin_shifts.tmpl", &VolAdminShiftsPage{
		Conf:     conf,
		Days:     dayList,
		VolMap:   volMap,
		JobTypes: jobs,
		DaysList: conf.DaysList("days-", true),
		Year:     helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/volcoord/shifts template failed: %s", conf.Tag, err.Error())
	}
}

func volAdminShiftsRedirect(w http.ResponseWriter, r *http.Request, conf *types.Conf) {
	http.Redirect(w, r, fmt.Sprintf("/%s/volcoord/shifts", conf.Tag), http.StatusSeeOther)
}

func VolAdminCreateShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	jobTag := r.FormValue("job_type")
	day := r.FormValue("day")
	startStr := r.FormValue("start_time")
	endStr := r.FormValue("end_time")
	maxVols, _ := strconv.ParseUint(r.FormValue("max_vols"), 10, 32)
	priority, _ := strconv.ParseUint(r.FormValue("priority"), 10, 32)

	if name == "" {
		http.Error(w, "Name required", http.StatusBadRequest)
		return
	}

	start, end, err := parseShiftFormTimes(conf, day, startStr, endStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	jobs, _ := getters.FetchJobsCached(ctx)
	jobType := findJobByTag(jobs, jobTag)

	err = getters.CreateShift(ctx, conf, jobType, name, start, end, uint(maxVols), uint(priority))
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/shifts/new failed: %s", conf.Tag, err.Error())
		http.Error(w, "Failed to create shift: "+err.Error(), http.StatusInternalServerError)
		return
	}

	volAdminShiftsRedirect(w, r, conf)
}

func VolAdminUpdateShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	shiftRef := mux.Vars(r)["shiftRef"]

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := r.FormValue("name")
	jobTag := r.FormValue("job_type")
	day := r.FormValue("day")
	startStr := r.FormValue("start_time")
	endStr := r.FormValue("end_time")
	maxVols, _ := strconv.ParseUint(r.FormValue("max_vols"), 10, 32)
	priority, _ := strconv.ParseUint(r.FormValue("priority"), 10, 32)

	if name == "" {
		http.Error(w, "Name required", http.StatusBadRequest)
		return
	}

	start, end, err := parseShiftFormTimes(conf, day, startStr, endStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	jobs, _ := getters.FetchJobsCached(ctx)
	jobType := findJobByTag(jobs, jobTag)

	err = getters.UpdateShift(ctx, shiftRef, name, jobType, start, end, uint(maxVols), uint(priority))
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/shifts/%s/update failed: %s", conf.Tag, shiftRef, err.Error())
		http.Error(w, "Failed to update shift: "+err.Error(), http.StatusInternalServerError)
		return
	}

	volAdminShiftsRedirect(w, r, conf)
}

// VolShiftReschedule handles drag/resize gestures on the gantt UI at
// /{conf}/volcoord/shifts. JSON body: {day, startMin, endMin}. Day is
// either "01/02/2006" (matches ShiftDayGroup.Date) or "2006-01-02";
// startMin/endMin are minutes from midnight in conf-local time. Only
// the ShiftTime property gets patched — Name / JobType / MaxVols /
// Priority / Assignees stay as-is so a concurrent edit-form save
// elsewhere doesn't get clobbered.
func VolShiftReschedule(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	shiftRef := mux.Vars(r)["shiftRef"]

	var req struct {
		Day      string `json:"day"`
		StartMin int    `json:"startMin"`
		EndMin   int    `json:"endMin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.EndMin <= req.StartMin {
		http.Error(w, "endMin must be after startMin", http.StatusBadRequest)
		return
	}

	loc := conf.Loc()
	var day time.Time
	if t, e := time.ParseInLocation("01/02/2006", req.Day, loc); e == nil {
		day = t
	} else if t, e := time.ParseInLocation("2006-01-02", req.Day, loc); e == nil {
		day = t
	} else {
		http.Error(w, "bad day format", http.StatusBadRequest)
		return
	}
	start := day.Add(time.Duration(req.StartMin) * time.Minute)
	end := day.Add(time.Duration(req.EndMin) * time.Minute)

	if err := getters.UpdateShiftTimes(ctx, shiftRef, start, end); err != nil {
		ctx.Err.Printf("/%s/volcoord/shifts/%s/reschedule: %s", conf.Tag, shiftRef, err)
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	// Auto-fire calendar update for every assignee on this
	// shift. Hash check inside dispatch suppresses email when
	// the time genuinely didn't change (same start/end after a
	// no-op drag); changed times bump SEQUENCE on the
	// assignees' calendars. Best-effort — log on error, don't
	// fail the schedule write.
	dispatchShiftCalAfterReschedule(ctx, conf, shiftRef)

	w.WriteHeader(http.StatusOK)
}

// dispatchShiftCalAfterReschedule looks up the freshly-updated
// shift, resolves its assignees to (email, name) attendees, and
// fans the cal-invite update to each via DispatchShiftICS. force=
// false so the hash check inside dispatch silently skips when
// times didn't actually move.
func dispatchShiftCalAfterReschedule(ctx *config.AppContext, conf *types.Conf, shiftRef string) {
	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("shift cal-fire: load shifts %s: %s", conf.Tag, err)
		return
	}
	var shift *types.WorkShift
	for _, s := range shifts {
		if s != nil && s.Ref == shiftRef {
			shift = s
			break
		}
	}
	if shift == nil || shift.ShiftTime == nil || shift.ShiftTime.End == nil {
		return
	}
	if len(shift.AssigneesRef) == 0 {
		return
	}

	vols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("shift cal-fire: load vols %s: %s", conf.Tag, err)
		return
	}
	volByRef := make(map[string]ics.Attendee, len(vols))
	for _, v := range vols {
		if v == nil || v.Email == "" {
			continue
		}
		volByRef[v.Ref] = ics.Attendee{Email: v.Email, Name: v.Name}
	}
	recipients := make([]ics.Attendee, 0, len(shift.AssigneesRef))
	for _, ref := range shift.AssigneesRef {
		if a, ok := volByRef[ref]; ok {
			recipients = append(recipients, a)
		}
	}
	if len(recipients) == 0 {
		return
	}
	if err := DispatchShiftICS(ctx, shift, conf, recipients, kindRequest, false); err != nil {
		ctx.Err.Printf("shift cal-fire %q: %s", shift.Name, err)
	}
}

// AdminGifts renders /{conf}/admin/gifts — the per-event Speaker
// Gifts list. Conf is in the URL (no dropdown), auth gated by
// requireConfStaff. Each row is one speaker (deduped — a speaker
// on multiple talks appears once, with the clipart from their
// "most interesting" talk: fewer co-speakers wins, so a solo keynote
// outranks a panel appearance). Ties break on first-encountered, with
// a non-empty clipart beating an empty one. {conf}-staff volunteers
// also appear, using the conf's leading.png as their gift clipart.
func AdminGifts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfStaff(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil || conf == nil {
		handle404(w, r, ctx)
		return
	}
	filePath := r.URL.Query().Get("filepath")

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load talks", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/gifts get talks: %s", conf.Tag, err)
		return
	}

	// Pick the smallest-panel talk per speaker. Key on Speaker.ID
	// when available, fall back to lower-cased name (older rows
	// may lack stable IDs).
	type pick struct {
		name    string
		clipart string
		panelN  int
	}
	best := map[string]*pick{}
	for _, talk := range talks {
		if talk == nil {
			continue
		}
		n := len(talk.Speakers)
		for _, sp := range talk.Speakers {
			if sp == nil {
				continue
			}
			key := sp.ID
			if key == "" {
				key = "name:" + strings.ToLower(strings.TrimSpace(sp.Name))
			}
			prev, ok := best[key]
			if !ok {
				best[key] = &pick{name: sp.Name, clipart: talk.Clipart, panelN: n}
				continue
			}
			// Fewer co-speakers wins. On tie, prefer the
			// non-empty clipart so a panel-with-art doesn't
			// lose to a same-size panel-without-art.
			if n < prev.panelN || (n == prev.panelN && prev.clipart == "" && talk.Clipart != "") {
				prev.clipart = talk.Clipart
				prev.panelN = n
				prev.name = sp.Name
			}
		}
	}

	rows := make([]*GiftRow, 0, len(best))
	for _, p := range best {
		rows = append(rows, &GiftRow{Clipart: p.clipart, SpeakerName: p.name})
	}

	// {conf}-staff Speakers row too — leading.png as their
	// clipart, skipped if they're already on a talk.
	for _, sp := range staffSpeakersForConf(ctx, conf.Tag) {
		if sp == nil {
			continue
		}
		key := sp.ID
		if key == "" {
			key = "name:" + strings.ToLower(strings.TrimSpace(sp.Name))
		}
		if _, ok := best[key]; ok {
			continue
		}
		best[key] = &pick{} // mark to dedupe across staff list itself
		rows = append(rows, &GiftRow{
			Clipart:     "leading.png",
			SpeakerName: sp.Name,
		})
	}

	sort.SliceStable(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].SpeakerName) < strings.ToLower(rows[j].SpeakerName)
	})

	if err := ctx.TemplateCache.ExecuteTemplate(w, "talks/gifts.tmpl", &TalksGiftsPage{
		Conf:     conf,
		Rows:     rows,
		FilePath: filePath,
		Year:     helpers.CurrentYear(),
	}); err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/gifts template: %s", conf.Tag, err)
	}
}

func SpeakerAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	id := requireConfStaff(w, r, ctx)
	if id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	// Iterate Proposals (filtered to this conf) so each row carries
	// the proposal IDs directly — Talk struct doesn't expose the
	// underlying Proposal.ID, and the admin edit-talk route needs
	// it. Each SpeakerConf on a proposal contributes one (speaker,
	// talk) pair; first-seen also pulls per-conf overrides from the
	// SpeakerConf (ComingFrom, Company, OrgPhoto override the
	// Speaker-level values).
	proposals := loadConfProposals(ctx, conf)
	rowByID := make(map[string]*SpeakerRow)
	for _, p := range proposals {
		if p == nil {
			continue
		}
		for _, sc := range resolveProposalSpeakers(p) {
			if sc == nil || sc.Speaker == nil {
				continue
			}
			sp := sc.Speaker
			row, ok := rowByID[sp.ID]
			if !ok {
				row = &SpeakerRow{
					ID:            sp.ID,
					Name:          sp.Name,
					Email:         sp.Email,
					Signal:        sp.Signal,
					Photo:         sp.Photo,
					Company:       sp.Company,
					OrgLogo:       sp.OrgLogo,
					SpeakerConfID: sc.ID,
					ComingFrom:    sc.ComingFrom,
				}
				if sc.Company != "" {
					row.Company = sc.Company
				}
				if sc.OrgPhoto != "" {
					row.OrgLogo = sc.OrgPhoto
				}
				rowByID[sp.ID] = row
			}
			row.Talks = append(row.Talks, &SpeakerRowTalk{
				ProposalID: p.ID,
				Title:      p.Title,
				Status:     p.Status,
			})
			if row.CardURL == "" {
				if ct := getters.FetchConfTalkByProposal(p.ID); ct != nil {
					row.CardURL = SpeakerCardURL(ctx, conf.Tag, "insta", sp.ID, ct.ID)
				}
			}
		}
	}
	rows := make([]*SpeakerRow, 0, len(rowByID))
	for _, r := range rowByID {
		// Mark speakers whose only attachments are soft statuses
		// so the page-level filter can collapse them. Zero-talk
		// rows stay onlySoft=false — they're a distinct edge
		// case (admin-imported but unattached) that we surface
		// separately via the "No talks attached" tag.
		if len(r.Talks) > 0 {
			allSoft := true
			for _, t := range r.Talks {
				if t.Status != "Waitlisted" && t.Status != "Invited" {
					allSoft = false
					break
				}
			}
			r.OnlySoftStatuses = allSoft
		}
		rows = append(rows, r)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})

	err = ctx.TemplateCache.ExecuteTemplate(w, "talks/speakers.tmpl", &SpeakerAdminPage{
		Conf:         conf,
		Rows:         rows,
		FlashMessage: r.URL.Query().Get("flash"),
		IsConfAdmin:  id.HasRoleForConf(conf.Tag, auth.RoleAdmin),
		Year:         helpers.CurrentYear(),
		EmailCompose: &EmailComposeData{
			Title:            "Email Selected Speakers",
			Description:      "Write a one-off email to speakers. Uses Go template syntax.",
			TitlePlaceholder: "Subject line",
			BodyPlaceholder:  "Hi {{ .Speaker.Name }},\n\nLooking forward to your talk at {{ .Conf.Desc }}...",
			Fields: []EmailFieldGroup{
				fieldGroup(".Speaker", types.Speaker{}, false),
				fieldGroup(".Conf", types.Conf{}, false),
				fieldGroup(".Talks", types.Talk{}, true),
			},
		},
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/talks/speakers template failed: %s", err.Error())
	}
}

func SpeakerAdminBulkEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	speakerRefs := r.Form["speaker_refs"]

	title := r.FormValue("title")
	body := r.FormValue("body")
	if title == "" || body == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=Title+and+body+required", conf.Tag), http.StatusSeeOther)
		return
	}

	// For test mode, don't require speaker selection
	testEmail := r.FormValue("test_email")
	isTest := r.FormValue("send_test") == "1" && testEmail != ""

	if len(speakerRefs) == 0 && !isTest {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=No+speakers+selected", conf.Tag), http.StatusSeeOther)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load talks", http.StatusInternalServerError)
		return
	}

	// Build speaker ID -> speaker and speaker ID -> talks maps
	speakerMap := make(map[string]*types.Speaker)
	speakerTalks := make(map[string][]*types.Talk)
	for _, talk := range talks {
		for _, s := range talk.Speakers {
			speakerMap[s.ID] = s
			speakerTalks[s.ID] = append(speakerTalks[s.ID], talk)
		}
	}

	refSet := make(map[string]bool, len(speakerRefs))
	for _, ref := range speakerRefs {
		refSet[ref] = true
	}

	if isTest {
		// Use first selected speaker, or first available if none selected
		var testSpeaker *types.Speaker
		var testTalks []*types.Talk
		for id, speaker := range speakerMap {
			if len(refSet) == 0 || refSet[id] {
				testSpeaker = speaker
				testTalks = speakerTalks[id]
				break
			}
		}
		if testSpeaker == nil {
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=No+speakers+available+for+test", conf.Tag), http.StatusSeeOther)
			return
		}
		ts := *testSpeaker
		ts.Email = testEmail
		_, err := emails.SendCustomToSpeaker(ctx, &ts, conf, testTalks, title, body)
		if err != nil {
			ctx.Err.Printf("/%s/admin/speakers/email test -> %s failed: %s", conf.Tag, testEmail, err)
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=Test+email+failed", conf.Tag), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=Test+sent+to+%s", conf.Tag, testEmail), http.StatusSeeOther)
		return
	}

	sent := 0
	for id, speaker := range speakerMap {
		if !refSet[id] {
			continue
		}
		if speaker.Email == "" {
			continue
		}
		_, err := emails.SendCustomToSpeaker(ctx, speaker, conf, speakerTalks[id], title, body)
		if err != nil {
			ctx.Err.Printf("/%s/admin/speakers/email -> %s failed: %s", conf.Tag, speaker.Email, err)
			continue
		}
		sent++
	}

	flash := fmt.Sprintf("Sent+to+%d+of+%d+speakers", sent, len(speakerRefs))
	http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=%s", conf.Tag, flash), http.StatusSeeOther)
}

func AdminSpeakerRefreshCards(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	speakerID := strings.TrimSpace(mux.Vars(r)["speakerID"])
	if speakerID == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=Missing+speaker", conf.Tag), http.StatusSeeOther)
		return
	}
	talks, err := talksForSpeakerMediaRefresh(ctx, conf, speakerID)
	if err != nil {
		ctx.Err.Printf("/%s/admin/speakers/%s/refresh-cards: %s", conf.Tag, speakerID, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=%s", conf.Tag, url.QueryEscape("Refresh failed: "+err.Error())), http.StatusSeeOther)
		return
	}
	if len(talks) == 0 {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=No+social+cards+for+speaker", conf.Tag), http.StatusSeeOther)
		return
	}
	RefreshTalkCardsForceOpt(ctx, talks, true)
	http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers?flash=%s", conf.Tag, url.QueryEscape(fmt.Sprintf("Force refreshed %d talk(s) for speaker.", len(talks)))), http.StatusSeeOther)
}

func SpeakerAdminNew(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	backURL := fmt.Sprintf("/%s/admin/speakers", conf.Tag)
	formAction := fmt.Sprintf("/%s/admin/speakers/new", conf.Tag)
	if r.Method == http.MethodPost {
		adminCreateSpeakerPOST(w, r, ctx, conf, backURL)
		return
	}
	page := &EditSpeakerPage{
		Mode:       "create",
		IsAdmin:    true,
		BackURL:    backURL,
		FormAction: formAction,
		Year:       helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "dashboard_edit_speaker.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/speakers/new render: %s", conf.Tag, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

func adminCreateSpeakerPOST(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, backURL string) {
	limitRequestBody(w, r, maxMultipartBodyBytes)
	if err := r.ParseMultipartForm(maxUploadFileBytes); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
	}
	name := strings.TrimSpace(r.FormValue("Name"))
	email := strings.TrimSpace(r.FormValue("Email"))
	if name == "" || email == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers/new?flash=%s", conf.Tag, url.QueryEscape("Name and email are required.")), http.StatusSeeOther)
		return
	}
	existing, err := getters.GetSpeakersByEmail(ctx, email)
	if err != nil {
		ctx.Err.Printf("/%s/admin/speakers/new lookup %s: %s", conf.Tag, email, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers/new?flash=%s", conf.Tag, url.QueryEscape("Speaker lookup failed: "+err.Error())), http.StatusSeeOther)
		return
	}
	if len(existing) > 0 && existing[0] != nil {
		sp := existing[0]
		flash := "Speaker already exists for " + email + ". Edit the existing profile, then attach them to a proposal."
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers/%s/edit?flash=%s", conf.Tag, sp.ID, url.QueryEscape(flash)), http.StatusSeeOther)
		return
	}
	picRaw, picContentType, picExt, picErr := readMultipartFile(r, "PicFile")
	hasNewPic := picErr == nil && len(picRaw) > 0
	if picErr != nil && picErr != http.ErrMissingFile {
		ctx.Err.Printf("/%s/admin/speakers/new read pic: %s", conf.Tag, picErr)
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers/new?flash=%s", conf.Tag, url.QueryEscape("Photo upload failed.")), http.StatusSeeOther)
		return
	}
	in := getters.SpeakerInput{
		Name:      name,
		Email:     email,
		Phone:     strings.TrimSpace(r.FormValue("Phone")),
		Signal:    strings.TrimSpace(r.FormValue("Signal")),
		Telegram:  strings.TrimSpace(r.FormValue("Telegram")),
		Twitter:   strings.TrimSpace(r.FormValue("Twitter")),
		Nostr:     strings.TrimSpace(r.FormValue("Nostr")),
		Github:    strings.TrimSpace(r.FormValue("Github")),
		Instagram: strings.TrimSpace(r.FormValue("Instagram")),
		LinkedIn:  strings.TrimSpace(r.FormValue("LinkedIn")),
		Website:   strings.TrimSpace(r.FormValue("Website")),
		TShirt:    validShirtCode(strings.TrimSpace(r.FormValue("TShirt"))),
	}
	if hasNewPic {
		in.Photo = imgproc.ShortID(picRaw) + picExt
	}
	speakerID, err := getters.CreateSpeaker(ctx, in)
	if err != nil {
		ctx.Err.Printf("/%s/admin/speakers/new create %s: %s", conf.Tag, email, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/speakers/new?flash=%s", conf.Tag, url.QueryEscape("Create failed: "+err.Error())), http.StatusSeeOther)
		return
	}
	if hasNewPic {
		go newPhotoPipeline(ctx).mirrorPicToSpaces(picRaw, picContentType, picExt)
	}
	http.Redirect(w, r, backURL+"?flash="+url.QueryEscape("Speaker created. Attach them to a proposal from the proposal editor. ID: "+speakerID), http.StatusSeeOther)
}

func SpeakerAdminEdit(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	speakerID := mux.Vars(r)["speakerID"]
	if !speakerIsOnConf(ctx, conf, speakerID) {
		http.Error(w, "speaker is not attached to this event", http.StatusForbidden)
		return
	}
	sp, err := getters.FetchSpeakerByID(ctx, speakerID)
	if err != nil {
		ctx.Err.Printf("/%s/admin/speakers/%s/edit load: %s", conf.Tag, speakerID, err)
		http.Error(w, "speaker lookup failed", http.StatusInternalServerError)
		return
	}
	if sp == nil {
		http.NotFound(w, r)
		return
	}

	backURL := fmt.Sprintf("/%s/admin/speakers", conf.Tag)
	formAction := fmt.Sprintf("/%s/admin/speakers/%s/edit", conf.Tag, speakerID)
	if r.Method == http.MethodPost {
		adminUpdateSpeakerPOST(w, r, ctx, conf, sp, backURL)
		return
	}
	page := &EditSpeakerPage{
		Speaker:      sp,
		Mode:         "edit",
		FlashMessage: r.URL.Query().Get("flash"),
		IsAdmin:      true,
		BackURL:      backURL,
		FormAction:   formAction,
		Year:         helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "dashboard_edit_speaker.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/speakers/%s/edit render: %s", conf.Tag, speakerID, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

func adminUpdateSpeakerPOST(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, sp *types.Speaker, backURL string) {
	limitRequestBody(w, r, maxMultipartBodyBytes)
	if err := r.ParseMultipartForm(maxUploadFileBytes); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
	}
	picRaw, picContentType, picExt, picErr := readMultipartFile(r, "PicFile")
	hasNewPic := picErr == nil && len(picRaw) > 0
	if picErr != nil && picErr != http.ErrMissingFile {
		ctx.Err.Printf("/%s/admin/speakers/%s/edit read pic: %s", conf.Tag, sp.ID, picErr)
		http.Redirect(w, r, backURL+"?flash="+url.QueryEscape("Photo upload failed."), http.StatusSeeOther)
		return
	}
	up := getters.SpeakerUpdate{
		Phone:     strings.TrimSpace(r.FormValue("Phone")),
		Signal:    strings.TrimSpace(r.FormValue("Signal")),
		Telegram:  strings.TrimSpace(r.FormValue("Telegram")),
		Twitter:   strings.TrimSpace(r.FormValue("Twitter")),
		Nostr:     strings.TrimSpace(r.FormValue("Nostr")),
		Github:    strings.TrimSpace(r.FormValue("Github")),
		Instagram: strings.TrimSpace(r.FormValue("Instagram")),
		LinkedIn:  strings.TrimSpace(r.FormValue("LinkedIn")),
		Website:   strings.TrimSpace(r.FormValue("Website")),
		TShirt:    validShirtCode(strings.TrimSpace(r.FormValue("TShirt"))),
	}
	if hasNewPic {
		up.Photo = imgproc.ShortID(picRaw) + picExt
	}
	if err := getters.UpdateSpeaker(ctx, sp.ID, up); err != nil {
		ctx.Err.Printf("/%s/admin/speakers/%s/edit update: %s", conf.Tag, sp.ID, err)
		http.Redirect(w, r, backURL+"?flash="+url.QueryEscape("Update failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	if hasNewPic {
		go newPhotoPipeline(ctx).mirrorPicToSpaces(picRaw, picContentType, picExt)
	}
	http.Redirect(w, r, backURL+"?flash="+url.QueryEscape("Speaker info updated."), http.StatusSeeOther)
}

func SpeakerConfAdminEdit(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	speakerConfID := mux.Vars(r)["speakerConfID"]
	sc, err := getters.FetchSpeakerConfWithSpeaker(ctx, speakerConfID)
	if err != nil {
		ctx.Err.Printf("/%s/admin/speakerconfs/%s/edit load: %s", conf.Tag, speakerConfID, err)
		http.Error(w, "speaker conf lookup failed", http.StatusInternalServerError)
		return
	}
	if sc == nil {
		http.NotFound(w, r)
		return
	}
	if scConf := speakerConfConf(sc); scConf == nil || scConf.Tag != conf.Tag {
		http.Error(w, "speaker conf is not attached to this event", http.StatusForbidden)
		return
	}

	backURL := fmt.Sprintf("/%s/admin/speakers", conf.Tag)
	formAction := fmt.Sprintf("/%s/admin/speakerconfs/%s/edit", conf.Tag, speakerConfID)
	if r.Method == http.MethodPost {
		adminUpdateSpeakerConfPOST(w, r, ctx, conf, sc, backURL)
		return
	}

	var returning bool
	if sc.Speaker != nil && sc.Speaker.Email != "" {
		if reg, err := getters.EmailHasRegistration(ctx, sc.Speaker.Email); err == nil {
			returning = reg
		}
	}
	rsvpDayList := conf.DaysList("", true)
	rsvpFor := ""
	if len(rsvpDayList) > 0 {
		rsvpFor = rsvpDayList[0].ItemDesc
	}
	page := &EditSpeakerConfPage{
		SpeakerConf:         sc,
		Conf:                conf,
		Locked:              false,
		DaysList:            conf.DaysList("", false),
		RecordingOptions:    helpers.GetRecordingOptions(),
		IsReturningAttendee: returning,
		RSVPFor:             rsvpFor,
		IsAdmin:             true,
		BackURL:             backURL,
		FormAction:          formAction,
		Year:                helpers.CurrentYear(),
	}
	if err := ctx.TemplateCache.ExecuteTemplate(w, "dashboard_edit_speakerconf.tmpl", page); err != nil {
		ctx.Err.Printf("/%s/admin/speakerconfs/%s/edit render: %s", conf.Tag, speakerConfID, err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

func adminUpdateSpeakerConfPOST(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, sc *types.SpeakerConf, backURL string) {
	limitRequestBody(w, r, maxMultipartBodyBytes)
	if err := r.ParseMultipartForm(maxUploadFileBytes); err != nil {
		ctx.Err.Printf("/%s/admin/speakerconfs/%s/edit parseform: %s", conf.Tag, sc.ID, err)
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	fields := getters.SpeakerConfFields{
		Company:      strings.TrimSpace(r.PostForm.Get("Company")),
		OrgID:        strings.TrimSpace(r.PostForm.Get("OrgID")),
		ComingFrom:   strings.TrimSpace(r.PostForm.Get("ComingFrom")),
		Availability: r.PostForm["Availability"],
		RecordOK:     strings.TrimSpace(r.PostForm.Get("RecordOK")),
		Visa:         strings.TrimSpace(r.PostForm.Get("Visa")),
		FirstEvent:   r.PostForm.Get("FirstEvent") == "on",
		DinnerRSVP:   r.PostForm.Get("DinnerRSVP") == "on",
		Sponsor:      r.PostForm.Get("Sponsor") == "on",
	}
	logoRaw, logoContentType, logoExt, logoErr := readMultipartFile(r, "OrgLogoFile")
	hasLogo := logoErr == nil && len(logoRaw) > 0
	if logoErr != nil && logoErr != http.ErrMissingFile {
		ctx.Err.Printf("/%s/admin/speakerconfs/%s/edit read logo: %s", conf.Tag, sc.ID, logoErr)
		http.Redirect(w, r, backURL+"?flash="+url.QueryEscape("Logo upload failed."), http.StatusSeeOther)
		return
	}
	if hasLogo {
		fields.OrgPhoto = imgproc.ShortID(logoRaw) + logoExt
	}
	if err := getters.UpdateSpeakerConf(ctx, sc.ID, fields); err != nil {
		ctx.Err.Printf("/%s/admin/speakerconfs/%s/edit update: %s", conf.Tag, sc.ID, err)
		http.Redirect(w, r, backURL+"?flash="+url.QueryEscape("Update failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	if hasLogo {
		go newPhotoPipeline(ctx).mirrorOrgLogoToSpaces(logoRaw, logoContentType, logoExt)
	}
	http.Redirect(w, r, backURL+"?flash="+url.QueryEscape("Speaker conf updated."), http.StatusSeeOther)
}

func speakerIsOnConf(ctx *config.AppContext, conf *types.Conf, speakerID string) bool {
	for _, p := range loadConfProposals(ctx, conf) {
		for _, sc := range resolveProposalSpeakers(p) {
			if sc != nil && sc.Speaker != nil && sc.Speaker.ID == speakerID {
				return true
			}
		}
	}
	return false
}

func RegistrationsAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	id := requireConfStaff(w, r, ctx)
	if id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	regs, err := getters.FetchRegistrations(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load registrations", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/registrations failed: %s", conf.Tag, err.Error())
		return
	}

	// Deduplicate by email
	seen := make(map[string]bool)
	var unique []*types.Registration
	for _, r := range regs {
		if r.Email != "" && !seen[r.Email] {
			seen[r.Email] = true
			unique = append(unique, r)
		}
	}

	regTypeOrder := map[string]int{
		"speaker":   0,
		"sponsor":   1,
		"volunteer": 2,
		"local":     3,
		"genpop":    4,
	}
	sort.SliceStable(unique, func(i, j int) bool {
		oi, oj := regTypeOrder[unique[i].Type], regTypeOrder[unique[j].Type]
		if oi != oj {
			return oi < oj
		}
		return unique[i].Email < unique[j].Email
	})

	err = ctx.TemplateCache.ExecuteTemplate(w, "admin/registrations.tmpl", &RegistrationsAdminPage{
		Conf:          conf,
		Registrations: unique,
		FlashMessage:  r.URL.Query().Get("flash"),
		IsConfAdmin:   id.HasRoleForConf(conf.Tag, auth.RoleAdmin),
		Year:          helpers.CurrentYear(),
		EmailCompose: &EmailComposeData{
			Title:            "Email Attendees",
			Description:      "Write a one-off email to registered attendees. Uses Go template syntax.",
			TitlePlaceholder: "Subject line",
			BodyPlaceholder:  "Hi there!\n\nExciting news about {{ .Conf.Desc }}...",
			Fields: []EmailFieldGroup{
				fieldGroup(".Conf", types.Conf{}, false),
			},
		},
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/registrations template failed: %s", conf.Tag, err.Error())
	}
}

func RegistrationsAdminBulkEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	selectedEmails := r.Form["reg_emails"]

	title := r.FormValue("title")
	body := r.FormValue("body")
	if title == "" || body == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/registrations?flash=Title+and+body+required", conf.Tag), http.StatusSeeOther)
		return
	}

	testEmail := r.FormValue("test_email")
	isTest := r.FormValue("send_test") == "1" && testEmail != ""

	if len(selectedEmails) == 0 && !isTest {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/registrations?flash=No+attendees+selected", conf.Tag), http.StatusSeeOther)
		return
	}

	// Load registrations
	regs, err := getters.FetchRegistrations(ctx, conf.Ref)
	if err != nil {
		http.Error(w, "Unable to load registrations", http.StatusInternalServerError)
		return
	}

	if isTest {
		// Use first available registration as sample data
		var testReg *types.Registration
		if len(selectedEmails) > 0 {
			for _, reg := range regs {
				if reg.Email == selectedEmails[0] {
					testReg = reg
					break
				}
			}
		}
		if testReg == nil && len(regs) > 0 {
			testReg = regs[0]
		}
		if testReg == nil {
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/registrations?flash=No+registrations+available", conf.Tag), http.StatusSeeOther)
			return
		}
		tr := *testReg
		tr.Email = testEmail
		_, err := emails.SendCustomToAttendee(ctx, &tr, conf, title, body)
		if err != nil {
			ctx.Err.Printf("/%s/admin/registrations/email test failed: %s", conf.Tag, err)
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/registrations?flash=Test+email+failed", conf.Tag), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/registrations?flash=Test+sent+to+%s", conf.Tag, testEmail), http.StatusSeeOther)
		return
	}

	emailSet := make(map[string]bool, len(selectedEmails))
	for _, e := range selectedEmails {
		emailSet[e] = true
	}

	// Deduplicate registrations by email for sending
	sent := 0
	sentEmails := make(map[string]bool)
	for _, reg := range regs {
		if !emailSet[reg.Email] || sentEmails[reg.Email] {
			continue
		}
		sentEmails[reg.Email] = true
		_, err := emails.SendCustomToAttendee(ctx, reg, conf, title, body)
		if err != nil {
			ctx.Err.Printf("/%s/admin/registrations/email -> %s failed: %s", conf.Tag, reg.Email, err)
			continue
		}
		sent++
	}

	flash := fmt.Sprintf("Sent+to+%d+of+%d+attendees", sent, len(selectedEmails))
	http.Redirect(w, r, fmt.Sprintf("/%s/admin/registrations?flash=%s", conf.Tag, flash), http.StatusSeeOther)
}

func ProposalAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	rows, err := loadProposalRowsForConf(ctx, conf)
	if err != nil {
		http.Error(w, "Unable to load applicants", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/applicants failed: %s", conf.Tag, err.Error())
		return
	}

	sort.SliceStable(rows, func(i, j int) bool {
		return speakerName(rows[i].Speaker) < speakerName(rows[j].Speaker)
	})

	err = ctx.TemplateCache.ExecuteTemplate(w, "talks/applicants.tmpl", &ProposalAdminPage{
		Conf:         conf,
		Rows:         rows,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
		EmailCompose: &EmailComposeData{
			Title:            "Email Selected Applicants",
			Description:      "Write a one-off email to speaker applicants. Uses Go template syntax.",
			TitlePlaceholder: "Subject line",
			BodyPlaceholder:  "Hi {{ .Speaker.Name }},\n\nThank you for applying to speak at {{ .Conf.Desc }}...",
			Fields: []EmailFieldGroup{
				fieldGroup(".Speaker", types.Speaker{}, false),
				fieldGroup(".Proposal", types.Proposal{}, false),
				fieldGroup(".Conf", types.Conf{}, false),
			},
		},
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/applicants template failed: %s", conf.Tag, err.Error())
	}
}

// loadProposalRowsForConf returns one ProposalAdminRow per Proposal
// whose ScheduleFor matches conf, joined to the Speakers that filed
// it via the SpeakerProposal link table and to the ConfTalk (when
// scheduled). Rows whose proposal has no SpeakerProposal still
// appear with Speakers == nil.
//
// Each row carries pre-computed display labels (StartLabel,
// VenueLabel, …) and a CalState flag derived from the stored
// CalNotif vs the freshly-computed content hash. CalState drives
// the per-card "Send / Resend / Update cal invite" button.
func loadProposalRowsForConf(ctx *config.AppContext, conf *types.Conf) ([]*ProposalAdminRow, error) {
	proposals, err := getters.ListProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list proposals: %w", err)
	}

	proposalMap := make(map[string]*types.Proposal)
	for _, p := range proposals {
		if p.ScheduleFor != nil && p.ScheduleFor.Ref == conf.Ref {
			proposalMap[p.ID] = p
		}
	}
	if len(proposalMap) == 0 {
		return nil, nil
	}

	speakers, err := getters.ListSpeakers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list speakers: %w", err)
	}
	speakerMap := make(map[string]*types.Speaker, len(speakers))
	for _, sp := range speakers {
		speakerMap[sp.ID] = sp
	}

	sps, err := getters.ListSpeakerConfs(ctx, speakerMap, proposalMap)
	if err != nil {
		return nil, fmt.Errorf("list speaker confs: %w", err)
	}

	// Each SpeakerConf carries one or more Proposals (multi-relation `talk`).
	// Build proposalID → []*Speaker so each card can list all collaborators.
	speakersByProposal := make(map[string][]*types.Speaker, len(proposalMap))
	for _, sp := range sps {
		if sp.Speaker == nil {
			continue
		}
		for _, p := range sp.Proposals {
			if p == nil {
				continue
			}
			speakersByProposal[p.ID] = append(speakersByProposal[p.ID], sp.Speaker)
		}
	}

	loc := conf.Loc()
	rows := make([]*ProposalAdminRow, 0, len(proposalMap))
	for _, p := range proposalMap {
		spList := speakersByProposal[p.ID]
		// Stable order so the "first speaker" picked for the
		// email-compose .Speaker field is deterministic.
		sort.SliceStable(spList, func(i, j int) bool {
			return strings.ToLower(spList[i].Name) < strings.ToLower(spList[j].Name)
		})
		row := &ProposalAdminRow{
			Proposal:           p,
			Speakers:           spList,
			DurationDesiredMin: p.DesiredDuration,
		}
		if len(spList) > 0 {
			row.Speaker = spList[0]
		}

		// Pull the ConfTalk if this proposal has been scheduled.
		// FetchConfTalkByProposal hits the warm cache; nil when
		// the proposal isn't in the schedule yet.
		if ct := getters.FetchConfTalkByProposal(p.ID); ct != nil {
			row.ConfTalk = ct
			row.TalkCardURL = TalkCardURL(ctx, conf.Tag, "1080p", ct.ID)
			if ct.Sched != nil {
				row.StartLabel = ct.Sched.Start.In(loc).Format("Mon Jan 2 · 3:04 PM")
				if ct.Sched.End != nil {
					row.EndLabel = ct.Sched.End.In(loc).Format("3:04 PM")
					row.DurationActualMin = int(ct.Sched.End.Sub(ct.Sched.Start).Minutes())
				}
			}
			row.VenueLabel = ics.MapVenue(ct.Venue)
			row.CalState = computeCalState(ct, p, conf)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// computeCalState classifies a ConfTalk's CalNotif against its
// current content for the per-card cal-invite button. Returns one of
// "none" (no CalNotif yet), "fresh" (CalNotif matches current data —
// idempotent re-send), or "stale" (data changed since last send,
// re-send will bump SEQUENCE).
func computeCalState(ct *types.ConfTalk, p *types.Proposal, conf *types.Conf) string {
	if ct == nil || ct.Sched == nil || ct.Sched.End == nil {
		return ""
	}
	prev, ok := ics.ParseCalNotif(ct.CalNotif)
	if !ok || prev.HashHex == "" {
		return "none"
	}
	cur := ics.ContentHash(ct.Sched.Start, *ct.Sched.End, conf.Tag, p.Title)
	if cur == prev.HashHex {
		return "fresh"
	}
	return "stale"
}

func AdminProposalRefreshCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	proposalID := strings.TrimSpace(mux.Vars(r)["proposalID"])
	talk, err := talkForProposalMediaRefresh(ctx, conf, proposalID)
	if err != nil {
		ctx.Err.Printf("/%s/admin/applicants/%s/refresh-card: %s", conf.Tag, proposalID, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=%s", conf.Tag, url.QueryEscape("Refresh failed: "+err.Error())), http.StatusSeeOther)
		return
	}
	RefreshTalkCardsForceOpt(ctx, []*types.Talk{talk}, true)
	http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=%s", conf.Tag, url.QueryEscape("Force refreshed card for "+talk.Name)), http.StatusSeeOther)
}

func talkForProposalMediaRefresh(ctx *config.AppContext, conf *types.Conf, proposalID string) (*types.Talk, error) {
	if proposalID == "" {
		return nil, fmt.Errorf("missing proposal")
	}
	proposals, err := getters.ListProposals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list proposals: %w", err)
	}
	proposalMap := make(map[string]*types.Proposal, len(proposals))
	for _, p := range proposals {
		if p != nil {
			proposalMap[p.ID] = p
		}
	}
	proposal := proposalMap[proposalID]
	if proposal == nil {
		return nil, fmt.Errorf("proposal not found")
	}
	if proposal.ScheduleFor == nil || proposal.ScheduleFor.Ref != conf.Ref {
		return nil, fmt.Errorf("proposal is not attached to %s", conf.Tag)
	}
	confTalks, err := getters.ListConfTalks(ctx, proposalMap)
	if err != nil {
		return nil, fmt.Errorf("list conf talks: %w", err)
	}
	var target *types.ConfTalk
	for _, ct := range confTalks {
		if ct != nil && ct.Proposal != nil && ct.Proposal.ID == proposalID {
			target = ct
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("proposal is not scheduled yet")
	}
	talks, err := getters.LoadTalksFromConfTalks(ctx, conf.Tag)
	if err != nil {
		return nil, fmt.Errorf("load talks: %w", err)
	}
	for _, talk := range talks {
		if talk != nil && talk.ID == target.ID {
			return talk, nil
		}
	}
	return nil, fmt.Errorf("scheduled talk card source not found")
}

func talksForSpeakerMediaRefresh(ctx *config.AppContext, conf *types.Conf, speakerID string) ([]*types.Talk, error) {
	var out []*types.Talk
	for _, p := range loadConfProposals(ctx, conf) {
		if p == nil {
			continue
		}
		var matched bool
		for _, sc := range resolveProposalSpeakers(p) {
			if sc != nil && sc.Speaker != nil && sc.Speaker.ID == speakerID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		ct := getters.FetchConfTalkByProposal(p.ID)
		if ct == nil {
			continue
		}
		out = append(out, talkForAdminMediaRefresh(conf, p, ct))
	}
	return out, nil
}

func talkForAdminMediaRefresh(conf *types.Conf, proposal *types.Proposal, ct *types.ConfTalk) *types.Talk {
	talk := &types.Talk{
		ID:          ct.ID,
		Name:        proposal.Title,
		Description: proposal.Description,
		Type:        proposal.TalkType,
		Status:      proposal.Status,
		Event:       conf.Tag,
		Clipart:     ct.Clipart,
		Sched:       ct.Sched,
		Venue:       ct.Venue,
		Section:     ct.Section,
		CalNotif:    ct.CalNotif,
		TalkCardURL: ct.SocialCard,
	}
	if talk.Sched != nil {
		talk.TimeDesc = talk.Sched.Desc()
	}
	for _, sc := range resolveProposalSpeakers(proposal) {
		if sc == nil || sc.Speaker == nil {
			continue
		}
		view := *sc.Speaker
		if sc.Company != "" {
			view.Company = sc.Company
		}
		if sc.OrgPhoto != "" {
			view.OrgLogo = sc.OrgPhoto
		}
		talk.Speakers = append(talk.Speakers, &view)
	}
	return talk
}

func speakerName(sp *types.Speaker) string {
	if sp == nil {
		return ""
	}
	return sp.Name
}

// AdminProposalSendCalAll fires cal invites for every scheduled
// proposal on the page where the data hasn't drifted since the
// last send (CalState in {none, fresh}; "stale" is skipped). The
// "none" rows produce first-send emails (seq=0). "fresh" rows hit
// the hash-unchanged short-circuit inside dispatch and don't email
// anyone. "stale" is deliberately excluded so an admin reviews
// pending changes individually via the per-card "Update cal
// invite" button.
//
// Path: POST /{conf}/admin/applicants/sendcal-all
func AdminProposalSendCalAll(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	rows, err := loadProposalRowsForConf(ctx, conf)
	if err != nil {
		ctx.Err.Printf("/%s/admin/applicants/sendcal-all load rows: %s", conf.Tag, err)
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/applicants?flash=%s",
				conf.Tag, url.QueryEscape("Bulk sendcal failed: "+err.Error())),
			http.StatusSeeOther)
		return
	}

	var attempted, sent, skippedStale, failed int
	for _, row := range rows {
		if row == nil || row.Proposal == nil {
			continue
		}
		// Skip unscheduled proposals — no ConfTalk → no
		// CalState → no cal-invite to send.
		if row.CalState == "" {
			continue
		}
		// Skip stale: data has drifted since the last send,
		// the admin should review and click "Update cal
		// invite" individually rather than push a silent
		// SEQUENCE bump out via the bulk button.
		if row.CalState == "stale" {
			skippedStale++
			continue
		}
		attempted++
		// force=false: "fresh" rows hit the hash-unchanged
		// no-op inside dispatch and don't email; "none" rows
		// fire seq=0 first-sends.
		if err := DispatchTalkICSForProposal(ctx, row.Proposal, conf, row.Speakers, false); err != nil {
			ctx.Err.Printf("sendcal-all %q: %s", row.Proposal.Title, err)
			failed++
			continue
		}
		// "fresh" returns nil from dispatch but didn't actually
		// email; only count "none" as a real send. We can tell
		// them apart by the entering CalState.
		if row.CalState == "none" {
			sent++
			// First-send locks the schedule in: flip
			// Accepted → Scheduled.
			if row.Proposal.Status == StatusAccepted {
				if err := getters.UpdateProposalStatus(ctx, row.Proposal.ID, StatusScheduled); err != nil {
					ctx.Err.Printf("sendcal-all %q status flip: %s", row.Proposal.Title, err)
				}
			}
		}
	}

	flash := fmt.Sprintf("Bulk cal invites: %d sent · %d already current · %d pending updates skipped",
		sent, attempted-sent-failed, skippedStale)
	if failed > 0 {
		flash = fmt.Sprintf("%s · %d failed (see logs)", flash, failed)
	}
	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/applicants?flash=%s",
			conf.Tag, url.QueryEscape(flash)),
		http.StatusSeeOther)
}

// AdminProposalSendCal handles the per-card "Send / Resend /
// Update cal invite" button on /{conf}/admin/applicants. Looks up
// the proposal's ConfTalk + speakers and fires
// DispatchTalkICSForProposal. force=true is implied — clicking the
// button means the admin wants a send regardless of whether the
// content hash changed (the button label tells the admin which
// state they're in; the backend always honors the click).
//
// Path: POST /{conf}/admin/proposals/{proposalID}/sendcal
func AdminProposalSendCal(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	proposalID := mux.Vars(r)["proposalID"]
	if proposalID == "" {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/applicants?flash=Missing+proposal", conf.Tag),
			http.StatusSeeOther)
		return
	}

	proposal, err := getters.GetProposal(ctx, proposalID)
	if err != nil || proposal == nil {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/applicants?flash=Proposal+not+found", conf.Tag),
			http.StatusSeeOther)
		return
	}

	speakers := proposalSpeakers(proposal)
	if err := DispatchTalkICSForProposal(ctx, proposal, conf, speakers, true); err != nil {
		ctx.Err.Printf("/%s/admin/proposals/%s/sendcal: %s", conf.Tag, proposalID, err)
		http.Redirect(w, r,
			fmt.Sprintf("/%s/admin/applicants?flash=%s",
				conf.Tag, url.QueryEscape("Cal invite failed: "+err.Error())),
			http.StatusSeeOther)
		return
	}

	// Sending the cal invite is what locks the talk in: flip
	// Accepted (draft) → Scheduled. Re-sends and updates leave
	// the status alone (already Scheduled). Best-effort — the
	// dispatch already succeeded, so log the status-write
	// failure but still tell the admin the invite went out.
	if proposal.Status == StatusAccepted {
		if err := getters.UpdateProposalStatus(ctx, proposalID, StatusScheduled); err != nil {
			ctx.Err.Printf("/%s/admin/proposals/%s/sendcal status flip: %s", conf.Tag, proposalID, err)
		}
	}

	http.Redirect(w, r,
		fmt.Sprintf("/%s/admin/applicants?flash=%s",
			conf.Tag,
			url.QueryEscape(fmt.Sprintf("Cal invite sent for %q to %d speaker(s).", proposal.Title, len(speakers)))),
		http.StatusSeeOther)
}

// proposalSpeakers walks proposal.SpeakerConfRefs through the
// warm SpeakerConf cache and returns the *Speaker for each. Used
// by per-proposal cal-invite dispatch where the page-level
// speakers map isn't already in scope. Distinct from
// resolveProposalSpeakers (admin_review.go), which returns
// SpeakerConf wrappers — here we only need the bare Speaker.
func proposalSpeakers(proposal *types.Proposal) []*types.Speaker {
	if proposal == nil {
		return nil
	}
	out := make([]*types.Speaker, 0, len(proposal.SpeakerConfRefs))
	for _, ref := range proposal.SpeakerConfRefs {
		sc := getters.FetchSpeakerConfByID(ref)
		if sc == nil || sc.Speaker == nil {
			continue
		}
		out = append(out, sc.Speaker)
	}
	return out
}

func ProposalAdminBulkEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	proposalRefs := r.Form["proposal_refs"]
	if len(proposalRefs) == 0 {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=No+applicants+selected", conf.Tag), http.StatusSeeOther)
		return
	}

	title := r.FormValue("title")
	body := r.FormValue("body")
	if title == "" || body == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=Title+and+body+required", conf.Tag), http.StatusSeeOther)
		return
	}

	rows, err := loadProposalRowsForConf(ctx, conf)
	if err != nil {
		http.Error(w, "Unable to load applicants", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/admin/applicants/email failed: %s", conf.Tag, err.Error())
		return
	}

	rowByID := make(map[string]*ProposalAdminRow, len(rows))
	for _, row := range rows {
		rowByID[row.Proposal.ID] = row
	}

	refSet := make(map[string]bool, len(proposalRefs))
	for _, ref := range proposalRefs {
		refSet[ref] = true
	}

	testEmail := r.FormValue("test_email")
	isTest := r.FormValue("send_test") == "1" && testEmail != ""

	if isTest {
		for _, ref := range proposalRefs {
			row := rowByID[ref]
			if row == nil || row.Speaker == nil {
				continue
			}
			testSpeaker := *row.Speaker
			testSpeaker.Email = testEmail
			_, err := emails.SendCustomToProposalSpeaker(ctx, row.Proposal, &testSpeaker, conf, title, body)
			if err != nil {
				http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=Test+email+failed:+%s", conf.Tag, err.Error()), http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=Test+sent+to+%s", conf.Tag, testEmail), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=No+applicant+selected+for+test", conf.Tag), http.StatusSeeOther)
		return
	}

	sent := 0
	for _, ref := range proposalRefs {
		row := rowByID[ref]
		if row == nil || row.Speaker == nil || row.Speaker.Email == "" {
			continue
		}
		_, err := emails.SendCustomToProposalSpeaker(ctx, row.Proposal, row.Speaker, conf, title, body)
		if err != nil {
			ctx.Err.Printf("/%s/admin/applicants/email -> %s failed: %s", conf.Tag, row.Speaker.Email, err)
			continue
		}
		sent++
	}

	flash := fmt.Sprintf("Sent+to+%d+of+%d+applicants", sent, len(proposalRefs))
	http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=%s", conf.Tag, flash), http.StatusSeeOther)
}

// ProposalAdminAccept flips a Proposal to Accepted and creates a ConfTalk row.
// Always redirects back to the applicants page with a flash describing the
// outcome.
func ProposalAdminAccept(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfAdmin(w, r, ctx); id == nil {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	limitRequestBody(w, r, maxFormBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	proposalID := r.FormValue("proposal_ref")
	if proposalID == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=No+proposal+selected", conf.Tag), http.StatusSeeOther)
		return
	}

	result, err := newAcceptPipeline(ctx).AcceptProposal(proposalID)
	if err != nil {
		ctx.Err.Printf("/%s/admin/applicants/accept (%s) failed: %s", conf.Tag, proposalID, err)
		flash := url.QueryEscape(fmt.Sprintf("Accept failed: %s", err.Error()))
		http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=%s", conf.Tag, flash), http.StatusSeeOther)
		return
	}

	var msg string
	if result.AlreadyAccepted {
		msg = "Already accepted"
	} else {
		msg = "Accepted: created conf talk"
	}
	http.Redirect(w, r, fmt.Sprintf("/%s/admin/applicants?flash=%s", conf.Tag, url.QueryEscape(msg)), http.StatusSeeOther)
}

// SendVolCals fans the self-hosted ICS pipeline across every
// SendVolOrientation broadcasts the volunteer-orientation invite
// to every Scheduled volunteer for a conf. Used by the
// "Resend orientation invite" button on /{conf}/volcoord when the
// orientation time has changed and the admin wants the new time
// to land in every volunteer's calendar in one click.
//
// Unlike the per-vol DispatchOrientICS (fires from scheduledFlow,
// hash-gated), this is an explicit force-send: SEQUENCE bumps
// once and the same seq lands on every recipient. Conf.OrientCalNotif
// stamps the new state.
//
// Path: POST /{conf}/volcoord/send-orientation
func SendVolOrientation(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
	if err != nil || volinfo == nil || volinfo.OrientTimes == nil || volinfo.OrientTimes.End == nil {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag,
				url.QueryEscape("No orientation time set — add it on the conf's VolInfo row first.")),
			http.StatusSeeOther)
		return
	}

	vols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/send-orientation list vols: %s", conf.Tag, err)
		http.Error(w, "Unable to load volunteers", http.StatusInternalServerError)
		return
	}

	recipients := make([]ics.Attendee, 0, len(vols))
	for _, v := range vols {
		if v == nil || v.Email == "" {
			continue
		}
		// Only Scheduled vols got an orientation invite the
		// first time around (DispatchOrientICS fires inside
		// scheduledFlow, post-status flip). Mirror that here
		// so the broadcast doesn't blast unscheduled
		// applicants who never received the original.
		if v.Status != "Scheduled" {
			continue
		}
		recipients = append(recipients, ics.Attendee{Email: v.Email, Name: v.Name})
	}

	recipients = append(recipients, orientationStaffRecipients(ctx, conf.Tag)...)
	recipients = dedupeAttendees(recipients)

	if len(recipients) == 0 {
		http.Redirect(w, r,
			fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag,
				url.QueryEscape("No Scheduled volunteers or staff/admin recipients to notify.")),
			http.StatusSeeOther)
		return
	}

	sent, err := BroadcastOrientICS(ctx, conf, volinfo.OrientTimes.Start, *volinfo.OrientTimes.End, volinfo.OrientLink, recipients)
	if err != nil && sent == 0 {
		ctx.Err.Printf("/%s/volcoord/send-orientation: %s", conf.Tag, err)
		http.Redirect(w, r,
			fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag,
				url.QueryEscape("Orientation broadcast failed: "+err.Error())),
			http.StatusSeeOther)
		return
	}

	http.Redirect(w, r,
		fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag,
			url.QueryEscape(fmt.Sprintf("Orientation invite re-sent to %d recipient(s).", sent))),
		http.StatusSeeOther)
}

func VolAdminScheduleOrientation(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfVolcoord(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Bad orientation form.")), http.StatusSeeOther)
		return
	}
	start, err := parseOrientationTime(r.FormValue("start"), conf)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Orientation start time is required.")), http.StatusSeeOther)
		return
	}
	end, err := parseOrientationTime(r.FormValue("end"), conf)
	if err != nil || !end.After(start) {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Orientation end time must be after the start time.")), http.StatusSeeOther)
		return
	}
	orientLink := strings.TrimSpace(r.FormValue("orient_link"))
	if orientLink == "" {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Orientation link is required.")), http.StatusSeeOther)
		return
	}
	volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
	if err != nil || volinfo == nil {
		ctx.Err.Printf("/%s/volcoord/orientation volinfo: %s", conf.Tag, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("No VolInfo row found for this conference.")), http.StatusSeeOther)
		return
	}
	if err := getters.UpdateVolInfoOrientation(ctx, volinfo.Ref, start, end, orientLink); err != nil {
		ctx.Err.Printf("/%s/volcoord/orientation update: %s", conf.Tag, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Orientation update failed: "+err.Error())), http.StatusSeeOther)
		return
	}
	vols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/orientation list vols: %s", conf.Tag, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Orientation saved, but volunteers could not be loaded.")), http.StatusSeeOther)
		return
	}
	recipients := dedupeAttendees(append(scheduledVolunteerAttendees(vols), orientationStaffRecipients(ctx, conf.Tag)...))
	if len(recipients) == 0 {
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Orientation saved. No Scheduled volunteers or staff/admin recipients to notify.")), http.StatusSeeOther)
		return
	}
	sent, err := BroadcastOrientICS(ctx, conf, start, end, orientLink, recipients)
	if err != nil && sent == 0 {
		ctx.Err.Printf("/%s/volcoord/orientation broadcast: %s", conf.Tag, err)
		http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape("Orientation saved, but invite send failed: "+err.Error())), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/%s/volcoord?flash=%s", conf.Tag, url.QueryEscape(fmt.Sprintf("Orientation scheduled and invite sent to %d recipient(s).", sent))), http.StatusSeeOther)
}

func orientationInputValues(volinfo *types.VolInfo, conf *types.Conf) (string, string) {
	if volinfo == nil || volinfo.OrientTimes == nil {
		return "", ""
	}
	loc := time.Local
	if conf != nil {
		loc = conf.Loc()
	}
	start := volinfo.OrientTimes.Start.In(loc).Format("2006-01-02T15:04")
	end := ""
	if volinfo.OrientTimes.End != nil {
		end = volinfo.OrientTimes.End.In(loc).Format("2006-01-02T15:04")
	}
	return start, end
}

func parseOrientationTime(raw string, conf *types.Conf) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty orientation time")
	}
	loc := time.Local
	if conf != nil {
		loc = conf.Loc()
	}
	return time.ParseInLocation("2006-01-02T15:04", raw, loc)
}

func scheduledVolunteerAttendees(vols []*types.Volunteer) []ics.Attendee {
	out := make([]ics.Attendee, 0, len(vols))
	for _, v := range vols {
		if v == nil || v.Email == "" || v.Status != "Scheduled" {
			continue
		}
		out = append(out, ics.Attendee{Email: v.Email, Name: v.Name})
	}
	return out
}

func orientationStaffRecipients(ctx *config.AppContext, confTag string) []ics.Attendee {
	speakers, err := getters.FetchSpeakersCached(ctx)
	if err != nil || len(speakers) == 0 {
		return nil
	}
	out := make([]ics.Attendee, 0)
	for _, sp := range speakers {
		if sp == nil || sp.Email == "" || !speakerGetsOrientationStaffInvite(sp, confTag) {
			continue
		}
		out = append(out, ics.Attendee{Email: sp.Email, Name: sp.Name})
	}
	return out
}

func speakerGetsOrientationStaffInvite(sp *types.Speaker, confTag string) bool {
	for _, role := range auth.ParseRoles(sp.Roles) {
		if role.Scope != auth.GlobalScope && role.Scope != confTag {
			continue
		}
		switch role.Name {
		case auth.RoleAdmin, auth.RoleVolcoord, auth.RoleStaff:
			return true
		}
	}
	return false
}

func dedupeAttendees(in []ics.Attendee) []ics.Attendee {
	seen := map[string]bool{}
	out := make([]ics.Attendee, 0, len(in))
	for _, a := range in {
		email := strings.ToLower(strings.TrimSpace(a.Email))
		if email == "" || seen[email] {
			continue
		}
		seen[email] = true
		a.Email = strings.TrimSpace(a.Email)
		out = append(out, a)
	}
	return out
}

// scheduled volunteer shift for a conf. Mirrors SendCals on the
// volunteer side; per-shift CalNotif now stores the "UID:Sequence:
// Hashbytes" triple. Idempotent re-clicks skip emails when nothing
// changed.
func SendVolCals(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/sendcal failed to get shifts: %s", conf.Tag, err.Error())
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		return
	}

	vols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/%s/volcoord/sendcal failed to get volunteers: %s", conf.Tag, err.Error())
		http.Error(w, "Unable to load volunteers", http.StatusInternalServerError)
		return
	}

	// Build a map of volunteer ref -> attendee (email + name) so
	// the rendered ICS carries CN= alongside mailto:.
	volByRef := make(map[string]ics.Attendee)
	for _, vol := range vols {
		if vol == nil || vol.Email == "" {
			continue
		}
		volByRef[vol.Ref] = ics.Attendee{Email: vol.Email, Name: vol.Name}
	}

	for _, shift := range shifts {
		if len(shift.AssigneesRef) == 0 {
			continue
		}
		if shift.ShiftTime == nil || shift.ShiftTime.End == nil {
			ctx.Err.Printf("Skipping shift %s: no end time", shift.Name)
			continue
		}

		recipients := make([]ics.Attendee, 0, len(shift.AssigneesRef))
		for _, ref := range shift.AssigneesRef {
			if a, ok := volByRef[ref]; ok {
				recipients = append(recipients, a)
			}
		}
		if len(recipients) == 0 {
			continue
		}

		if err := DispatchShiftICS(ctx, shift, conf, recipients, kindRequest, false); err != nil {
			ctx.Err.Printf("vol sendcal %q: %s", shift.Name, err)
		}
	}
}
