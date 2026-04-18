package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"text/template"

	"btcpp-web/external/buffer"
	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

var speakerPostTmpl = template.Must(template.New("speaker").Parse(
	`JUST IN {{.Conf.Emoji}}: {{.SpeakerName}} `+
		`{{if .TwitterHandle}}(@{{.TwitterHandle}}) {{end}}` +
                `{{ if .Org }}of {{ .Org }} {{ end }}` +
                `to speak at {{.Conf.Desc}} this coming {{ .Conf.DateDesc }}` +
		`{{if .TalkName}}` + "\n\n" + `~~{{.TalkName}}{{end}}~~` +
		"\n\n" + `Join us 👉  https://btcpp.dev/conf/{{.Conf.Tag}}#tickets`))

var talkPostTmpl = template.Must(template.New("talk").Parse(
	`JUST SCHEDULED {{ .Conf.Emoji }}: "{{.TalkName}}"` +
		` by{{range .Speakers }}` +
                ` {{ .Name }}{{ if .Twitter }} (@{{ .TwitterHandle }}) {{ end}}` +
		`{{ end }}` +
		"\n\n" + `Don't miss it. Join us in {{ .Conf.Location }} this {{ .Conf.DateDesc }} 👉  https://btcpp.dev/conf/{{.Conf.Tag}}#tickets`))

type speakerPostData struct {
	SpeakerName   string
	TwitterHandle string
        Org           string
	TalkName      string
        Conf          *types.Conf
}

type talkPostData struct {
	TalkName     string
        Speakers     []*types.Speaker
        Conf         *types.Conf
}

type channelFilter struct {
	Service string
	Name    string // if non-empty, channel name must contain this (case-insensitive)
}

var targetFilters = []channelFilter{
	{"twitter", "btcplusplus"},
	{"instagram", ""},
	{"linkedin", ""},
}

func isTargetChannel(ch buffer.Channel) bool {
	for _, f := range targetFilters {
		if ch.Service == f.Service {
			if f.Name == "" || strings.Contains(strings.ToLower(ch.Name), strings.ToLower(f.Name)) {
				return true
			}
		}
	}
	return false
}

func SocialAdmin(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load talks", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/social/%s failed to get talks: %s", conf.Tag, err.Error())
		return
	}

	// Build a map of speaker ID -> their talks
	speakerTalks := make(map[string][]*types.Talk)
	for _, talk := range talks {
		for _, speaker := range talk.Speakers {
			speakerTalks[speaker.ID] = append(speakerTalks[speaker.ID], talk)
		}
	}

	// Build deduplicated speaker rows
	seenSpeakers := make(map[string]bool)
	var speakerRows []*SocialSpeakerRow
	for _, talk := range talks {
		for _, speaker := range talk.Speakers {
			if seenSpeakers[speaker.ID] {
				continue
			}
			seenSpeakers[speaker.ID] = true

			// Prefer a talk where this speaker is the sole speaker
			bestTalk := talk
			for _, t := range speakerTalks[speaker.ID] {
				if len(t.Speakers) == 1 {
					bestTalk = t
					break
				}
			}

			// Only include talk name if the speaker is the sole speaker
			talkName := ""
			if len(bestTalk.Speakers) == 1 {
				talkName = bestTalk.Name
			}

			var buf bytes.Buffer
			speakerPostTmpl.Execute(&buf, &speakerPostData{
				Conf:          conf,
				SpeakerName:   speaker.Name,
                                Org:           speaker.Company,
				TwitterHandle: speaker.TwitterHandle(),
				TalkName:      talkName,
			})

			speakerPhotoURL := SpeakerPhotoURL(ctx, speaker.Photo)
			photoURL := SpeakerCardURL(ctx, conf.Tag, "1080p", speaker.ID, bestTalk.ID)
			instaURL := SpeakerCardURL(ctx, conf.Tag, "insta", speaker.ID, bestTalk.ID)
			speakerRows = append(speakerRows, &SocialSpeakerRow{
				ID:              speaker.ID,
				Name:            speaker.Name,
				TwitterHandle:   speaker.TwitterHandle(),
				TalkName:        talkName,
				SpeakerPhotoURL: speakerPhotoURL,
				PhotoURL:        photoURL,
				InstaPhotoURL:   instaURL,
				PostText:        buf.String(),
			})
		}
	}

	sort.SliceStable(speakerRows, func(i, j int) bool {
		return speakerRows[i].Name < speakerRows[j].Name
	})

	// Build talk rows
	var talkRows []*SocialTalkRow
	for _, talk := range talks {
		var speakerNames []string
		for _, s := range talk.Speakers {
			name := s.Name
			if h := s.TwitterHandle(); h != "" {
				name += " (@" + h + ")"
			}
			speakerNames = append(speakerNames, name)
		}

		var buf bytes.Buffer
		talkPostTmpl.Execute(&buf, &talkPostData{
                        Conf:         conf,
			TalkName:     talk.Name,
			Speakers: talk.Speakers,
		})

		var displayNames []string
		for _, s := range talk.Speakers {
			displayNames = append(displayNames, s.Name)
		}

		photoURL := TalkCardURL(ctx, conf.Tag, "1080p", talk.ID)
		talkRows = append(talkRows, &SocialTalkRow{
			ID:           talk.ID,
			Name:         talk.Name,
			SpeakerNames: strings.Join(displayNames, ", "),
			PostText:     buf.String(),
			PhotoURL:     photoURL,
		})
	}

	sort.SliceStable(talkRows, func(i, j int) bool {
		return talkRows[i].Name < talkRows[j].Name
	})

	err = ctx.TemplateCache.ExecuteTemplate(w, "talks/social.tmpl", &SocialAdminPage{
		Conf:         conf,
		SpeakerRows:  speakerRows,
		TalkRows:     talkRows,
		FlashMessage: r.URL.Query().Get("flash"),
		Year:         helpers.CurrentYear(),
		BufferOK:     buffer.IsConfigured(),
	})
	if err != nil {
		http.Error(w, "Unable to load page", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/social/%s template failed: %s", conf.Tag, err.Error())
	}
}

func SocialPost(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if ok := helpers.CheckPin(w, r, ctx); !ok {
		helpers.Render401(w, r, ctx)
		return
	}

	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	if !buffer.IsConfigured() {
		http.Error(w, "Buffer API not configured", http.StatusBadRequest)
		return
	}

	r.ParseForm()

	// Get target channels
	allChannels, err := buffer.FetchChannels()
	if err != nil {
		ctx.Err.Printf("/admin/social/%s/post failed to fetch channels: %s", conf.Tag, err.Error())
		http.Error(w, "Failed to fetch Buffer channels: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var targetChannels []buffer.Channel
	for _, ch := range allChannels {
		if isTargetChannel(ch) {
			targetChannels = append(targetChannels, ch)
		}
	}

	if len(targetChannels) == 0 {
		http.Error(w, "No target channels found in Buffer", http.StatusBadRequest)
		return
	}

	posted := 0

	// Process selected speakers
	for key := range r.Form {
		if !strings.HasPrefix(key, "speaker_") {
			continue
		}
		speakerID := strings.TrimPrefix(key, "speaker_")

		postText := r.FormValue("text_speaker_" + speakerID)
		if postText == "" {
			continue
		}

		speakerPhotoURL := r.FormValue("speakerphoto_speaker_" + speakerID)
		photoURL := r.FormValue("photo_speaker_" + speakerID)
		instaPhotoURL := r.FormValue("instaphoto_speaker_" + speakerID)

		for _, ch := range targetChannels {
			var imgs []string
			if ch.Service == "instagram" && instaPhotoURL != "" {
				imgs = append(imgs, instaPhotoURL)
			} else if photoURL != "" {
				imgs = append(imgs, photoURL)
			}
			if speakerPhotoURL != "" {
				imgs = append(imgs, speakerPhotoURL)
			}
			ctx.Infos.Printf("Posting speaker %s to %s with images: %v", speakerID, ch.Service, imgs)
			_, err := buffer.CreatePost(ch.ID, postText, imgs, ch.Service)
			if err != nil {
				ctx.Err.Printf("Failed to post speaker %s to %s: %s", speakerID, ch.Service, err.Error())
				continue
			}
			posted++
			ctx.Infos.Printf("Queued speaker post for %s to %s", speakerID, ch.Service)
		}
	}

	// Process selected talks
	for key := range r.Form {
		if !strings.HasPrefix(key, "talk_") {
			continue
		}
		talkID := strings.TrimPrefix(key, "talk_")

		postText := r.FormValue("text_talk_" + talkID)
		if postText == "" {
			continue
		}

		photoURL := r.FormValue("photo_talk_" + talkID)
		var imgs []string
		if photoURL != "" {
			imgs = append(imgs, photoURL)
		}

		for _, ch := range targetChannels {
			_, err := buffer.CreatePost(ch.ID, postText, imgs, ch.Service)
			if err != nil {
				ctx.Err.Printf("Failed to post talk %s to %s: %s", talkID, ch.Service, err.Error())
				continue
			}
			posted++
			ctx.Infos.Printf("Queued talk post for %s to %s", talkID, ch.Service)
		}
	}

	flash := fmt.Sprintf("%d posts queued to Buffer", posted)
	http.Redirect(w, r, "/admin/social/"+conf.Tag+"?flash="+strings.ReplaceAll(flash, " ", "+"), http.StatusFound)
}
