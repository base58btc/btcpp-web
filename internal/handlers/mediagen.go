package handlers

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

var (
	cardHashes    = make(map[string]string)
	cardHashesMu  sync.Mutex
	refreshRunning int32
)

// readFileHead reads up to the first 1000 bytes of a file
func readFileHead(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, 1000)
	n, _ := f.Read(buf)
	return buf[:n]
}

func speakerCardHash(speaker *types.Speaker, talk *types.Talk) string {
	h := sha256.New()
	h.Write([]byte(speaker.Name))
	h.Write([]byte(speaker.Photo))
	h.Write([]byte(speaker.Twitter))
	h.Write([]byte(speaker.Company))
	h.Write([]byte(talk.Name))
	h.Write([]byte(talk.Clipart))
	h.Write(readFileHead("static/img/speakers/" + speaker.Photo))
	h.Write(readFileHead("static/img/talks/" + talk.Clipart))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func talkCardHash(talk *types.Talk) string {
	h := sha256.New()
	h.Write([]byte(talk.Name))
	h.Write([]byte(talk.Clipart))
	h.Write(readFileHead("static/img/talks/" + talk.Clipart))
	// Include speaker data so card updates when speakers change
	sort.Slice(talk.Speakers, func(i, j int) bool {
		return talk.Speakers[i].ID < talk.Speakers[j].ID
	})
	for _, s := range talk.Speakers {
		h.Write([]byte(s.Name))
		h.Write([]byte(s.Photo))
		h.Write(readFileHead("static/img/speakers/" + s.Photo))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func generateAndUploadSpeakerPng(ctx *config.AppContext, confTag, card string, speaker *types.Speaker, talk *types.Talk) (string, error) {
	key := fmt.Sprintf("%s/speakers/%s-%s-%s.png", confTag, talk.ID, speaker.ID, card)
	hash := speakerCardHash(speaker, talk)

	cardHashesMu.Lock()
	if cardHashes[key] == hash {
		cardHashesMu.Unlock()
                ctx.Infos.Printf("speaker media already exists %s (%s)", key, hash)
		return spaces.PublicURL(key), nil
	}
	cardHashesMu.Unlock()

        ctx.Infos.Printf("generating speaker media %s (%s)", key, hash)
	png, err := helpers.MakeSpeakerPng(ctx, confTag, card, speaker.ID, talk.ID)
	if err != nil {
		return "", fmt.Errorf("failed to generate speaker png %s/%s: %w", talk.ID, speaker.Name, card, err)
	}

	url, err := spaces.Upload(key, png, "image/png", hash)
	if err != nil {
		return "", err
	}

	cardHashesMu.Lock()
	cardHashes[key] = hash
	cardHashesMu.Unlock()

	ctx.Infos.Printf("media refresh: uploaded %s", key)
	return url, nil
}

func generateAndUploadTalkPng(ctx *config.AppContext, confTag, card string, talk *types.Talk) (string, error) {
	key := fmt.Sprintf("%s/talks/%s-%s.png", confTag, talk.ID, card)
	hash := talkCardHash(talk)

	cardHashesMu.Lock()
	if cardHashes[key] == hash {
		cardHashesMu.Unlock()
                ctx.Infos.Printf("talks media already exists %s (%s)", key, hash)
		return spaces.PublicURL(key), nil
	}
	cardHashesMu.Unlock()

        ctx.Infos.Printf("generating talks media %s (%s)", key, hash)
	png, err := helpers.MakeTalkPng(ctx, confTag, card, talk.ID)
	if err != nil {
		return "", fmt.Errorf("failed to generate talk png %s/%s: %w", talk.Name, card, err)
	}

	url, err := spaces.Upload(key, png, "image/png", hash)
	if err != nil {
		return "", err
	}

	cardHashesMu.Lock()
	cardHashes[key] = hash
	cardHashesMu.Unlock()

	// Save the card URL back to Notion if not already set
	if talk.TalkCardURL == "" {
		err = getters.TalkUpdateCardURL(ctx.Notion, talk.ID, url)
		if err != nil {
			ctx.Err.Printf("media refresh: failed to update TalkCardURL for %s: %s", talk.Name, err)
		}
	}

	ctx.Infos.Printf("media refresh: uploaded %s", key)
	return url, nil
}

func RefreshTalkCards(ctx *config.AppContext, talks []*types.Talk) {
	if !atomic.CompareAndSwapInt32(&refreshRunning, 0, 1) {
		ctx.Infos.Printf("media refresh: skipping, already running")
		return
	}
	defer atomic.StoreInt32(&refreshRunning, 0)

        confs, _ := getters.FetchConfsCached(ctx)
        confset := helpers.ConfTagSet(confs)

        card := "1080p"
        for _, talk := range talks {
                // Skip talks without a proper 'conf' attached
                conf, ok := confset[talk.Event]
                if !ok {
                        continue
                }

                // Skip nonactive conferences
                if !conf.Active {
                        continue
                }

                // Skip talks without clipart
                if talk.Clipart == "" {
                        continue
                }

                _, err := generateAndUploadTalkPng(ctx, talk.Event, card, talk)
                if err != nil {
                        ctx.Err.Printf("media refresh talks: %s", err)
                }

                for _, speaker := range talk.Speakers {
                        // Skip speakers without a photo
                        if speaker.Photo == "" {
                                continue
                        }
                        for _, cardtype := range []string{card, "insta", "social"} {
                                _, err := generateAndUploadSpeakerPng(ctx, talk.Event, cardtype, speaker, talk)
                                if err != nil {
                                        ctx.Err.Printf("media refresh speakers: %s", err)
                                }
                        }
                }
        }

        ctx.Infos.Printf("media refresh talks: finished (%d talks)", len(talks))
}

func RefreshSpeakerCards(ctx *config.AppContext, speakers []*types.Speaker) {
        ctx.Infos.Printf("skipping speaker cards")
}

func InitMediaRefresh(ctx *config.AppContext) {
	// Load existing hashes from S3 to avoid regenerating unchanged cards
	hashes, err := spaces.LoadHashes()
	if err != nil {
		ctx.Err.Printf("media refresh: failed to load hashes from spaces: %s", err)
	} else {
		cardHashesMu.Lock()
		for k, v := range hashes {
			cardHashes[k] = v
		}
		cardHashesMu.Unlock()
		ctx.Infos.Printf("media refresh: loaded %d existing hashes from spaces", len(hashes))
	}

	// Register callbacks so cards refresh when data changes
	getters.OnTalksRefresh(func(ctx *config.AppContext, talks []*types.Talk) {
		RefreshTalkCards(ctx, talks)
	})

	getters.OnSpeakersRefresh(func(ctx *config.AppContext, speakers []*types.Speaker) {
		RefreshSpeakerCards(ctx, speakers)
	})

	ctx.Infos.Println("Media card refresh callbacks registered")

	// Do an initial refresh with the data already loaded by WaitFetch
	talks, err := getters.FetchTalksCached(ctx)
	if err == nil && talks != nil {
		ctx.Infos.Println("Running initial media card refresh...")
		RefreshTalkCards(ctx, talks)
	}
}

// SpeakerCardURL returns the S3 URL for a speaker card, falling back to dynamic PNG route
func SpeakerCardURL(ctx *config.AppContext, confTag, card, speakerID, talkID string) string {
	if spaces.IsConfigured() {
		key := fmt.Sprintf("%s/speakers/%s-%s-%s.png", confTag, talkID, speakerID, card)
		return spaces.PublicURL(key)
	}
	return fmt.Sprintf("%s/media/png/%s/speaker/%s/%s/%s", ctx.Env.GetURI(), confTag, card, talkID, speakerID)
}

// TalkCardURL returns the S3 URL for a talk card, falling back to dynamic PNG route
func TalkCardURL(ctx *config.AppContext, confTag, card, talkID string) string {
	if spaces.IsConfigured() {
		key := fmt.Sprintf("%s/talks/%s-%s.png", confTag, talkID, card)
		return spaces.PublicURL(key)
	}
	return fmt.Sprintf("%s/media/png/%s/talk/%s/%s", ctx.Env.GetURI(), confTag, card, talkID)
}

// SponsorCardURL returns the URL for a sponsor card PNG
func SponsorCardURL(ctx *config.AppContext, confTag, card, sponsorRef string) string {
	return fmt.Sprintf("%s/media/png/%s/sponsor/%s/%s", ctx.Env.GetURI(), confTag, card, sponsorRef)
}

// SpeakerPhotoURL returns the URL for a speaker's photo
func SpeakerPhotoURL(ctx *config.AppContext, photo string) string {
	if photo == "" {
		return ""
	}
	if strings.HasPrefix(photo, "http") {
		return photo
	}
	return ctx.Env.GetURI() + "/static/img/speakers/" + photo
}
