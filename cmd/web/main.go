package main

import (
	"crypto/sha256"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/google"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/handlers"
	"btcpp-web/internal/types"
	"github.com/BurntSushi/toml"
	"github.com/alexedwards/scs/v2"
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

		config.HMACKey = sha256.Sum256([]byte(config.HMACSecret))
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
			Token:        os.Getenv("NOTION_TOKEN"),
			PurchasesDb:  os.Getenv("NOTION_PURCHASES_DB"),
			TalksDb:      os.Getenv("NOTION_TALKS_DB"),
			SpeakersDb:   os.Getenv("NOTION_SPEAKERS_DB"),
			ConfsDb:      os.Getenv("NOTION_CONFS_DB"),
			ConfsTixDb:   os.Getenv("NOTION_CONFSTIX_DB"),
			DiscountsDb:  os.Getenv("NOTION_DISCOUNT_DB"),
			NewsletterDb: os.Getenv("NOTION_NEWSLETTER_DB"),
			MissivesDb:   os.Getenv("NOTION_MISSIVES_DB"),
			TokenDb:      os.Getenv("NOTION_TOKEN_DB"),
		}
		config.Google = types.GoogleConfig{
			Key:    os.Getenv("GOOGLE_KEY"),
			Config: os.Getenv("GOOGLE_CONFIG"),
		}

		secretHex := os.Getenv("HMAC_SECRET")
		config.HMACKey = sha256.Sum256([]byte(secretHex))
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

	/* Load cached data */
	getters.WaitFetch(&app)
	getters.StartWorkPool(&app)

	/* Start up Google stuffs */
	google.InitOauth(&app)

	/* Set up Routes + Templates */
	routes, err := handlers.Routes(&app)
	if err != nil {
		app.Err.Fatal(err)
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%s", app.Env.Port),
		Handler: app.Session.LoadAndSave(routes),
	}

	/* Kick off job to start sending mails */
	if !app.Env.MailOff {
		go RunNewMails(&app)
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

	app.Infos.Println("\n\n\n")
	app.Infos.Println("~~~~app restarted, here we go~~~~~")
	app.Infos.Println("Running in prod?", env.Prod)

	// Initialize the session manager
	app.Session = scs.New()
	app.Session.Lifetime = 4 * 24 * time.Hour
	app.Session.Cookie.Persist = true
	app.Session.Cookie.SameSite = http.SameSiteLaxMode
	app.Session.Cookie.Secure = app.InProduction

	app.Notion = &types.Notion{Config: &env.Notion}
	app.Notion.Setup(env.Notion.Token)

	return nil
}
