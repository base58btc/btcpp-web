package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"btcpp-web/external/buffer"
	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/handlers"
	"btcpp-web/internal/types"
	"github.com/BurntSushi/toml"
	"github.com/alexedwards/scs/boltstore"
	"github.com/alexedwards/scs/v2"
	bolt "go.etcd.io/bbolt"
)

const configFile = "config.toml"

var app config.AppContext

func loadConfig() *types.EnvConfig {
	var config types.EnvConfig

	if _, err := os.Stat("config.toml"); err == nil {
		_, err = toml.DecodeFile(configFile, &config)
		if err != nil {
			log.Fatal(err)
		}
		config.Prod = false

		config.HMACKey, err = types.DeriveHMACKey(config.HMACSecret)
		if err != nil {
			log.Fatal(err)
		}
		config.HMACSecret = ""
	} else {
		config.Port = os.Getenv("PORT")
		config.Prod = true

		config.Host = os.Getenv("HOST")
		config.MailerSecret = os.Getenv("MAILER_SECRET")
		config.MailEndpoint = os.Getenv("MAILER_ENDPOINT")
		config.MailOff = false

		mailSec, err := strconv.ParseInt(os.Getenv("MAILER_JOB_SEC"), 10, 32)
		if err != nil {
			log.Fatal(err)
			return nil
		}
		config.MailerJob = int(mailSec)

		config.OpenNode.Key = os.Getenv("OPENNODE_KEY")
		config.OpenNode.Endpoint = os.Getenv("OPENNODE_ENDPOINT")

		config.StripeKey = os.Getenv("STRIPE_KEY")
		config.StripeEndpointSec = os.Getenv("STRIPE_END_SECRET")
		config.RegistryPin = os.Getenv("REGISTRY_PIN")
		config.Notion = types.NotionConfig{
			Token:            os.Getenv("NOTION_TOKEN"),
			PurchasesDb:      os.Getenv("NOTION_PURCHASES_DB"),
			SpeakersDb:       os.Getenv("NOTION_SPEAKERS_DB"),
			ConfsDb:          os.Getenv("NOTION_CONFS_DB"),
			ConfsTixDb:       os.Getenv("NOTION_CONFSTIX_DB"),
			DiscountsDb:      os.Getenv("NOTION_DISCOUNT_DB"),
			NewsletterDb:     os.Getenv("NOTION_NEWSLETTER_DB"),
			MissivesDb:       os.Getenv("NOTION_MISSIVES_DB"),
			HotelsDb:         os.Getenv("NOTION_HOTEL_DB"),
			VolunteerDb:      os.Getenv("NOTION_VOLUNTEER_DB"),
			JobTypeDb:        os.Getenv("NOTION_JOBTYPE_DB"),
			ProposalDb:       os.Getenv("NOTION_PROPOSAL_DB"),
			SpeakerConfDb:    os.Getenv("NOTION_SPEAKER_CONF_DB"),
			ConfTalkDb:       os.Getenv("NOTION_CONFTALK_DB"),
			RecordingsDb:     os.Getenv("NOTION_RECORDINGS_DB"),
			ConfInfoDb:       os.Getenv("NOTION_CONFINFO_DB"),
			ShiftDb:          os.Getenv("NOTION_SHIFTS_DB"),
			VolInfoDb:        os.Getenv("NOTION_VOLINFO_DB"),
			OrgDb:            os.Getenv("NOTION_ORG_DB"),
			SponsorshipsDb:   os.Getenv("NOTION_SPONSORSHIPS_DB"),
			SocialPostsDb:    os.Getenv("NOTION_SOCIAL_POSTS_DB"),
			AffiliateUsageDb: os.Getenv("NOTION_AFFILIATE_USE_DB"),
		}
		config.BufferAPI = os.Getenv("BUFFER_KEY")

		config.Spaces = types.SpacesConfig{
			Endpoint: os.Getenv("SPACES_ENDPOINT"),
			Region:   os.Getenv("SPACES_REGION"),
			Bucket:   os.Getenv("SPACES_BUCKET"),
			Key:      os.Getenv("SPACES_KEY"),
			Secret:   os.Getenv("SPACES_SECRET"),
		}

		if ttl := os.Getenv("CACHE_TTL_SEC"); ttl != "" {
			if v, err := strconv.Atoi(ttl); err == nil {
				config.CacheTTLSec = v
			}
		}

		config.HMACKey, err = types.DeriveHMACKey(os.Getenv("HMAC_SECRET"))
		if err != nil {
			log.Fatal(err)
		}
	}

	// Default cache TTL to 300s (5 min) if not set
	if config.CacheTTLSec == 0 {
		config.CacheTTLSec = 300
	}

	if err := config.Validate(); err != nil {
		log.Fatal(err)
	}

	return &config
}

/* Every XX seconds, try to send new ticket emails. */
func RunNewMails(ctx *config.AppContext) {
	/* Wait a bit, so server can start up */
	time.Sleep(4 * time.Second)
	ctx.Infos.Println("Starting up mailer job...")
	for true {
		emails.CheckForNewMails(ctx)
		time.Sleep(time.Duration(ctx.Env.MailerJob) * time.Second)
	}
}

func main() {
	/* Load configs from config.toml */
	app.Env = loadConfig()
	err := run(app.Env)
	if err != nil {
		log.Fatal(err)
	}

	/* Start up Spaces (S3-compatible storage) */
	spaces.Init(app.Env.Spaces)

	/* Load cached data */
	getters.WaitFetch(&app)
	getters.StartWorkPool(&app)

	/* Start up Buffer */
	buffer.Init(app.Env.BufferAPI)

	/* Set up Routes + Templates */
	routes, err := handlers.Routes(&app)
	if err != nil {
		app.Err.Fatal(err)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%s", app.Env.Port),
		Handler:           app.Session.LoadAndSave(routes),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	/* Kick off job to start sending mails */
	if !app.Env.MailOff {
		go RunNewMails(&app)
	}

	/* Start media card refresh after server is listening */
	if spaces.IsConfigured() {
		go func() {
			time.Sleep(3 * time.Second)
			handlers.InitMediaRefresh(&app)
			app.Infos.Printf("media refresh done")
		}()
		app.Infos.Printf("scheduling media refresh")
	}

	/* Start the server */
	app.Infos.Printf("Starting application on port %s\n", app.Env.Port)
	app.Infos.Printf("... Current domain is %s\n", app.Env.GetDomain())
	err = srv.ListenAndServe()
	if err != nil {
		app.Err.Fatal(err)
	}
}

func run(env *types.EnvConfig) error {
	/* Load up the logfile */
	var logfile *os.File
	var err error
	if env.LogFile != "" {
		fmt.Println("Using logfile:", env.LogFile)
		logfile, err = os.OpenFile(env.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		if err != nil {
			return err
		}
	} else {
		fmt.Println("Using logfile: stdout")
		logfile = os.Stdout
	}

	app.Infos = log.New(logfile, "INFO\t", log.Ldate|log.Ltime)
	app.Err = log.New(logfile, "ERROR\t", log.Ldate|log.Ltime|log.Lshortfile)

	// Initialize the application configuration
	app.InProduction = env.Prod
	app.EmailCache = make(map[string]*template.Template)

	app.Infos.Println("")
	app.Infos.Println("~~~~app restarted, here we go~~~~~")
	app.Infos.Println("Running in prod?", env.Prod)

	// Initialize the session manager backed by a BoltDB file so
	// admin/dashboard logins survive app restarts. Default
	// memstore wipes every session when the process exits — fine
	// for the legacy CheckPin model (one shared PIN, re-typed
	// after every restart) but a regression for the magic-link
	// auth flow where a restart silently logs everyone out.
	// No defer Close — the DB lives for the lifetime of the
	// process. A defer here would fire when run() returns (i.e.
	// before the HTTP server even starts handling requests),
	// shuttering the store and breaking every session-touching
	// route with "database not open." On process exit the OS
	// reclaims the file handle; bbolt's mmap+commit format is
	// crash-consistent, so an unclean shutdown is recoverable on
	// the next open.
	sessDB, err := bolt.Open("sessions.bolt", 0600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		app.Err.Fatalf("open sessions.bolt: %s", err)
	}
	app.Session = scs.New()
	app.Session.Lifetime = 4 * 24 * time.Hour
	app.Session.Cookie.Persist = true
	app.Session.Cookie.SameSite = http.SameSiteLaxMode
	app.Session.Cookie.Secure = app.InProduction
	app.Session.Store = boltstore.New(sessDB)

	app.Notion = &types.Notion{Config: &env.Notion}
	app.Notion.Setup(env.Notion.Token)

	// Per-request Notion timing → app log. Stays off in CLIs that build
	// their own ad-hoc Notion clients without calling SetNotionRequestLogger.
	types.SetNotionRequestLogger(app.Infos.Printf)

	return nil
}
