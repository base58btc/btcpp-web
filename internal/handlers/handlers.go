package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/base58btc/btcpp-web/external/getters"
	"github.com/base58btc/btcpp-web/internal/config"
	"github.com/base58btc/btcpp-web/internal/types"
	"github.com/gorilla/mux"
	"github.com/gorilla/schema"

	"encoding/base64"
	qrcode "github.com/skip2/go-qrcode"

	stripe "github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/webhook"
)

func MiniCss() string {
	css, err := ioutil.ReadFile("static/css/mini.css")
	if err != nil {
		panic(err)
	}
	return string(css)
}

var pages []string = []string{"index", "about", "sponsor", "contact", "talk", "press", "volunteer", "vegas25"}

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

func findConf(r *http.Request, app *config.AppContext) (*types.Conf, error) {
	params := mux.Vars(r)
	confTag := params["conf"]

	for _, conf := range app.Confs {
		if conf.Tag == confTag {
			return conf, nil
		}
	}

	return nil, fmt.Errorf("'%s' not found (url: %s)", confTag, r.URL.String())
}

func findConfByRef(app *config.AppContext, confRef string) *types.Conf {
	for _, conf := range app.Confs {
		if conf.Ref == confRef {
			return conf
		}
	}
	return nil
}

func findTicket(app *config.AppContext, tixID string) (*types.ConfTicket, *types.Conf) {
	for _, conf := range app.Confs {
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
	r.HandleFunc("/privacy", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/riga", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/istanbul", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/istanbul", http.StatusSeeOther)
	}).Methods("GET")
	r.HandleFunc("/taipei", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/conf/taipei", http.StatusSeeOther)
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

	r.HandleFunc("/conf/{conf}/success", func(w http.ResponseWriter, r *http.Request) {
		RenderConfSuccess(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/conf/{conf}/talks", func(w http.ResponseWriter, r *http.Request) {
		RenderTalks(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/conf/{conf}", func(w http.ResponseWriter, r *http.Request) {
		RenderConf(w, r, app)
	}).Methods("GET")
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
	}).Methods("GET", "POST")
	r.HandleFunc("/check-in/{ticket}", func(w http.ResponseWriter, r *http.Request) {
		CheckIn(w, r, app)
	}).Methods("GET", "POST")
	r.HandleFunc("/welcome-email", func(w http.ResponseWriter, r *http.Request) {
		TicketCheck(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/ticket/{ticket}", func(w http.ResponseWriter, r *http.Request) {
		Ticket(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/trial-email", func(w http.ResponseWriter, r *http.Request) {
		SendMailTest(w, r, app)
	}).Methods("GET")

	/* Setup stripe! */
	stripe.Key = app.Env.StripeKey
	r.HandleFunc("/callback/stripe", func(w http.ResponseWriter, r *http.Request) {
		StripeCallback(w, r, app)
	}).Methods("POST")
	r.HandleFunc("/callback/opennode", func(w http.ResponseWriter, r *http.Request) {
		OpenNodeCallback(w, r, app)
	}).Methods("GET", "POST")

	// Create a file server to serve static files from the "static" directory
	fs := http.FileServer(http.Dir("static"))
	r.PathPrefix("/static/").Handler(http.StripPrefix("/static/", fs))
	err := addFaviconRoutes(r)

	if err != nil {
		return r, err
	}

	err = loadTemplates(app)
	if err != nil {
		return r, err
	}

	getters.GetSpeakers(app)
	getters.GetTalks(app)
	getters.GetDiscounts(app)

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

func getSessionKey(p string, r *http.Request) (string, bool) {
	ok := r.URL.Query().Has(p)
	key := r.URL.Query().Get(p)
	return key, ok
}

type HomePage struct {
	Year uint
}

type ConfPage struct {
	Conf          *types.Conf
	Tix           *types.ConfTicket
	MaxTix        *types.ConfTicket
	Sold          uint
	TixLeft       uint
	Talks         []*types.Talk
	EventSpeakers []*types.Speaker
	Buckets       map[string]sessionTime
	Days          []*Day
	Year          uint
}

type SuccessPage struct {
	Conf *types.Conf
	Year uint
}

type TixFormPage struct {
	Conf          *types.Conf
	Tix           *types.ConfTicket
	TixSlug       string
	Count         uint
	TixPrice      uint
	Discount      string
	DiscountPrice uint
	DiscountRef   string
	HMAC          string
	Err           string
	Year          uint
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

func GetReloadConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* Check for logged in */
	pin := ctx.Session.GetString(r.Context(), "pin")

	if pin == "" {
		w.Header().Set("x-missing-field", "pin")
		w.WriteHeader(http.StatusBadRequest)
		err := ctx.TemplateCache.ExecuteTemplate(w, "checkin.tmpl", &CheckInPage{
			NeedsPin: true,
			Year:     currentYear(),
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
			Year: currentYear(),
		})
		if err != nil {
			http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
			ctx.Err.Printf("/conf-reload ExecuteTemplate failed ! %s", err.Error())
		}
		return
	}

	confs, err := getters.ListConferences(ctx.Notion)
	if err != nil {
		http.Error(w, "Unable to load confereneces, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/conf-reload conf load failed ! %s", err.Error())
		return
	}

	ctx.Confs = confs

	/* Also reload cached data */
	getters.GetSpeakers(ctx)
	getters.GetTalks(ctx)
	getters.GetDiscounts(ctx)
	for _, conf := range confs {
		getters.UpdateSoldTix(ctx, conf)
	}

	/* We redirect to home on success */
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func ReloadConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	switch r.Method {
	case http.MethodGet:
		GetReloadConf(w, r, ctx)
		return
	case http.MethodPost:
		r.ParseForm()
		pin := r.Form.Get("pin")
		if pin != ctx.Env.RegistryPin {
			w.WriteHeader(http.StatusBadRequest)
			err := ctx.TemplateCache.ExecuteTemplate(w, "checkin.tmpl", &CheckInPage{
				NeedsPin: true,
				Msg:      "Wrong pin",
				Year:     currentYear(),
			})
			if err != nil {
				http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
				ctx.Err.Printf("/conf-reload ExecuteTemplate failed ! %s", err.Error())
				return
			}
			ctx.Err.Printf("/conf-reload wrong pin submitted! %s", pin)
			return
		}

		/* Set pin?? */
		ctx.Session.Put(r.Context(), "pin", pin)
		GetReloadConf(w, r, ctx)
	}
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
	conf, err := findConf(r, ctx)
	if err != nil {
		http.Error(w, "Unable to find page", 404)
		ctx.Err.Printf("Unable to find conf %s: %s", err.Error())
		return
	}

	var talks talkTime
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
		Year:          currentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s/talks ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func RenderConfSuccess(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := findConf(r, ctx)
	if err != nil {
		http.Error(w, "Unable to find page", 404)
		ctx.Err.Printf("Unable to find conf %s: %s", err.Error())
		return
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "success.tmpl", &SuccessPage{
		Conf: conf,
		Year: currentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/conf/%s/success ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func RenderConf(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	conf, err := findConf(r, ctx)
	if err != nil {
		http.Error(w, "Unable to find page", 404)
		ctx.Err.Printf("Unable to find conf %s: %s", err.Error())
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
		Tix:           currTix,
		MaxTix:        maxTix,
		Sold:          soldCount,
		TixLeft:       tixLeft,
		Talks:         talks,
		EventSpeakers: evSpeakers,
		Buckets:       buckets,
		Days:          days,
		Year:          currentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s ExecuteTemplate failed ! %s", conf.Tag, err.Error())
		return
	}
}

func currentYear() uint {
	year, _, _ := time.Now().Date()
	return uint(year)
}

func RenderPage(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, page string) {

	template := fmt.Sprintf("embeds/%s.tmpl", page)
	err := ctx.TemplateCache.ExecuteTemplate(w, template, &HomePage{
		Year: currentYear(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/%s ExecuteTemplate failed ! %s", page, err.Error())
		return
	}
}

type Session struct {
	Name      string
	Speakers  []*types.Speaker
	TalkPhoto string
	Sched     *types.Times
	StartTime string
	Len       string
	DayTag    string
	Type      string
	Venue     string
	AnchorTag string
	ConfTag   string
}

func TalkToSession(talk *types.Talk, conf *types.Conf) *Session {
	sesh := &Session{
		Name:      talk.Name,
		Speakers:  talk.Speakers,
		TalkPhoto: talk.Clipart,
		Sched:     talk.Sched,
		Type:      talk.Type,
		Venue:     talk.Venue,
		AnchorTag: talk.AnchorTag,
		ConfTag:   conf.Tag,
	}

	if talk.Sched != nil {
		sesh.Len = talk.Sched.LenStr()
		sesh.StartTime = talk.Sched.StartTime()
		sesh.DayTag = talk.Sched.Day()
	}

	return sesh
}

type SchedulePage struct {
	Talks    []*types.Talk
	Sessions []talkTime
}

type talkTime []*types.Talk
type sessionTime []*Session

func (p talkTime) Len() int {
	return len(p)
}

func (p talkTime) Less(i, j int) bool {
	if p[i].Sched == nil {
		return true
	}
	if p[j].Sched == nil {
		return false
	}

	/* Sort by time first */
	if p[i].Sched.Start != p[j].Sched.Start {
		return p[i].Sched.Start.Before(p[j].Sched.Start)
	}

	/* Then we sort by room */
	return p[i].VenueValue() < p[j].VenueValue()
}

func (p talkTime) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

type Day struct {
	Morning   []sessionTime
	Afternoon []sessionTime
	Evening   []sessionTime
}

func talkDays(ctx *config.AppContext, conf *types.Conf, talks talkTime) ([]*Day, error) {
	buckets, err := bucketTalks(conf, talks)
	if err != nil {
		return nil, err
	}
	/* Sort keys alphabetically */
	days := make([]*Day, 0)

	keys := make([]string, len(buckets))
	i := 0
	for k, _ := range buckets {
		keys[i] = k
		i++
	}
	sort.Strings(keys)
	for _, k := range keys {
		v, _ := buckets[k]
		i, err := strconv.Atoi(string(k[0]))
		if err != nil {
			return nil, err
		}
		/* This could go horribly wrong */
		if i > 21 {
			return nil, fmt.Errorf("too many days %d", i)
		}
		for i > len(days) {
			days = append(days, &Day{
				Morning:   make([]sessionTime, 0),
				Afternoon: make([]sessionTime, 0),
				Evening:   make([]sessionTime, 0),
			})
		}

		day := days[i-1]
		switch string(k[len(k)-1]) {
		case "+":
			day.Morning = append(day.Morning, v)
		case "=":
			day.Afternoon = append(day.Afternoon, v)
		case "-":
			day.Evening = append(day.Evening, v)
		}
	}

	return days, nil
}

func bucketTalks(conf *types.Conf, talks talkTime) (map[string]sessionTime, error) {
	sort.Sort(talks)

	sessions := make(map[string]sessionTime)
	for _, talk := range talks {
		session := TalkToSession(talk, conf)
		section, ok := sessions[talk.Section]
		if !ok {
			section = make(sessionTime, 0)
		}
		section = append(section, session)
		sessions[talk.Section] = section
	}
	return sessions, nil
}

type EmailTmpl struct {
	URI     string
	CSS     string
	ConfTag string
}

type TicketTmpl struct {
	QRCodeURI string
	Domain    string
	CSS       string
	Type      string
	Conf      *types.Conf
}

func SendMailTest(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	reg := &types.Registration{
		RefID:      "testticket",
		Type:       "volunteer",
		Email:      "niftynei@gmail.com",
		ItemBought: "bitcoin++",
	}

	sendMail(w, r, ctx, reg)
}

func sendMail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, reg *types.Registration) {
	pdf, err := MakeTicketPDF(ctx, reg)

	if err != nil {
		http.Error(w, "Unable to make ticket, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/send test mail failed ! %s", err.Error())
		return
	}

	tickets := make([]*types.Ticket, 1)
	tickets[0] = &types.Ticket{
		Pdf: pdf,
		ID:  reg.RefID,
	}

	err = SendTickets(ctx, tickets, reg.ConfRef, reg.Email, time.Now())

	/* Return the error */
	if err != nil {
		http.Error(w, "Unable to send ticket, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("/send test mail failed to send! %s", err.Error())
		return
	}
}

func Ticket(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	ticket := params["ticket"]

	tixType, _ := getSessionKey("type", r)
	confRef, _ := getSessionKey("conf", r)

	/* make it pretty */
	if tixType == "genpop" {
		tixType = "general"
	}

	conf := findConfByRef(ctx, confRef)
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
		CSS:       MiniCss(),
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

func TicketCheck(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	confTag, _ := getSessionKey("tag", r)

	tmplTag := fmt.Sprintf("emails/%s.tmpl", confTag)
	err := ctx.TemplateCache.ExecuteTemplate(w, tmplTag, &EmailTmpl{
		URI: ctx.Env.GetURI(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Infos.Printf("/welcome-email ExecuteTemplate failed ! %s", err.Error())
	}
}

type CheckInPage struct {
	NeedsPin   bool
	TicketType string
	Msg        string
	Year       uint
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
				Year:     currentYear(),
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
			Year:     currentYear(),
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
			Year: currentYear(),
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
		ctx.Err.Printf("Unable to check-in %s:", ticket, err.Error())
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
		Year:       currentYear(),
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
		ctx.Err.Printf("Unable to fetch charge", err)
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
		Year:          currentYear(),
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

		discountCode, _ := getSessionKey("q", r)

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
			Year:          currentYear(),
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
	payment, err := getters.InitOpenNodeCheckout(ctx, tixPrice, tix, conf, isLocal, tixForm.Count, tixForm.Email, tixForm.DiscountRef)

	if err != nil {
		http.Error(w, "unable to init btc payment", http.StatusInternalServerError)
		ctx.Err.Printf("opennode payment init failed", err.Error())
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
			w.WriteHeader(http.StatusOK)
			return
		}
		conf := findConfByRef(ctx, confRef)
		if conf == nil {
			ctx.Err.Println("Couldn't find conf %s", confRef)
			w.WriteHeader(http.StatusOK)
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
			ctx.Err.Printf("!!! Unable to add ticket %s: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		ctx.Infos.Printf("Added %d tickets!!", len(entry.Items))
	default:
		ctx.Infos.Printf("Unhandled event type: %s", event.Type)
	}

	w.WriteHeader(http.StatusOK)
}
