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
	r.HandleFunc("/media/preview/{conf}/speaker/{card}", func(w http.ResponseWriter, r *http.Request) {
		PreviewSpeakerCard(w, r, app)
	}).Methods("GET")
	r.HandleFunc("/media/preview/{conf}/talk/{card}", func(w http.ResponseWriter, r *http.Request) {
		PreviewTalkCard(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/speakers", func(w http.ResponseWriter, r *http.Request) {
		GenSpeakerCards(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/talks", func(w http.ResponseWriter, r *http.Request) {
		GenTalkCards(w, r, app)
	}).Methods("GET")

	/* Gen both talk + speaker cards */
	r.HandleFunc("/media/imgs/{conf}", func(w http.ResponseWriter, r *http.Request) {
		GenSpeakerCards(w, r, app)
		GenTalkCards(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/speaker/{card}/{talk}/{speaker}", func(w http.ResponseWriter, r *http.Request) {
		MakeSpeakerCard(w, r, app)
	}).Methods("GET")

	r.HandleFunc("/media/imgs/{conf}/talk/{card}/{talk}", func(w http.ResponseWriter, r *http.Request) {
		MakeTalkCard(w, r, app)
	}).Methods("GET")
}

type TalkCard struct {
	ConfTag   string
	Talk      *types.Talk
}

type SpeakerCard struct {
	ConfTag   string
	TalkTitle string
	TalkImg   string
	Name      string
	Twitter   string
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
				img, err := helpers.MakeSpeakerImg(ctx, confTag, card, speaker.ID, talk.ID)
				if err != nil {
					ctx.Err.Printf("oh no can't make speaker image %s: %s", speaker.Name, err)
					return
				}
				
				ctx.Infos.Printf("made image for (%s) %s", card, speaker.Name)

				imgName := strings.Split(speaker.Photo, ".")[0]
				fileName := fmt.Sprintf("%s/%s-%s.pdf", path, imgName, card)
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
	// FIXME: do all sizes for talks 
	card := "1080p"
	for _, talk := range talks {
		img, err := helpers.MakeTalkImg(ctx, confTag, card, talk.ID)
		if err != nil {
			ctx.Err.Printf("oh no can't make talk image %s: %s", talk.Name, err)
			return
		}
		
		ctx.Infos.Printf("made image for (%s) %s", card, talk.Name)

		imgName := strings.Split(talk.Clipart, ".")[0]
		fileName := fmt.Sprintf("%s/%s-%s.pdf", path, imgName, card)
		err = os.WriteFile(fileName, img, 0644)
		if err != nil {
			ctx.Err.Printf("oh no can't write talk image %s: %s", talk.Name, err)
			return
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
		Photo:     "niftynei.png",
		Twitter:   "https://x.com/niftynei",
		Company:   "bitcoin++",
		Name:      "lisa neigut",
	}
	
	err := ctx.TemplateCache.ExecuteTemplate(w, template, &TalkCard{
		ConfTag: confTag,
		Talk: &types.Talk{
			Clipart: "riga_clock.png",
			Name: "This is a very long talk Name: one that goes way too far",
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

	/* Find speaker! */
	template := fmt.Sprintf("media/talk_%s.tmpl", card)
	err = ctx.TemplateCache.ExecuteTemplate(w, template, &TalkCard{
		ConfTag: confTag,
		Talk: talk,
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec speaker_social failed")
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

	template := fmt.Sprintf("media/%s_%s.tmpl", card)
	err := ctx.TemplateCache.ExecuteTemplate(w, template, &SpeakerCard{
		ConfTag: confTag,
		Name: "Speaker's Name",
		TalkTitle: "This is a very long talk Name: one that goes way too far",
		TalkImg: "riga_clock.png",
		Twitter: "niftynei",
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec speaker_social failed")
		return
	}
}

func MakeSpeakerCard(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ctx.Env.Prod {
		return
	}

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
		ConfTag: confTag,
		Name: speaker.Name,
		TalkTitle: talk.Name,
		TalkImg: talk.Clipart,
		Twitter: speaker.TwitterHandle(),
	})
	if err != nil {
		http.Error(w, "Unable to load page, please try again later", http.StatusInternalServerError)
		ctx.Err.Printf("exec speaker_social failed")
		return
	}
}

