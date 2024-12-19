package main

import (
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/alexedwards/scs/v2"
	"github.com/base58btc/btcpp-web/external/getters"
	"github.com/base58btc/btcpp-web/internal/config"
	"github.com/base58btc/btcpp-web/internal/handlers"
	"github.com/base58btc/btcpp-web/internal/types"
	"github.com/niftynei/cln-checkout/checkout"
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
		config.MailOff = false

		mailSec, err := strconv.ParseInt(os.Getenv("MAILER_JOB_SEC"), 10, 32)
		if err != nil {
			log.Fatal(err)
			return nil
		}
		config.MailerJob = int(mailSec)

		config.OpenNode.Key = os.Getenv("OPENNODE_KEY")
		config.OpenNode.Endpoint = os.Getenv("OPENNODE_ENDPOINT")

		config.UseCLN = os.Getenv("CLN_ON") == "1"

		config.StripeKey = os.Getenv("STRIPE_KEY")
		config.StripeEndpointSec = os.Getenv("STRIPE_END_SECRET")
		config.RegistryPin = os.Getenv("REGISTRY_PIN")
		config.Notion = types.NotionConfig{
			Token:       os.Getenv("NOTION_TOKEN"),
			PurchasesDb: os.Getenv("NOTION_PURCHASES_DB"),
			TalksDb:     os.Getenv("NOTION_TALKS_DB"),
			SpeakersDb:  os.Getenv("NOTION_SPEAKERS_DB"),
			ConfsDb:     os.Getenv("NOTION_CONFS_DB"),
			ConfsTixDb:  os.Getenv("NOTION_CONFSTIX_DB"),
			DiscountsDb:  os.Getenv("NOTION_DISCOUNT_DB"),
		}
		config.Google = types.GoogleConfig{Key: os.Getenv("GOOGLE_KEY")}

		secretHex := os.Getenv("HMAC_SECRET")
		config.HMACKey = sha256.Sum256([]byte(secretHex))

		expirySec, err := strconv.ParseUint(os.Getenv("CLN_INVOICE_EXPIRY"), 10, 64)
		if err != nil {
			log.Fatal(err)
			return nil
		}
		config.CLN = types.CLNConfig{
			Expiry:   expirySec,
			Hostname: os.Getenv("CLN_HOSTNAME"),
			Pubkey:   os.Getenv("CLN_NODE_PUBKEY"),
			Rune:     os.Getenv("CLN_RUNE"),
		}
	}

	return &config
}

/* Every XX seconds, try to send new ticket emails. */
func RunNewMails(ctx *config.AppContext) {
	/* Wait a bit, so server can start up */
	time.Sleep(4 * time.Second)
	ctx.Infos.Println("Starting up mailer job...")
	for true {
		handlers.CheckForNewMails(ctx)
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

	/* Load up conference info */
	app.Confs, err = getters.ListConferences(app.Notion)
	if err != nil {
		app.Err.Fatal(err)
	}

	/* Set up Routes + Templates */
	routes, err := handlers.Routes(&app)
	if err != nil {
		app.Err.Fatal(err)
	}


	/* If we're using the CLN backend, init
	 * the checkout runner */
	 if app.Env.UseCLN {
		err = setupCLNCheckout(&app)
		if err != nil {
			app.Err.Printf("Warning: CLN checkout setup failed: %v", err)
			app.Env.UseCLN = false
			app.Err.Printf("CLN checkout disabled, Falling back to OpenNode")
		}
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

func setupCLNCheckout(app *config.AppContext) error {
	label := "btcpp"
	err := checkout.Init(app.Env.CLN.Hostname, app.Env.CLN.Pubkey, app.Env.CLN.Rune, label)
	if err != nil {
		return err
	}

	msgbus := make(chan *checkout.InvoiceEvent)
	err = checkout.RegisterForInvoiceUpdates(msgbus)
	if err != nil {
		return err
	}

	/* FIXME: pull out last updated index? */
	lastIndex := uint64(0)

	/* Run a loop for handling invoice event notifications! */
	go func(msgchan chan *checkout.InvoiceEvent) {
		for {
			inv := <-msgbus
			handled := getters.HandleCLNInvoiceEvent(app, inv)
			if !handled {
				/* FIXME: loop until it is handled? */
				app.Err.Printf("Unable to handle invoice event %v", inv)
			}

			/* FIXME: write to disk? */
			lastIndex = inv.UpdateIndex + 1
		}
	}(msgbus)

	return checkout.StartInvoiceWatch(lastIndex)
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
