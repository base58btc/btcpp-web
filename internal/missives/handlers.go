package missives

import (
	"fmt"
	"net/http"
	"net/mail"
	"sort"
	"strconv"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/emails"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/mtypes"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

func RegisterNewsletterHandlers(r *mux.Router, ctx *config.AppContext) {
	r.HandleFunc("/{newsletter}/subscribe", func(w http.ResponseWriter, r *http.Request) {
		SubscribeEmail(w, r, ctx)
	}).Methods("POST")

	r.HandleFunc("/confirm/{token}", func(w http.ResponseWriter, r *http.Request) {
		ConfirmEmail(w, r, ctx)
	}).Methods("GET")

	r.HandleFunc("/newsletter/unsubscribe/{token}", func(w http.ResponseWriter, r *http.Request) {
		UnsubscribeEmail(w, r, ctx)
	}).Methods("GET")

	r.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		Login(w, r, ctx)
	}).Methods("POST")

	r.HandleFunc("/{newsletter}/schedule", func(w http.ResponseWriter, r *http.Request) {
		ScheduleNewsMissives(w, r, ctx)
	}).Methods("GET")

	r.HandleFunc("/missives/schedule/MISS-{uid}", func(w http.ResponseWriter, r *http.Request) {
		ScheduleNewsMissive(w, r, ctx)
	}).Methods("GET")

	r.HandleFunc("/missives/unschedule/MISS-{uid}", func(w http.ResponseWriter, r *http.Request) {
		UnscheduleNewsMissive(w, r, ctx)
	}).Methods("GET")

	r.HandleFunc("/missives/preview/MISS-{uid}", func(w http.ResponseWriter, r *http.Request) {
		PreviewMissive(w, r, ctx)
	}).Methods("GET")

	r.HandleFunc("/missives/port", func(w http.ResponseWriter, r *http.Request) {
		PortRegistrationsToNewsletters(w, r, ctx)
	}).Methods("GET")
}

func PortRegistrationsToNewsletters(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ctx.InProduction {
		w.Write([]byte("no way! in prod"))
		return
	}
	rezzies, err := getters.FetchBtcppRegistrations(ctx, false)
	if err != nil {
		ctx.Err.Println(err)
		return
	}

	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Println(err)
		return
	}
	for _, rez := range rezzies {
		conf := helpers.FindConfByRef(confs, rez.ConfRef)

		newsletters := make([]string, 1)

		if conf == nil {
			newsletters[0] = "other"
		} else {
			newsletters[0] = conf.Tag
		}
		/* Also add their type + conf-type! */
		newsletters = append(newsletters, rez.Type)
		newsletters = append(newsletters, newsletters[0] + "-" + rez.Type)
		if rez.Type == "local" {
			newsletters = append(newsletters, "genpop")
			newsletters = append(newsletters, newsletters[0] + "-genpop")
		}

		_, err := getters.SubscribeEmailList(ctx.Notion, rez.Email, newsletters)
		if err != nil {
			ctx.Err.Println(err)
			continue
		}
	}

	w.Write([]byte("ok!"))
}

type TextData struct {
	Text string
}

func SubscribeEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	newsletter := params["newsletter"]

	r.ParseForm()
	email := r.Form.Get("newsletter-email")

	/* We're returning html */
	w.Header().Set("Content-Type", "text/html")

	/* Validate email */
	if _, err := mail.ParseAddress(email); err != nil {
		msg := fmt.Sprintf("\"%s\" not a valid email. Try again.", email)
		err = ctx.TemplateCache.ExecuteTemplate(w, "section/err.tmpl", &TextData{
			Text: msg,
		})
		if err != nil {
			ctx.Err.Printf("nope. %s", err)
		}
		return
	}

	timestamp := uint64(time.Now().UTC().UnixNano())
	_, token := helpers.GetSubscribeToken(ctx.Env.HMACKey[:], email, newsletter, timestamp)

	ctx.Infos.Printf("%s subscribe token is %s. sending confirmation email", email, token)
	_, err := emails.SendNewsletterSubEmail(ctx, email, token, newsletter)
	if err != nil {
		ctx.Infos.Printf("Unable to send mail to %s: %s", email, err)
		msg := fmt.Sprintf("Unable to subscribe \"%s\". Try again.", email)
		err = ctx.TemplateCache.ExecuteTemplate(w, "section/err.tmpl", &TextData{
			Text: msg,
		})
		if err != nil {
			ctx.Err.Printf("nope. %s", err)
		}
		return
	}

	msg := fmt.Sprintf("Subscription confirmation sent to %s.", email)

	err = ctx.TemplateCache.ExecuteTemplate(w, "section/ok.tmpl", &TextData{
		Text: msg,
	})
	if err != nil {
		ctx.Err.Printf("nope. %s", err)
	}
}

type SubscribePage struct {
	Year       uint
	Confs      []*types.Conf
	Email      string
	Text       string
	Newsletter string
}

func ConfirmEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* If there's no token-key, redirect to the front page */
	params := mux.Vars(r)
	token := params["token"]

	if token == "" {
		ctx.Infos.Printf("No token found for newsletter confirmation request")
		/* Return the homepage page */
		http.Redirect(w, r, "/#newsletter?b=sub_fail", http.StatusSeeOther)
		return
	}

	subToken, err := ParseSubscribeToken(ctx.Env.HMACKey[:], token)
	if err != nil {
		ctx.Infos.Printf("Email subscribe token validation failed. %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	/* Add to email list */
	subscriber, err := getters.FindSubscriber(ctx.Notion, subToken.Email)
	if err != nil {
		ctx.Infos.Printf("Subscribe failed for newsletter confirmation request %s: %s", subToken.Email, err)
		/* FIXME: show an error banner or something */
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if subscriber == nil {
		subscriber, err = getters.SubscribeEmail(ctx.Notion, subToken.Email, subToken.Newsletter)
		if err != nil {
			ctx.Infos.Printf("Subscribe failed for newsletter confirmation request %s: %s", subToken.Email, err)
			/* FIXME: show an error banner or something */
			/* Return the homepage page */
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}

	changed := subscriber.AddSubscription(subToken.Newsletter)
	if changed {
		/* Send Subscriptions (if any) */
		err = NewSubscriberMissives(ctx, subscriber, subToken.Newsletter)
		if err != nil {
			ctx.Infos.Printf("Missive subscribe failed for newsletter confirmation %s: %s", subToken.Email, err)
			/* FIXME: show an error banner or something */
			/* Return the homepage page */
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		err = getters.UpdateSubs(ctx.Notion, subscriber)
		if err != nil {
			ctx.Infos.Printf("Subscribe failed for newsletter confirmation request %s: %s", subToken.Email, err)
			/* FIXME: show an error banner or something */
			/* Return the homepage page */
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}

	if err != nil {
		ctx.Err.Printf("Subscribe failed for newsletter confirmation request %s: %s", subToken.Email, err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var confs types.ConfList
	confs, _ = getters.FetchConfsCached(ctx)
	sort.Sort(confs)

	err = ctx.TemplateCache.ExecuteTemplate(w, "emails/subscribe_ok.tmpl", &SubscribePage{
		Confs:      confs,
		Text:       "Subscribed Success",
		Email:      subToken.Email,
		Newsletter: subToken.Newsletter,
	})

	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/emails/unsubscribe exec template failed %s\n", err.Error())
		return
	}
}

func UnsubscribeEmail(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* If there's no token-key, redirect to the front page */
	params := mux.Vars(r)
	token := params["token"]

	subToken, err := ParseSubscribeToken(ctx.Env.HMACKey[:], token)
	if err != nil {
		ctx.Infos.Printf("Invalid token %s for unsubscribe: %s", token, err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	/* Find record for that token */
	subscriber, err := getters.FindSubscriber(ctx.Notion, subToken.Email)
	if err != nil || subscriber == nil {
		ctx.Infos.Printf("No subscriber found for token %s (%s)", token, subToken.Email)
		if err != nil {
			ctx.Err.Printf("error: %s", err)
		}
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	changed := subscriber.RmSubscription(subToken.Newsletter)
	if changed {

		/* Update on Notion */
		err := getters.UpdateSubs(ctx.Notion, subscriber)
		if err != nil {
			ctx.Infos.Printf("notion error: unsubscribing %s from %s: %s", subscriber.Email, subToken.Newsletter, err)
		}

		/* Update with mailer */
		err = emails.SendSubDeleteRequest(ctx, subToken.Email, subToken.Newsletter)
		if err != nil {
			ctx.Infos.Printf("mailer error: unsubscribing %s from %s: %s", subscriber.Email, subToken.Newsletter, err)
		} else {
			ctx.Infos.Printf("Unsubscribed %s from %s", subscriber.Email, subToken.Newsletter)
		}
	} else {
		ctx.Infos.Printf("Subscriber %s already unsubscribed from %s", subscriber.Email, subToken.Newsletter)
	}

	// Render the template with the data
	var confs types.ConfList
	confs, _ = getters.FetchConfsCached(ctx)
	sort.Sort(confs)

	err = ctx.TemplateCache.ExecuteTemplate(w, "emails/unsubscribe_ok.tmpl", &SubscribePage{
		Year:       helpers.CurrentYear(),
		Confs:      confs,
		Email:      subscriber.Email,
		Text:       "Sorry to see you go",
		Newsletter: subToken.Newsletter,
	})

	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/emails/subscribe exec template failed %s\n", err.Error())
		return
	}
}

type LoginPage struct {
	Year        uint
	Destination string
}

/* Set the pin cookie and redirect to destination */
func Login(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	r.ParseForm()
	password := r.Form.Get("pass")
	destpath := r.Form.Get("dest")

	if password != ctx.Env.RegistryPin {
		w.Write([]byte(`
		<div class="form_message-error" style="display: block;">
                  <div class="error-text">Incorrect password. Try again.</div>
                </div>
		`))
		return
	}

	/* Set the pin as cookie and redirect */
	ctx.Session.Put(r.Context(), "pin", password)

	/* Use HTMX to redirect */
	w.Header().Set("HX-Redirect", destpath)
}

func Render401(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	err := ctx.TemplateCache.ExecuteTemplate(w, "401.tmpl", &LoginPage{
		Year:        helpers.CurrentYear(),
		Destination: r.URL.Path,
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/401.tmpl exec template failed %s\n", err.Error())
		return
	}
}

func PreviewMissive(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* Check for verified */
	if ok := checkPin(w, r, ctx); !ok {
		Render401(w, r, ctx)
		return
	}

	params := mux.Vars(r)
	uniqueID := params["uid"]

	uid, err := strconv.ParseUint(uniqueID, 10, 64)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives uid (%s): %s", uniqueID, err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	missive, err := getters.GetLetter(ctx.Notion, uid)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	subscribers := []*mtypes.Subscriber{
		&mtypes.Subscriber{
			Email: "hello@btcpp.dev",
			Subs: []*mtypes.Subscription{
				&mtypes.Subscription {
					Name: missive.Newsletters[0],
				},
			},
		},
		&mtypes.Subscriber{
			Email: "niftynei@gmail.com",
			Subs: []*mtypes.Subscription{
				&mtypes.Subscription {
					Name: missive.Newsletters[0],
				},
			},
		},
	}

	body, _, err := scheduleMissive(ctx, subscribers, missive, true)
	if err != nil {
		ctx.Infos.Printf("Unable to send missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx.Infos.Printf("Scheduled preview emails for %s", missive.Title)
	w.Write(body)
}


func ScheduleNewsMissive(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* Check for verified */
	if ok := checkPin(w, r, ctx); !ok {
		Render401(w, r, ctx)
		return
	}

	params := mux.Vars(r)
	uniqueID := params["uid"]

	uid, err := strconv.ParseUint(uniqueID, 10, 64)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives uid (%s): %s", uniqueID, err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	missive, err := getters.GetLetter(ctx.Notion, uid)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	subscribers, err := getters.ListSubscribersFor(ctx.Notion, missive.Newsletters)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	letters := []*mtypes.Letter { missive }
	err = scheduleMissives(ctx, subscribers, letters)
	if err != nil {
		ctx.Infos.Printf("Unable to send missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx.Infos.Printf("Scheduled emails for %s", missive.Title)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func ScheduleNewsMissives(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* Check for verified */
	if ok := checkPin(w, r, ctx); !ok {
		Render401(w, r, ctx)
		return
	}

	params := mux.Vars(r)
	newsletter := params["newsletter"]

	subscribers, err := getters.ListSubscribers(ctx.Notion, newsletter)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	letters, err := getters.GetLetters(ctx.Notion, newsletter)
	if err != nil {
		ctx.Infos.Printf("Unable to send missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	err = scheduleMissives(ctx, subscribers, letters)
	if err != nil {
		ctx.Infos.Printf("Unable to send missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	ctx.Infos.Printf("Scheduled emails for %s", newsletter)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func UnscheduleNewsMissive(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	/* Check for verified */
	if ok := checkPin(w, r, ctx); !ok {
		Render401(w, r, ctx)
		return
	}

	params := mux.Vars(r)
	uniqueID := params["uid"]

	uid, err := strconv.ParseUint(uniqueID, 10, 64)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives uid (%s): %s", uniqueID, err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	missive, err := getters.GetLetter(ctx.Notion, uid)
	if err != nil {
		ctx.Infos.Printf("Unable to schedule missives: %s", err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	err = emails.SendCancelMissiveRequest(ctx, missive)
	if err != nil {
		ctx.Infos.Printf("Unable to unschedule missive %s: %s", missive, err)
		/* Return the homepage page */
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

