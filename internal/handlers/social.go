package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"text/template"
	"time"

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

var sponsorPostTmpl = template.Must(template.New("sponsor").Parse(
	`JUST IN: {{.OrgName}} ` +
	`{{if ne .Twitter.Handle "" }}({{.Twitter.Mention }}) {{end}}` +
        `to sponsor {{.Conf.Desc}} {{.Conf.Emoji}}` +
        `{{ if .Level }} as our {{ .Level }} sponsor{{ end }}` +
        "\n\n" + `Join us this {{ .Conf.DateDesc }} 👉  https://btcpp.dev/conf/{{.Conf.Tag}}#tickets`))

type sponsorPostData struct {
	OrgName string
	Twitter types.Twitter
	Website string
	Level   string
	Conf    *types.Conf
}

var sponsorBatchTmpl = template.Must(template.New("sponsor_batch").Parse(
	`JUST IN {{ .Conf.Emoji }}: @btcplusplus is thrilled to announce the sponsorship of{{ range .Sponsors }} {{ .OrgName }}{{ end }} for our upcoming {{ .Conf.Desc }} this {{ .Conf.DateDesc }} #bitcoin #btcplusplus #btcpp`))

type sponsorBatchData struct {
	Conf     *types.Conf
	Sponsors []*SocialSponsorRow
}

var sponsorLevelOrder = map[string]int{
	"Headline": 0,
	"Title":    1,
	"Workshop": 2,
	"Gold":     3,
	"Silver":   4,
	"Bronze":   5,
}

func sponsorSortOrder(level string) int {
	if order, ok := sponsorLevelOrder[level]; ok {
		return order
	}
	return 99
}

type selectedSponsor struct {
	ref     string
	text    string
	cardURL string
	level   string
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

	// Load already-posted refs to hide them
	postedRefs, err := getters.ListPostedRefs(ctx.Notion, conf)
	if err != nil {
		ctx.Err.Printf("/admin/social/%s failed to load posted refs: %s", conf.Tag, err.Error())
		postedRefs = make(map[string]bool)
	}

	talks, err := getters.LoadTalksFromConfTalks(ctx, conf.Tag)
	if err != nil {
		http.Error(w, "Unable to load talks", http.StatusInternalServerError)
		ctx.Err.Printf("/admin/social/%s failed to get conf talks: %s", conf.Tag, err.Error())
		return
	}

	// Kick off a card-refresh in the background. The OnTalksRefresh wiring
	// only fires on Talks-DB updates, which won't happen for ConfTalk-sourced
	// data — so we trigger it here. Hash and spaces.Exists checks make
	// re-runs cheap when nothing's changed.
	go RefreshTalkCards(ctx, talks)

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

			// Skip if already posted
			if postedRefs[helpers.SpeakerSocialPostRef(conf.Tag, bestTalk.ID, speaker.ID)] {
				continue
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
                                TalkID:          talk.ID,
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
		// Skip if already posted
		if postedRefs[helpers.TalkSocialPostRef(conf.Tag, talk.ID)] {
			continue
		}

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

	// Build sponsor rows
	var sponsorRows []*SocialSponsorRow
	sponsorships, err := getters.ListSponsorships(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("/admin/social/%s failed to get sponsorships: %s", conf.Tag, err.Error())
	} else {
		for _, sp := range sponsorships {
			if sp.Org == nil {
				continue
			}

			// Skip if already posted
			if postedRefs[helpers.SponsorSocialPostRef(conf.Tag, sp.Ref)] {
				continue
			}

                        level := sp.Level
                        if sp.Level == "Bronze" {
                                level = "" 
                        }

			var buf bytes.Buffer
			sponsorPostTmpl.Execute(&buf, &sponsorPostData{
				OrgName: sp.Org.Name,
				Twitter: sp.Org.Twitter,
				Website: sp.Org.Website,
				Level:   level,
				Conf:    conf,
			})

			cardURL := SponsorCardURL(ctx, conf.Tag, "1080p", sp.Ref)

			sponsorRows = append(sponsorRows, &SocialSponsorRow{
				Ref:      sp.Ref,
				OrgName:  sp.Org.Name,
				Twitter:  sp.Org.Twitter.Handle,
				Level:    sp.Level,
				CardURL:  cardURL,
				PostText: buf.String(),
			})
		}

		sort.SliceStable(sponsorRows, func(i, j int) bool {
			oi, oj := sponsorSortOrder(sponsorRows[i].Level), sponsorSortOrder(sponsorRows[j].Level)
			if oi != oj {
				return oi < oj
			}
			return sponsorRows[i].OrgName < sponsorRows[j].OrgName
		})
	}

	// Generate batch text for Instagram sponsor carousel
	var sponsorBatchText string
	if len(sponsorRows) > 0 {
		var buf bytes.Buffer
		sponsorBatchTmpl.Execute(&buf, &sponsorBatchData{
			Conf:     conf,
			Sponsors: sponsorRows,
		})
		sponsorBatchText = buf.String()
	}

	err = ctx.TemplateCache.ExecuteTemplate(w, "talks/social.tmpl", &SocialAdminPage{
		Conf:             conf,
		SpeakerRows:      speakerRows,
		TalkRows:         talkRows,
		SponsorRows:      sponsorRows,
		SponsorBatchText: sponsorBatchText,
		FlashMessage:     r.URL.Query().Get("flash"),
		Year:             helpers.CurrentYear(),
		BufferOK:         buffer.IsConfigured(),
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
		talkID := r.FormValue("talkid_speaker" + speakerID)
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
                        id := helpers.SpeakerSocialPostRef(conf.Tag, talkID, speakerID)
			getters.RecordSocialPost(ctx.Notion, id, postText, ch.Service, time.Now())
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
                        postid := helpers.TalkSocialPostRef(conf.Tag, talkID)
			getters.RecordSocialPost(ctx.Notion, postid, postText, ch.Service, time.Now())
		}
	}

	var selectedSponsors []selectedSponsor
	for key := range r.Form {
		if !strings.HasPrefix(key, "sponsor_") {
			continue
		}
		sponsorRef := strings.TrimPrefix(key, "sponsor_")
		postText := r.FormValue("text_sponsor_" + sponsorRef)
		if postText == "" {
			continue
		}
		cardURL := r.FormValue("card_sponsor_" + sponsorRef)
		level := r.FormValue("level_sponsor_" + sponsorRef)
		selectedSponsors = append(selectedSponsors, selectedSponsor{
			ref: sponsorRef, text: postText, cardURL: cardURL, level: level,
		})
	}

	// Sort by sponsor level order
	sort.SliceStable(selectedSponsors, func(i, j int) bool {
		return sponsorSortOrder(selectedSponsors[i].level) < sponsorSortOrder(selectedSponsors[j].level)
	})

	// For non-Instagram channels, send individual sponsor posts
	for _, sp := range selectedSponsors {
		var imgs []string
		if sp.cardURL != "" {
			imgs = append(imgs, sp.cardURL)
		}
		for _, ch := range targetChannels {
			if ch.Service == "instagram" {
				continue
			}
			_, err := buffer.CreatePost(ch.ID, sp.text, imgs, ch.Service)
			if err != nil {
				ctx.Err.Printf("Failed to post sponsor %s to %s: %s", sp.ref, ch.Service, err.Error())
				continue
			}
			posted++
			ctx.Infos.Printf("Queued sponsor post for %s to %s", sp.ref, ch.Service)
                        postid := helpers.SponsorSocialPostRef(conf.Tag, sp.ref)
			getters.RecordSocialPost(ctx.Notion, postid, sp.text, ch.Service, time.Now())
		}
	}

	// For Instagram, send one batch post with all sponsor images as a carousel
	if len(selectedSponsors) > 0 {
		batchText := r.FormValue("text_sponsor_batch")
		var batchImgs []string
		for _, sp := range selectedSponsors {
			if sp.cardURL != "" {
				batchImgs = append(batchImgs, sp.cardURL)
			}
		}
		if batchText != "" && len(batchImgs) > 0 {
			for _, ch := range targetChannels {
				if ch.Service != "instagram" {
					continue
				}
				_, err := buffer.CreatePost(ch.ID, batchText, batchImgs, ch.Service)
				if err != nil {
					ctx.Err.Printf("Failed to post sponsor batch to instagram: %s", err.Error())
					continue
				}
				posted++

				ctx.Infos.Printf("Queued sponsor batch post to instagram with %d images", len(batchImgs))
                                RecordInstagramBatch(ctx, conf, selectedSponsors, batchText, ch)
			}
		}
	}

	flash := fmt.Sprintf("%d posts queued to Buffer", posted)
	http.Redirect(w, r, "/admin/social/"+conf.Tag+"?flash="+strings.ReplaceAll(flash, " ", "+"), http.StatusFound)
}

func RecordInstagramBatch(ctx *config.AppContext, conf *types.Conf, sponsors []selectedSponsor, text string, channel buffer.Channel) {
        for _, sponsor := range sponsors {
                postid := helpers.SponsorSocialPostRef(conf.Tag, sponsor.ref)
                getters.RecordSocialPost(ctx.Notion, postid, text, channel.Service, time.Now())
        }
}
