package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
        "mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/google"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/helpers"
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

var pages []string = []string{"index", "about", "sponsor", "contact", "press", "vegas25" }

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
		"safesrc": func(s string) template.HTMLAttr {
			return template.HTMLAttr(fmt.Sprintf(`src="%s"`, s))
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
func Routes(app *config.AppContext) (http.Handler, error) {
	r := mux.NewRouter()

	err := loadTemplates(app)
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

	/* List of 'normie' pages */
	for _, page := range pages {
		/* Normie Pages */
		renderPage := page
		r.HandleFunc("/"+renderPage, func(w http.ResponseWriter, r *http.Request) {
			RenderPage(w, r, app, renderPage)
		}).Methods("GET")
	}

	/* Legacy redirects! */
	r.HandleFunc("/berlin23", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/berlin23", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/ecash", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/berlin24", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/mempool", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/atx25", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/atx24", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/atx24", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/riga", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/riga", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/conf/lightning", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/berlin25", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/privacy", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/riga", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/istanbul", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/istanbul", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/taipei", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/taipei", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/nairobi", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/nairobi", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/lightning", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/berlin25", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/ba24", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/ba24", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/berlin23/talks", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/berlin23/talks", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/talks", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}).Methods("GET")

	r.HandleFunc("/exploits", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/floripa26", http.StatusSeeOther)
	}).Methods("GET")

	r.HandleFunc("/floripa", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/floripa26", http.StatusSeeOther)
	}).Methods("GET")

	r.HandleFunc("/conf/floripa", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/floripa26", http.StatusSeeOther)
	}).Methods("GET")

	r.HandleFunc("/conf/{conf}/success", func(w http.ResponseWriter, r *http.Request) {
		RenderConfSuccess(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/conf/{conf}/talks", func(w http.ResponseWriter, r *http.Request) {
		RenderTalks(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/conf/{conf}", func(w http.ResponseWriter, r *http.Request) {
		RenderConf(w, r, app)
	}).Methods("GET")

        r.HandleFunc("/volunteer", func (w http.ResponseWriter, r *http.Request) {
                RenderVolunteers(w, r, app)
        }).Methods("GET")

        r.HandleFunc("/volunteer/{conf}", func (w http.ResponseWriter, r *http.Request) {
                RenderVolunteerConf(w, r, app)
        }).Methods("GET", "POST")

        r.HandleFunc("/talk", func (w http.ResponseWriter, r *http.Request) {
                RenderSpeakers(w, r, app)
        }).Methods("GET")

        r.HandleFunc("/talk/{conf}", func (w http.ResponseWriter, r *http.Request) {
                RenderSpeakerConf(w, r, app)
        }).Methods("GET", "POST")

	r.HandleFunc("/tix/{tix}/collect-email", func(w http.ResponseWriter, r *http.Request) {
		HandleEmail(w, r, app)
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

	r.HandleFunc("/auth-login", func(w http.ResponseWriter, r *http.Request) {
		redirectTo := app.Session.GetString(r.Context(), "r")
		app.Infos.Printf("login called, redirect to %s", redirectTo)
		google.HandleLogin(w, r, app, redirectTo)
	}).Methods("GET")

	r.HandleFunc("/gcal-callback", func(w http.ResponseWriter, r *http.Request) {
		redirectTo := app.Session.GetString(r.Context(), "r")
		app.Infos.Printf("gcal-callback called, redirect to %s", redirectTo)

		ok := google.HandleLoginCallback(w, r, app)
		if ok {
			http.Redirect(w, r, redirectTo, http.StatusFound)
		}
		// FIXME: what if not ok to login?
	}).Methods("GET")

	r.HandleFunc("/i/{conf}/sendcal", func(w http.ResponseWriter, r *http.Request) {
		/* Check for verified */
		if ok := helpers.CheckPin(w, r, app); !ok {
			helpers.Render401(w, r, app)
			return
		}

		if !google.IsLoggedIn() {
			app.Session.Put(r.Context(), "r", r.URL.Path)
			http.Redirect(w, r, "/auth-login", http.StatusFound)
			return
		}

		SendCals(w, r, app)
	}).Methods("GET", "POST")

	AddMediaRoutes(r, app)

	r.HandleFunc("/ticket/{ticket}", func(w http.ResponseWriter, r *http.Request) {
		Ticket(w, r, app)
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
	r.HandleFunc("/vols/admin/{conf}", func(w http.ResponseWriter, r *http.Request) {
		VolAdmin(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/vols/admin/{conf}/promote", func(w http.ResponseWriter, r *http.Request) {
		VolAdminPromote(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/auto-assign", func(w http.ResponseWriter, r *http.Request) {
		VolAdminAutoAssign(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/shifts", func(w http.ResponseWriter, r *http.Request) {
		VolAdminShifts(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/vols/admin/{conf}/shifts/new", func(w http.ResponseWriter, r *http.Request) {
		VolAdminCreateShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/shifts/{shiftRef}/update", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateShift(w, r, app)
	}).Methods("POST")


	r.HandleFunc("/vols/admin/{conf}/vol/{volRef}", func(w http.ResponseWriter, r *http.Request) {
		VolAdminDetails(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/vols/admin/{conf}/vol/{volRef}/status", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateStatus(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/vol/{volRef}/availability", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateAvailability(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/vol/{volRef}/work-prefs", func(w http.ResponseWriter, r *http.Request) {
		VolAdminUpdateWorkPrefs(w, r, app)
	}).Methods("POST")


	r.HandleFunc("/vols/admin/{conf}/vol/{volRef}/add-shift", func(w http.ResponseWriter, r *http.Request) {
		VolAdminAddShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/vol/{volRef}/remove-shift", func(w http.ResponseWriter, r *http.Request) {
		VolAdminRemoveShift(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/vol/{volRef}/scheduled", func(w http.ResponseWriter, r *http.Request) {
		VolAdminMarkScheduled(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/admin/{conf}/email", func(w http.ResponseWriter, r *http.Request) {
		VolAdminBulkEmail(w, r, app)
	}).Methods("POST")

	r.HandleFunc("/vols/shift", func(w http.ResponseWriter, r *http.Request) {
		VolunteerShift(w, r, app)
	}).Methods("GET", "POST")

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


	r.HandleFunc("/talks/gifts", func(w http.ResponseWriter, r *http.Request) {
		TalksGifts(w, r, app)
	}).Methods("GET")

	// Create a file server to serve static files from the "static" directory
	fs := http.FileServer(http.Dir("static"))
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fs))
	err = addFaviconRoutes(r)

	if err != nil {
		return r, err
	}

	return r, err
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
	w.WriteHeader(http.StatusNotFound)
	ctx.Infos.Printf("404'd: %s", r.URL.Path)

	RenderPage(w, r, ctx, "404")
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

func ReloadConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* Check for verified */
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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

func filterSpeakers(talks []*types.Talk) types.Speakers {
	var speakers types.Speakers
	already := make(map[string]int)

	for _, talk := range talks {
		for _, speaker := range talk.Speakers {
			if _, ok := already[speaker.ID]; !ok {
				speakers = append(speakers, speaker)
				already[speaker.ID] = 1
			}
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

	var talks types.TalkTime
	talks, err = getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	var evSpeakers types.Speakers
	evSpeakers = filterSpeakers(talks)

	sort.Sort(talks)
	sort.Sort(evSpeakers)

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

	err = ctx.TemplateCache.ExecuteTemplate(w, "success.tmpl", &SuccessPage{
		Conf: conf,
		Year: helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/conf/%s/success ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func RenderSpeakers(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
        confs := listConfs(w, ctx)
	err := ctx.TemplateCache.ExecuteTemplate(w, "embeds/speaker_select.tmpl", &VolunteerPage{
		Confs: confs,
		Year: helpers.CurrentYear(),
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
        
        return getters.UploadFile(ctx.Notion, contentType, filename, fileData)
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

                talksDue := 45
                if conf.Tag == "floripa26" {
                        talksDue = 21
                }
                daylist := conf.DaysList("days-", true)
                err = ctx.TemplateCache.ExecuteTemplate(w, "embeds/talk.tmpl", &SpeakerPage{
                        Conf: conf,
                        Confs: confs,
                        ConfItems: helpers.GetOtherConfs(confs, *conf),
                        DueDate: conf.DateBeforeStart(talksDue),
                        DaysList:  daylist[1:],
                        RSVPFor: daylist[0].ItemDesc,
                        PresentationType: helpers.GetPresentationTypes(),
                        Year: helpers.CurrentYear(),
                })

                if err != nil {
                        http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
                        ctx.Err.Printf("/volunteer/%s ExecuteTemplate failed ! %s", conf.Tag, err.Error())
                        return
                }
                        return
        case http.MethodPost:
                err = r.ParseMultipartForm(10 << 20) // Limit uploads to 10MB
                if err != nil {
			ctx.Err.Printf("/talk/{conf} unable to parse multipart form %s", err)
                        w.Write([]byte(helpers.ErrTalkApp("Error parsing form.")))
			return
                }

		dec := schema.NewDecoder()
		dec.IgnoreUnknownKeys(true)
		var talkapp types.TalkApp
		err = dec.Decode(&talkapp, r.PostForm)
		if err != nil {
			ctx.Err.Printf("/speaker/{conf} unable to decode form %s", err)
                        w.Write([]byte(helpers.ErrTalkApp("Unable to register you: form parsing error")))
			return
		}

                /* ten divided by two is five */
                if talkapp.Captcha != 5 {
                        w.Write([]byte(helpers.ErrTalkApp("Incorrect captcha. The answer is 5.")))
			return
                }

                talkapp.ParseAvailability("days-", r.PostForm)
                dinneropt := r.PostForm.Get("DinnerOpt")
                talkapp.DinnerRSVP = dinneropt == "Yes"
                talkapp.OtherEvents = helpers.ParseFormConfs("conf-", r.PostForm, confs)

                /* Upload pics */
                talkapp.Pic, err = processFileUpload(ctx, r, "PicFile")
                if err != nil {
			ctx.Err.Printf("/talk/{conf} unable to upload speaker profile pic %s", err)
                        w.Write([]byte(helpers.ErrTalkApp("Error uploading pfp.")))
			return
                }

                talkapp.OrgLogo, err = processFileUpload(ctx, r, "OrgLogoFile")
                if err != nil && err != http.ErrMissingFile {
                        ctx.Err.Printf("/talk/{conf} unable to upload org logo %s", err)

                        w.Write([]byte(helpers.ErrTalkApp("Error uploading org logo.")))
                        return
                }
        
                if len(talkapp.ScheduleFor) == 0 {
                        talkapp.ScheduleFor = append(talkapp.ScheduleFor, conf)
                }

                ctx.Infos.Printf("parsed talkapp: %v", talkapp)

                err = getters.RegisterTalkApp(ctx.Notion, &talkapp)
                if err != nil {
			ctx.Err.Printf("/talk/{conf} unable to register speaker %s", err)
                        w.Write([]byte(helpers.ErrTalkApp("Unable to register you.")))
			return
                }

                /* Register to mailing lists :) */
                /* Note: this also sends pre-saved missives for the talkapp list(s)! */
                newslist := missives.MakeApplicationSublist(conf.Tag, "talkapp", talkapp.Subscribe)
                err = missives.NewSubs(ctx, talkapp.Email, newslist)

                if err != nil {
                        ctx.Err.Printf("!!! Unable to subscribe to newsletter %s: %v", err, talkapp)
                }

                // FIXME: some kind of confirmation notice on redirect
                w.Header().Set("HX-Redirect", fmt.Sprintf("/conf/%s", conf.Tag))
        }

}

func RenderVolunteers(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
        confs := listConfs(w, ctx)
	err := ctx.TemplateCache.ExecuteTemplate(w, "embeds/volunteer_select.tmpl", &VolunteerPage{
		Confs: confs,
		Year: helpers.CurrentYear(),
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

        jobs := listJobs(w, ctx)
        confs := listConfs(w, ctx)

        switch r.Method {
        case http.MethodGet: 
                err = ctx.TemplateCache.ExecuteTemplate(w, "embeds/volunteer.tmpl", &VolunteerPage{
                        Conf: conf,
                        Confs: confs,
                        YesJobs: helpers.BuildJobs("yjob-", jobs, true),
                        NoJobs: helpers.BuildJobs("njob-", jobs, false),
                        ConfItems: helpers.GetOtherConfs(confs, *conf),
                        DaysList:  conf.DaysList("days-", true),
                        Year: helpers.CurrentYear(),
                })

                if err != nil {
                        http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
                        ctx.Err.Printf("/volunteer/%s ExecuteTemplate failed ! %s", conf.Tag, err.Error())
                        return
                }
                        return
        case http.MethodPost:
		r.ParseForm()
		dec := schema.NewDecoder()
		dec.IgnoreUnknownKeys(true)
		var vol types.Volunteer
		err = dec.Decode(&vol, r.PostForm)
		if err != nil {
			ctx.Err.Printf("/volunteer/{conf} unable to decode form %s", err)
                        w.Write([]byte(helpers.ErrVolApp("Unable to register you.")))
			return
		}

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

                err = getters.RegisterVolunteer(ctx.Notion, &vol)
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

                // FIXME: some kind of confirmation notice on redirect
                w.Header().Set("HX-Redirect", fmt.Sprintf("/conf/%s", conf.Tag))
        }

}

func RenderConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	var evSpeakers types.Speakers
	evSpeakers = filterSpeakers(talks)
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
		Conf:          conf,
		Hotels:        confHotels,
		Tix:           currTix,
		MaxTix:        maxTix,
		Sold:          soldCount,
		TixLeft:       tixLeft,
		Talks:         talks,
		EventSpeakers: evSpeakers,
		Buckets:       buckets,
		Days:          days,
		Year:          helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func RenderPage(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, page string) {

	confList := listConfs(w, ctx)
	if confList == nil {
		return
	}

	data := struct {
		Confs []*types.Conf
		Year  uint
	}{
		Confs: confList,
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

func SendCals(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {

	params := mux.Vars(r)
	confTag := params["conf"]

	var talks types.TalkTime
	talks, err := getters.GetTalksFor(ctx, confTag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	/* Send a cal invite to every speaker of a talk! */
	for _, talk := range talks {
		if talk.Sched.End == nil {
			ctx.Err.Printf("Can't send cals for %s talk: no end time??", talk.Name)
			continue
		}

		emails := make([]string, len(talk.Speakers))
		for i, speaker := range talk.Speakers {
			emails[i] = speaker.Email
		}

		ctx.Infos.Printf("Sending cal invite for %s (%d)", talk.Name, len(emails))
		/* Send cal invites!! */
		calInvite := &google.CalInvite{
			ConfTag:   confTag,
			EventName: "speak @ btc++:" + talk.Name,
			Location:  talk.VenueName(),
                        Desc:      "Your talk is happening now!",
			Invitees:  emails,
			StartTime: talk.Sched.Start,
			EndTime:   *talk.Sched.End,
		}
		ident, err := google.RunCalendarInvites(talk.CalNotif, calInvite)

		if err != nil {
			ctx.Err.Printf("Failure sending cal invite for talk %s: %s", talk.Name, err)
			continue
		}

		err = getters.TalkUpdateCalNotif(ctx.Notion, talk.ID, ident)
		if err != nil {
			ctx.Err.Printf("Failure updating calnotif data!!! %s", err)
			continue
		}

		ctx.Infos.Printf("Cal invite sent to %d people!", len(emails))
	}
}

func CheckIn(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	switch r.Method {
	case http.MethodGet:
		CheckInGet(w, r, ctx)
		return
	case http.MethodPost:
		r.ParseForm()
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
				ctx.Err.Printf("/conf/check-in ExecuteTemplate failed ! %s", err.Error())
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
			ctx.Err.Printf("/conf/check-in ExecuteTemplate failed ! %s", err.Error())
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
			ctx.Err.Printf("/conf/check-in ExecuteTemplate failed ! %s", err.Error())
		}
		return
	}

	params := mux.Vars(r)
	ticket := params["ticket"]

	tix_type, ok, err := getters.CheckIn(ctx.Notion, ticket)
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
		ctx.Err.Printf("/conf/check-in ExecuteTemplate failed ! %s", err.Error())
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
	return msgMAC == actual
}

var decoder = schema.NewDecoder()

func OpenNodeCallback(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
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

	err = getters.AddTickets(ctx.Notion, &entry, "opennode")

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

	conf, tix, tixPrice, processBTC, err := determineTixPrice(ctx, tixSlug)
	if err != nil {
		ctx.Err.Printf("/tix/%s unable to determine tix price: %s", tixSlug, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !processBTC {
		StripeInit(w, r, ctx, conf, tix, tixPrice)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/tix/%s/collect-email", tixSlug), http.StatusSeeOther)
}

func getPrice(pricestr string) (uint, error) {
	price, err := strconv.ParseUint(pricestr, 10, 32)
	return uint(price), err
}

func HandleDiscount(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	tixSlug := params["tix"]

	r.ParseForm()
	discountCode := r.Form.Get("Discount")
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

	/* Calculate the discount */
	var discountRef string
	discountPrice, discount, err := getters.CalcDiscount(ctx, conf.Ref, discountCode, tixPrice)
	if discount != nil {
		discountRef = discount.Ref
	}
	errStr := ""
	if err != nil {
		ctx.Err.Printf("/tix/%s/apply-discount discount not available: %s", tixSlug, err)
		/* We don't bail though.. just continue */
		errStr = err.Error()
	}

	w.Header().Set("Content-Type", "text/html")
	err = ctx.TemplateCache.ExecuteTemplate(w, "tix_details.tmpl", &TixFormPage{
		Conf:          conf,
		Tix:           tix,
		TixSlug:       tixSlug,
		TixPrice:      tixPrice,
		Discount:      discountCode,
		DiscountPrice: discountPrice,
		DiscountRef:   discountRef,
		Err:           errStr,
		HMAC:          calcTixHMAC(ctx, conf, tixPrice, discountPrice, discountCode),
		Count:         uint(1),
		Year:          helpers.CurrentYear(),
	})

	if err != nil {
		http.Error(w, "Unable to load template, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/tix/%s/apply-discount templ exec failed %s", tixSlug, err.Error())
		return
	}
}

func HandleEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	tixSlug := params["tix"]

	if tixSlug == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	conf, tix, tixPrice, processBTC, err := determineTixPrice(ctx, tixSlug)
	if err != nil {
		ctx.Err.Printf("/tix/%s/collect-email unable to determine tix price: %s", tixSlug, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !processBTC {
		http.Redirect(w, r, fmt.Sprintf("/tix/%s", tixSlug), http.StatusSeeOther)
		return
	}

	switch r.Method {
	case http.MethodGet:

		discountCode, _ := helpers.GetSessionKey("q", r)

		discountPrice := tixPrice
		var errStr string
		var discountRef string
		if discountCode != "" {
			var discount *types.DiscountCode
			discountPrice, discount, err = getters.CalcDiscount(ctx, conf.Ref, discountCode, tixPrice)
			if err != nil {
				ctx.Err.Printf("/tix/%s/apply-discount discount not available: %s", tixSlug, err)
				/* We don't bail though.. just continue */
				errStr = err.Error()
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
			DiscountPrice: discountPrice,
			DiscountRef:   discountRef,
			Err:           errStr,
			HMAC:          calcTixHMAC(ctx, conf, tixPrice, discountPrice, discountCode),
			Count:         uint(1),
			Year:          helpers.CurrentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/tix/{%s}/collect-email templ exec failed %s", tixSlug, err.Error())
			return
		}
		return
	case http.MethodPost:
		r.ParseForm()
		dec := schema.NewDecoder()
		dec.IgnoreUnknownKeys(true)
		var form types.TixForm
		err = dec.Decode(&form, r.PostForm)
		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/collect-email unable to decode form %s", err)
			return
		}

		if form.Email == "" || form.Count < 1 {
			http.Redirect(w, r, fmt.Sprintf("/collect-email/%s", tixSlug), http.StatusSeeOther)
                        return
		}

		/*  Validate HMAC */
		expectedHMAC := calcTixHMAC(ctx, conf, tixPrice, form.DiscountPrice, form.Discount)
		if expectedHMAC != form.HMAC {
			ctx.Err.Printf("/tix/%s/collect-email hmac mismatch. %s != %s", tixSlug, expectedHMAC, form.HMAC)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		/* The goal is that we hit opennode init, with an email! */
		isLocal := tixPrice == tix.Local
		OpenNodeInit(w, r, ctx, conf, tix, form.DiscountPrice, &form, isLocal)
		return
	default:
		http.NotFound(w, r)
		return
	}
}

func OpenNodeInit(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, tix *types.ConfTicket, tixPrice uint, tixForm *types.TixForm, isLocal bool) {
	payment, err := getters.InitOpenNodeCheckout(ctx, tixPrice, tix, conf, isLocal, tixForm.Count, tixForm.Email, tixForm.DiscountRef, tixForm.Subscribe)

	if err != nil {
		http.Error(w, "unable to init btc payment", http.StatusInternalServerError)
		ctx.Err.Printf("opennode payment init failed: %s", err.Error())
		return
	}

	/* FIXME: v2: implement on-site btc checkout */
	/* for now we go ahead and just redirect to opennode, see you latrrr */
	http.Redirect(w, r, payment.HostedCheckoutURL, http.StatusSeeOther)
}

func StripeInit(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, conf *types.Conf, tix *types.ConfTicket, tixPrice uint) {

	domain := ctx.Env.GetURI()
	priceAsCents := int64(tixPrice * 100)
	confDesc := fmt.Sprintf("1 ticket for the %s", conf.Desc)
	metadata := make(map[string]string)
	metadata["conf-tag"] = conf.Tag
	metadata["conf-ref"] = conf.Ref
	metadata["tix-id"] = tix.ID
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
		SuccessURL:          stripe.String(domain + "/conf/" + conf.Tag + "/success"),
		CancelURL:           stripe.String(domain + "/conf/" + conf.Tag),
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

		entry := types.Entry{
			ID:       checkout.ID,
			ConfRef:  conf.Ref,
			Total:    checkout.AmountTotal,
			Currency: string(checkout.Currency),
			Created:  time.Unix(checkout.Created, 0).UTC(),
			Email:    checkout.CustomerDetails.Email,
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

		err = getters.AddTickets(ctx.Notion, &entry, "stripe")

		if err != nil {
			ctx.Err.Printf("!!! Unable to add ticket: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ctx.Infos.Printf("Added %d tickets!!", len(entry.Items))

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
		r.ParseForm()
		dec := schema.NewDecoder()
		dec.IgnoreUnknownKeys(true)
		var form EmailForm
                err := dec.Decode(&form, r.PostForm)
		if err != nil {
			ctx.Err.Printf("/vols/shift unable to decode email form %s", err)
                        w.Write([]byte(helpers.ErrVolApp("Unable to send you email link.")))
			return
		}

                _, err = emails.OnlyForVolLogin(ctx, form.Email)
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
                Applied: len(apps),
                Pending: pending,
                Accepted: accepted,
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

	r.ParseForm()
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

	r.ParseForm()
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

	// Re-render the shift list
	renderShiftList(w, r, ctx, email, confTag)
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

	err = getters.AddTickets(ctx.Notion, &entry, "volreg")
	if err != nil {
		return fmt.Errorf("add ticket: %w", err)
	}

	err = missives.NewTicketSub(ctx, vol.Email, conf.Tag, tixType, true)
	if err != nil {
		ctx.Err.Printf("scheduled flow: newsletter sub failed for %s: %s", vol.Email, err)
	}

	ctx.Infos.Println("Scheduled volunteer, ticket added:", entry.ID)

	// Send calendar invites if connected
	if google.IsLoggedIn() {
		for _, shift := range vol.WorkShifts {
			if shift.ShiftTime == nil || shift.ShiftTime.End == nil {
				continue
			}
			desc := ""
			if shift.Type != nil {
				desc = shift.Type.LongDesc
			}
			calInvite := &google.CalInvite{
				ConfTag:   conf.Tag,
				EventName: fmt.Sprintf("volunteer @ btc++: %s", shift.Name),
				Location:  conf.Location,
				Desc:      desc,
				Invitees:  []string{vol.Email},
				StartTime: shift.ShiftTime.Start,
				EndTime:   *shift.ShiftTime.End,
			}
			_, calErr := google.RunCalendarInvites("", calInvite)
			if calErr != nil {
				ctx.Err.Printf("scheduled flow: cal invite failed for shift %s: %s", shift.Name, calErr)
			}
		}

		if volinfo != nil && volinfo.OrientTimes != nil && volinfo.OrientTimes.End != nil {
			orientInvite := &google.CalInvite{
				ConfTag:   conf.Tag,
				EventName: fmt.Sprintf("volunteer @ bitcoin++: %s Volunteer Orientation", conf.Tag),
				Invitees:  []string{vol.Email},
				StartTime: volinfo.OrientTimes.Start,
				EndTime:   *volinfo.OrientTimes.End,
			}
			_, calErr := google.RunCalendarInvites("", orientInvite)
			if calErr != nil {
				ctx.Err.Printf("scheduled flow: orientation cal invite failed: %s", calErr)
			}
		}
	}

	return nil
}

func VolAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
        /* Check for verified */
        if ok := helpers.CheckPin(w, r, ctx); !ok {
                helpers.Render401(w, r, ctx)
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
		ctx.Err.Printf("/vols/admin/%s failed to get volunteers: %s", conf.Tag, err.Error())
		return
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		ctx.Err.Printf("/vols/admin/%s failed to get shifts: %s", conf.Tag, err.Error())
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

	missiveList, err := getters.ListOnlyForLetters(ctx.Notion)
	if err != nil {
		ctx.Err.Printf("/vols/admin/%s failed to load missives: %s", conf.Tag, err.Error())
		// continue without missives
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "volunteers/admin.tmpl", &VolAdminPage{
		Conf:         conf,
		Volunteers:   vols,
		Shifts:       shifts,
		StatusFilter: statusFilter,
		Missives:     missiveList,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/vols/admin/%s template failed: %s", conf.Tag, err.Error())
	}
}

func VolAdminPromote(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
        /* Check for verified */
        if ok := helpers.CheckPin(w, r, ctx); !ok {
                helpers.Render401(w, r, ctx)
                return
        }

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	r.ParseForm()
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
			ctx.Err.Printf("/vols/admin/%s/promote failed to update %s: %s", conf.Tag, vol.Name, err.Error())
			continue
		}

		// Send shift signup email when promoting to PendingShifts
		if targetStatus == "PendingShifts" {
			_, emailErr := emails.OnlyForVolSignup(ctx, vol, conf)
			if emailErr != nil {
				ctx.Err.Printf("/vols/admin/%s/promote email failed for %s: %s", conf.Tag, vol.Email, emailErr)
			}
		}

		promoted++
	}

	// Redirect back to admin page
	http.Redirect(w, r, fmt.Sprintf("/vols/admin/%s", conf.Tag), http.StatusSeeOther)
}

func VolAdminAutoAssign(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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
		ctx.Err.Printf("/vols/admin/%s/auto-assign failed to get volunteers: %s", conf.Tag, err.Error())
		return
	}

	shifts, err := getters.GetShiftsForConf(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load shifts", http.StatusInternalServerError)
		ctx.Err.Printf("/vols/admin/%s/auto-assign failed to get shifts: %s", conf.Tag, err.Error())
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
		ctx.Err.Printf("/vols/admin/%s/auto-assign failed: %s", conf.Tag, err.Error())
	}

	http.Redirect(w, r, fmt.Sprintf("/vols/admin/%s", conf.Tag), http.StatusSeeOther)
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
		selectedShifts := getSelectedShifts(vol, confShifts)
		for _, shift := range selectedShifts {
			err = getters.RemoveVolunteerFromShift(ctx, vol.Ref, shift.Ref)
			if err != nil {
				ctx.Err.Printf("/vols/shift/%s/decline failed to remove shift %s: %s", confTag, shift.Name, err.Error())
			}
		}
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
	err = getters.RevokeTicket(ctx.Notion, vol.RegisID())
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

	r.ParseForm()
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
	r.ParseForm()
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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

func volAdminRedirect(w http.ResponseWriter, r *http.Request, conf *types.Conf, volRef string) {
	http.Redirect(w, r, fmt.Sprintf("/vols/admin/%s/vol/%s", conf.Tag, volRef), http.StatusSeeOther)
}

func VolAdminUpdateStatus(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	r.ParseForm()
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	r.ParseForm()
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	jobs := listJobs(w, ctx)
	r.ParseForm()
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	r.ParseForm()
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, vol, _ := volAdminLoadVol(w, r, ctx)
	if vol == nil {
		return
	}

	r.ParseForm()
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

	volAdminRedirect(w, r, conf, vol.Ref)
}

func VolAdminMarkScheduled(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	r.ParseForm()
	volRefs := r.Form["vol_refs"]
	if len(volRefs) == 0 {
		http.Redirect(w, r, fmt.Sprintf("/vols/admin/%s?flash=No+volunteers+selected", conf.Tag), http.StatusSeeOther)
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
		ctx.Err.Printf("/vols/admin/%s/email failed to load shifts: %s", conf.Tag, err.Error())
	}
	for _, v := range targets {
		v.WorkShifts = getSelectedShifts(v, shifts)
	}

	volinfo, err := getters.GetVolInfo(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/vols/admin/%s/email failed to load volinfo: %s", conf.Tag, err.Error())
	}

	sent := 0
        title := r.FormValue("title")
        body := r.FormValue("body")
        if title == "" || body == "" {
                http.Redirect(w, r, fmt.Sprintf("/vols/admin/%s?flash=Title+and+body+required", conf.Tag), http.StatusSeeOther)
                return
        }
        for _, v := range targets {
                _, err := emails.SendCustomToVol(ctx, v, conf, volinfo, title, body)
                if err != nil {
                        ctx.Err.Printf("/vols/admin/%s/email custom -> %s failed: %s", conf.Tag, v.Email, err)
                        continue
                }
                sent++
        }

	flash := fmt.Sprintf("Sent+to+%d+of+%d+volunteers", sent, len(targets))
	http.Redirect(w, r, fmt.Sprintf("/vols/admin/%s?flash=%s", conf.Tag, flash), http.StatusSeeOther)
}

// parseShiftFormTimes turns a date (YYYY-MM-DD or 01/02/2006) plus two HH:MM
// time strings into start/end time.Time values in the conference's timezone.
// End is rolled over to the next day if it's earlier than start (e.g. an
// overnight shift).
func parseShiftFormTimes(conf *types.Conf, dayStr, startStr, endStr string) (time.Time, time.Time, error) {
	// Accept either Notion's "01/02/2006" or HTML date input "2006-01-02"
	loc := conf.StartDate.Location()
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
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
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
		ctx.Err.Printf("/vols/admin/%s/shifts failed to get shifts: %s", conf.Tag, err.Error())
		return
	}

	jobs, err := getters.FetchJobsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/vols/admin/%s/shifts failed to fetch jobs: %s", conf.Tag, err.Error())
	}

	// Resolve all unique assignees → Volunteer for name display
	volMap := make(map[string]*types.Volunteer)
	allVols, err := getters.ListVolunteersForConf(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/vols/admin/%s/shifts failed to load vols: %s", conf.Tag, err.Error())
	}
	for _, v := range allVols {
		volMap[v.Ref] = v
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
		// Pad ranges so the gantt has a little headroom
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
		ctx.Err.Printf("/vols/admin/%s/shifts template failed: %s", conf.Tag, err.Error())
	}
}

func volAdminShiftsRedirect(w http.ResponseWriter, r *http.Request, conf *types.Conf) {
	http.Redirect(w, r, fmt.Sprintf("/vols/admin/%s/shifts", conf.Tag), http.StatusSeeOther)
}

func VolAdminCreateShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	r.ParseForm()
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
		ctx.Err.Printf("/vols/admin/%s/shifts/new failed: %s", conf.Tag, err.Error())
		http.Error(w, "Failed to create shift: "+err.Error(), http.StatusInternalServerError)
		return
	}

	volAdminShiftsRedirect(w, r, conf)
}

func VolAdminUpdateShift(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	shiftRef := mux.Vars(r)["shiftRef"]

	r.ParseForm()
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
		ctx.Err.Printf("/vols/admin/%s/shifts/%s/update failed: %s", conf.Tag, shiftRef, err.Error())
		http.Error(w, "Failed to update shift: "+err.Error(), http.StatusInternalServerError)
		return
	}

	volAdminShiftsRedirect(w, r, conf)
}

func TalksGifts(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		http.Error(w, "Unable to load conferences", http.StatusInternalServerError)
		ctx.Err.Printf("/talks/gifts failed to get confs: %s", err.Error())
		return
	}

	confTag := r.URL.Query().Get("conf")
	filePath := r.URL.Query().Get("filepath")

	var selectedConf *types.Conf
	var talks []*types.Talk
	var rows []*GiftRow

	if confTag != "" {
		for _, conf := range confs {
			if conf.Tag == confTag {
				selectedConf = conf
				break
			}
		}
		if selectedConf != nil {
			talks, err = getters.GetTalksFor(ctx, selectedConf.Tag)
			if err != nil {
				http.Error(w, "Unable to load talks", http.StatusInternalServerError)
				ctx.Err.Printf("/talks/gifts failed to get talks for %s: %s", confTag, err.Error())
				return
			}

			for _, talk := range talks {
				for _, speaker := range talk.Speakers {
					rows = append(rows, &GiftRow{
						Clipart:     talk.Clipart,
						SpeakerName: speaker.Name,
					})
				}
			}

			sort.SliceStable(rows, func(i, j int) bool {
				return rows[i].SpeakerName < rows[j].SpeakerName
			})
		}
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "talks/gifts.tmpl", &TalksGiftsPage{
		Confs:    confs,
		Conf:     selectedConf,
		Rows:     rows,
		FilePath: filePath,
		Year:     helpers.CurrentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/talks/gifts template failed: %s", err.Error())
	}
}

