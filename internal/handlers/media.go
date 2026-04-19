package handlers

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"

	"github.com/gorilla/mux"
)

func AddMediaRoutes(r *mux.Router, app *config.AppContext) {
	r.HandleFunc("/media/preview/{conf}/{type}/{card}", func(w http.ResponseWriter, r *http.Request) {
		params := mux.Vars(r)
		cardtype := params["type"]
		switch cardtype {
		case "speaker":
			PreviewSpeakerCard(w, r, app)
		case "talk":
			PreviewTalkCard(w, r, app)
		case "sponsor":
			PreviewSponsorCard(w, r, app)
		case "agenda":
			confTag := params["conf"]
			ref := params["card"]
			/* Main stage preview! */
			MakeAgendaCard(w, r, app, confTag, ref, "one")
		default:
			handle404(w, r, app)
		}
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/{type}", func(w http.ResponseWriter, r *http.Request) {
		params := mux.Vars(r)
		cardtype := params["type"]
		switch cardtype {
		case "speakers":
			GenSpeakerCards(w, r, app)
		case "talks":
			GenTalkCards(w, r, app)
		case "agenda":
			GenAgendaCards(w, r, app)
		default:
			handle404(w, r, app)
		}
	}).Methods("GET")

	/* Gen both talk + speaker cards */
	r.HandleFunc("/media/imgs/{conf}", func(w http.ResponseWriter, r *http.Request) {
		GenSpeakerCards(w, r, app)
		GenTalkCards(w, r, app)
		GenAgendaCards(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/speaker/{card}/{talk}/{speaker}", func(w http.ResponseWriter, r *http.Request) {
		MakeSpeakerCard(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/talk/{card}/{talk}", func(w http.ResponseWriter, r *http.Request) {
		MakeTalkCard(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/png/{conf}/speaker/{card}/{talk}/{speaker}", func(w http.ResponseWriter, r *http.Request) {
		ServeSpeakerPng(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/png/{conf}/talk/{card}/{talk}", func(w http.ResponseWriter, r *http.Request) {
		ServeTalkPng(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/sponsor/{card}/{sponsorRef}", func(w http.ResponseWriter, r *http.Request) {
		MakeSponsorCard(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/png/{conf}/sponsor/{card}/{sponsorRef}", func(w http.ResponseWriter, r *http.Request) {
		ServeSponsorPng(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/agenda/{ref}/{venue}", func(w http.ResponseWriter, r *http.Request) {
		params := mux.Vars(r)
		confTag := params["conf"]
		ref := params["ref"]
		venue := params["venue"]
		MakeAgendaCard(w, r, app, confTag, ref, venue)
	}).Methods("GET")
}

type TalkCard struct {
	ConfTag string
	Talk    *types.Talk
}

type SpeakerCard struct {
	ConfTag   string
	TalkTitle string
	TalkImg   string
	Name      string
	Twitter   string
}

type SponsorCard struct {
	ConfTag       string
	SponsorName   string
	SponsorLogo   string
	SponsorLevel  string
	BackgroundImg string
	Twitter       string
	Website       string
}

type SessionCard struct {
	ConfTag  string
	Venue    string
	Sessions []*types.Session
}

func GenSpeakerCards(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ctx.Env.Prod {
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	/* Make sure the directory exists */
	path := fmt.Sprintf("media/%s/speakers", confTag)
	err := helpers.MakeDir(path)
	if err != nil {
		ctx.Err.Printf("can't make dir %s: %s", path, err)
		return
	}

	talks, _ := getters.GetTalksFor(ctx, confTag)
	/* Get talks for this conf */
	for _, talk := range talks {
		if talk.Type == "hackathon" {
			continue
		}
		for _, speaker := range talk.Speakers {
			for _, card := range types.MediaCards {
				img, err := helpers.MakeSpeakerPng(ctx, confTag, card, speaker.ID, talk.ID)
				if err != nil {
					ctx.Err.Printf("oh no can't make speaker image %s: %s", speaker.Name, err)
					return
				}

				ctx.Infos.Printf("made image for (%s) %s", card, speaker.Name)

				imgName := strings.Split(speaker.Photo, ".")[0]
				fileName := fmt.Sprintf("%s/%s-%s.png", path, imgName, card)
				err = os.WriteFile(fileName, img, 0644)
				if err != nil {
					ctx.Err.Printf("oh no can't write speaker image %s: %s", speaker.Name, err)
					return
				}
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

func GenTalkCards(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ctx.Env.Prod {
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]

	/* Make sure the directory exists */
	path := fmt.Sprintf("media/%s/talks", confTag)
	err := helpers.MakeDir(path)
	if err != nil {
		ctx.Err.Printf("can't make dir %s: %s", path, err)
		return
	}

	talks, _ := getters.GetTalksFor(ctx, confTag)
	// FIXME: do all sizes for talks ?
	card := "1080p"
	for _, talk := range talks {
		img, err := helpers.MakeTalkPng(ctx, confTag, card, talk.ID)
		if err != nil {
			ctx.Err.Printf("oh no can't make talk image %s: %s", talk.Name, err)
			return
		}

		ctx.Infos.Printf("made image for (%s) %s", card, talk.Name)

		imgName := strings.Split(talk.Clipart, ".")[0]
		fileName := fmt.Sprintf("%s/%s-%s.png", path, imgName, card)
		err = os.WriteFile(fileName, img, 0644)
		if err != nil {
			ctx.Err.Printf("oh no can't write talk image %s: %s", talk.Name, err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
}

func GenAgendaCards(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ctx.Env.Prod {
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	/* Make sure the directory exists */
	path := fmt.Sprintf("media/%s/agendas", conf.Tag)
	err = helpers.MakeDir(path)
	if err != nil {
		ctx.Err.Printf("can't make dir %s: %s", path, err)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("error getting talks %s", err)
		return
	}

	days, err := talkDays(ctx, conf, talks)
	if err != nil {
		ctx.Err.Printf("error bucketing talks %s", err)
		return
	}

	/* Get talks for this conf */
	for _, day := range days {
		venues := day.Venues()
		for _, venue := range venues {
			for char, daytime := range types.DayTimeChars {
				dayref := fmt.Sprintf("%d%s", day.Idx, char)
				img, err := helpers.MakeAgendaImg(ctx, conf.Tag, dayref, venue)
				if err != nil {
					ctx.Err.Printf("oh no can't make agenda image %s (%s): %s", dayref, venue, err)
					return
				}

				ctx.Infos.Printf("made image for %s-%s", dayref, venue)

				venueName := strings.Split(types.NameVenue(venue), " ")[0]
				fileName := fmt.Sprintf("%s/day%d-%s-%s.png", path, day.Idx, venueName, daytime)
				err = os.WriteFile(fileName, img, 0644)
				if err != nil {
					ctx.Err.Printf("oh no can't write agenda image %s (%s): %s", dayref, venue, err)
					return
				}
			}

		}
	}

	w.WriteHeader(http.StatusOK)
}

func PreviewTalkCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ctx.Env.Prod {
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]

	template := fmt.Sprintf("media/talk_%s.tmpl", card)
	speakers := make([]*types.Speaker, 1)
	speakers[0] = &types.Speaker{
		Photo:   "niftynei.png",
		Twitter: types.ParseTwitter("niftynei"),
		Company: "bitcoin++",
		Name:    "lisa neigut",
	}

	err := ctx.TemplateCache.ExecuteTemplate(w, template, &TalkCard{
		ConfTag: confTag,
		Talk: &types.Talk{
			Clipart:  "riga_clock.png",
			Name:     "This is a very long talk Name: one that goes way too far",
			Speakers: speakers,
		},
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec talk_%s failed", card)
		return
	}
}

func MakeTalkCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]
	talkID := params["talk"]

	/* Find talk! */
	talk, err := getters.GetTalk(ctx, talkID)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	tmplName := fmt.Sprintf("media/talk_%s.tmpl", card)
	if ctx.TemplateCache.Lookup(tmplName) == nil {
		tmplName = "media/talk_1080p.tmpl"
	}
	err = ctx.TemplateCache.ExecuteTemplate(w, tmplName, &TalkCard{
		ConfTag: confTag,
		Talk:    talk,
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec %s failed: %s", tmplName, err.Error())
		return
	}
}

func PreviewSpeakerCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ctx.Env.Prod {
		return
	}

	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]

	template := fmt.Sprintf("media/%s_%s.tmpl", confTag, card)
	err := ctx.TemplateCache.ExecuteTemplate(w, template, &SpeakerCard{
		ConfTag:   confTag,
		Name:      "Speaker's Name",
		TalkTitle: "This is a very long talk Name: one that goes way too far",
		TalkImg:   "riga_clock.png",
		Twitter:   "niftynei",
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec speaker_social failed")
		return
	}
}

func MakeSpeakerCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]
	talkID := params["talk"]
	sID := params["speaker"]

	/* Find talk! */
	talk, err := getters.GetTalk(ctx, talkID)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	/* Find speaker! */
	var speaker *types.Speaker
	for _, sp := range talk.Speakers {
		if sp.ID == sID {
			speaker = sp
			break
		}
	}

	if speaker == nil {
		ctx.Err.Printf("unable to find speaker %s for talk %s", sID, talk.Name)
		return
	}

	template := fmt.Sprintf("media/speaker_%s.tmpl", card)
	err = ctx.TemplateCache.ExecuteTemplate(w, template, &SpeakerCard{
		ConfTag:   confTag,
		Name:      speaker.Name,
		TalkTitle: talk.Name,
		TalkImg:   talk.Clipart,
		Twitter:   speaker.TwitterHandle(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec speaker_social failed")
		return
	}
}

func MakeAgendaCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext, confTag, dayref, venue string) {
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	/* Find talk! */
	talks, err := getters.GetTalksFor(ctx, confTag)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("Unable to fetch talks from Notion!! %s", err.Error())
		return
	}

	days, err := talkDays(ctx, conf, talks)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("unable to bucket talk days %s", err)
		return
	}

	/* Filter for only in particular venue */
	sessions, err := filterSessions(days, dayref, venue)
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("unable to pick sessions %s: %s", dayref, err)
		return
	}

	template := "media/agenda_1080p.tmpl"
	err = ctx.TemplateCache.ExecuteTemplate(w, template, &SessionCard{
		ConfTag:  confTag,
		Venue:    types.NameVenue(venue),
		Sessions: sessions,
	})

	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec agenda card failed")
		return
	}
}

func ServeSpeakerPng(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]
	talkID := params["talk"]
	speakerID := params["speaker"]

	png, err := helpers.MakeSpeakerPng(ctx, confTag, card, speakerID, talkID)
	if err != nil {
		http.Error(w, "Failed to generate image", http.StatusInternalServerError)
		ctx.Err.Printf("failed to generate speaker png: %s", err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

func ServeTalkPng(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]
	talkID := params["talk"]

	png, err := helpers.MakeTalkPng(ctx, confTag, card, talkID)
	if err != nil {
		http.Error(w, "Failed to generate image", http.StatusInternalServerError)
		ctx.Err.Printf("failed to generate talk png: %s", err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}

func findSponsorBackground(confTag string) string {
	return "/static/img/" + confTag + "/leading.png"
}

func PreviewSponsorCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]

	bgImg := findSponsorBackground(confTag)

	tmplName := fmt.Sprintf("media/sponsor_%s.tmpl", card)
	err := ctx.TemplateCache.ExecuteTemplate(w, tmplName, &SponsorCard{
		ConfTag:       confTag,
		SponsorName:   "Unchained",
		SponsorLogo:   "/static/img/sponsors/unchained_white.svg",
		SponsorLevel:  "Sponsor",
		BackgroundImg: bgImg,
		Twitter:       "unchained",
		Website:       "unchained.com",
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("exec %s failed: %s", tmplName, err.Error())
	}
}

func MakeSponsorCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]
	sponsorRef := params["sponsorRef"]

	sponsorships, err := getters.ListSponsorships(ctx, "")
	if err != nil {
		http.Error(w, "Unable to load sponsorships", http.StatusInternalServerError)
		ctx.Err.Printf("sponsor card: failed to load sponsorships: %s", err.Error())
		return
	}

	var sp *types.Sponsorship
	for _, s := range sponsorships {
		if s.Ref == sponsorRef {
			sp = s
			break
		}
	}

	if sp == nil || sp.Org == nil {
		http.Error(w, "Sponsorship not found", http.StatusNotFound)
		return
	}

	// Use dark logo for the card, prefer it from Spaces
	logo := sp.Org.LogoDark
	if logo == "" {
		logo = sp.Org.LogoLight
	}
	// If logo is a full URL, use it as-is; otherwise treat as local path
	if logo != "" && !strings.HasPrefix(logo, "http") {
		logo = "/static/img/sponsors/" + logo
	}

	bgImg := findSponsorBackground(confTag)

	tmplName := fmt.Sprintf("media/sponsor_%s.tmpl", card)
	if ctx.TemplateCache.Lookup(tmplName) == nil {
		tmplName = "media/sponsor_1080p.tmpl"
	}

	level := sp.Level
	if strings.EqualFold(level, "Bronze") {
		level = "Sponsor"
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, tmplName, &SponsorCard{
		ConfTag:       confTag,
		SponsorName:   sp.Org.Name,
		SponsorLogo:   logo,
		SponsorLevel:  level,
		BackgroundImg: bgImg,
		Twitter:       sp.Org.Twitter.Handle,
		Website:       sp.Org.Website,
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("exec %s failed: %s", tmplName, err.Error())
	}
}

func ServeSponsorPng(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	params := mux.Vars(r)
	confTag := params["conf"]
	card := params["card"]
	sponsorRef := params["sponsorRef"]

	png, err := helpers.MakeSponsorPng(ctx, confTag, card, sponsorRef)
	if err != nil {
		http.Error(w, "Failed to generate image", http.StatusInternalServerError)
		ctx.Err.Printf("failed to generate sponsor png: %s", err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Write(png)
}
