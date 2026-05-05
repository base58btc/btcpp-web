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
	h.Write([]byte(speaker.Twitter.Handle))
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
		return spaces.PublicURL(key), nil
	}
	cardHashesMu.Unlock()

	// If already in Spaces, just record the hash without re-uploading
	if spaces.Exists(key) {
		cardHashesMu.Lock()
		cardHashes[key] = hash
		cardHashesMu.Unlock()
		return spaces.PublicURL(key), nil
	}

        ctx.Infos.Printf("generating speaker media %s (%s)", key, hash)
	png, err := helpers.MakeSpeakerPng(ctx, confTag, card, speaker.ID, talk.ID)
	if err != nil {
		return "", fmt.Errorf("failed to generate speaker png %s/%s: %w", speaker.Name, card, err)
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
		return spaces.PublicURL(key), nil
	}
	cardHashesMu.Unlock()

	// If already in Spaces, just record the hash without re-uploading.
	// Still set ConfTalk.SocialCard since we may have generated the file
	// in a previous run that didn't write back the path (or it got cleared).
	if spaces.Exists(key) {
		cardHashesMu.Lock()
		cardHashes[key] = hash
		cardHashesMu.Unlock()
		writeSocialCardPath(ctx, talk.ID, key, card)
		return spaces.PublicURL(key), nil
	}

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
	writeSocialCardPath(ctx, talk.ID, key, card)

	ctx.Infos.Printf("media refresh: uploaded %s", key)
	return url, nil
}

// writeSocialCardPath records the freshly-generated card's path on the
// ConfTalk row's SocialCard rich_text field. We only do this for the
// canonical 1080p card — the other sizes (insta, social) are speaker-only.
// Path format: "/{conf}/talks/{shortID}-{card}.png" — i.e., the Spaces
// key with a leading slash. No host included; the rendering side composes
// the URL.
func writeSocialCardPath(ctx *config.AppContext, talkID, key, card string) {
	if card != "1080p" {
		return
	}
	if err := getters.ConfTalkSetSocialCard(ctx.Notion, talkID, "/"+key); err != nil {
		ctx.Err.Printf("ConfTalkSetSocialCard %s: %s", talkID, err)
	}
}

func sponsorCardHash(sp *types.Sponsorship) string {
	h := sha256.New()
	if sp.Org != nil {
		h.Write([]byte(sp.Org.Name))
		h.Write([]byte(sp.Org.LogoDark))
		h.Write([]byte(sp.Org.LogoLight))
		h.Write([]byte(sp.Org.Twitter.Handle))
		h.Write([]byte(sp.Org.Website))
	}
	h.Write([]byte(sp.Level))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func generateAndUploadSponsorPng(ctx *config.AppContext, confTag, card string, sp *types.Sponsorship) (string, error) {
	key := fmt.Sprintf("%s/sponsors/%s-%s.png", confTag, sp.Ref, card)
	hash := sponsorCardHash(sp)

	cardHashesMu.Lock()
	if cardHashes[key] == hash {
		cardHashesMu.Unlock()
		return spaces.PublicURL(key), nil
	}
	cardHashesMu.Unlock()

	// If already in Spaces, just record the hash without re-uploading
	if spaces.Exists(key) {
		cardHashesMu.Lock()
		cardHashes[key] = hash
		cardHashesMu.Unlock()
		return spaces.PublicURL(key), nil
	}

	ctx.Infos.Printf("generating sponsor media %s (%s)", key, hash)
	png, err := helpers.MakeSponsorPng(ctx, confTag, card, sp.Ref)
	if err != nil {
		return "", fmt.Errorf("failed to generate sponsor png %s/%s: %w", sp.Ref, card, err)
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

func RefreshSponsorCards(ctx *config.AppContext) {
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("media refresh sponsors: failed to fetch confs: %s", err)
		return
	}

	for _, conf := range confs {
		if !conf.Active || !conf.InFuture() {
			continue
		}

		sponsorships, err := getters.ListSponsorships(ctx, conf.Ref)
		if err != nil {
			ctx.Err.Printf("media refresh sponsors: failed to fetch sponsorships for %s: %s", conf.Tag, err)
			continue
		}

		for _, sp := range sponsorships {
			if sp.Org == nil {
				continue
			}
			for _, card := range []string{"1080p", "insta", "social"} {
				_, err := generateAndUploadSponsorPng(ctx, conf.Tag, card, sp)
				if err != nil {
					ctx.Err.Printf("media refresh sponsors: %s", err)
				}
			}
		}

		ctx.Infos.Printf("media refresh sponsors: finished %s (%d sponsorships)", conf.Tag, len(sponsorships))
	}

	// Persist hash index to Spaces
	cardHashesMu.Lock()
	hashCopy := make(map[string]string, len(cardHashes))
	for k, v := range cardHashes {
		hashCopy[k] = v
	}
	cardHashesMu.Unlock()
	if err := spaces.SaveHashes(hashCopy); err != nil {
		ctx.Err.Printf("media refresh: failed to save hash index: %s", err)
	}
}

// RefreshTalkCards is the periodic / on-demand refresher used by the live
// server. It enforces the atomic-running guard (so OnTalksRefresh callbacks
// don't pile up) and skips talks attached to inactive confs (which are past
// events and don't need fresh cards).
func RefreshTalkCards(ctx *config.AppContext, talks []*types.Talk) {
	if !atomic.CompareAndSwapInt32(&refreshRunning, 0, 1) {
		ctx.Infos.Printf("media refresh: skipping, already running")
		return
	}
	defer atomic.StoreInt32(&refreshRunning, 0)
	refreshTalkCards(ctx, talks, true)
}

// RefreshTalkCardsForce is the CLI-friendly variant. No atomic guard (one-
// shot, single-process), and it does NOT skip talks on inactive confs —
// useful for back-filling cards on past events (e.g., when migrating to a
// new ConfTalk-keyed file layout).
func RefreshTalkCardsForce(ctx *config.AppContext, talks []*types.Talk) {
	refreshTalkCards(ctx, talks, false)
}

func refreshTalkCards(ctx *config.AppContext, talks []*types.Talk, requireActive bool) {
	confs, _ := getters.FetchConfsCached(ctx)
	confset := helpers.ConfTagSet(confs)

	card := "1080p"
	for _, talk := range talks {
		conf, ok := confset[talk.Event]
		if !ok {
			continue
		}
		if requireActive && !conf.Active {
			continue
		}
		if talk.Clipart == "" {
			continue
		}

		if _, err := generateAndUploadTalkPng(ctx, talk.Event, card, talk); err != nil {
			ctx.Err.Printf("media refresh talks: %s", err)
		}

		for _, speaker := range talk.Speakers {
			if speaker.Photo == "" {
				continue
			}
			for _, cardtype := range []string{card, "insta", "social"} {
				if _, err := generateAndUploadSpeakerPng(ctx, talk.Event, cardtype, speaker, talk); err != nil {
					ctx.Err.Printf("media refresh speakers: %s", err)
				}
			}
		}
	}

	ctx.Infos.Printf("media refresh talks: finished (%d talks, requireActive=%v)", len(talks), requireActive)

	cardHashesMu.Lock()
	hashCopy := make(map[string]string, len(cardHashes))
	for k, v := range cardHashes {
		hashCopy[k] = v
	}
	cardHashesMu.Unlock()
	if err := spaces.SaveHashes(hashCopy); err != nil {
		ctx.Err.Printf("media refresh: failed to save hash index: %s", err)
	}
}

func RefreshSpeakerCards(ctx *config.AppContext, speakers []*types.Speaker) {
        ctx.Infos.Printf("skipping speaker cards")
}

// PreloadCardHashes pulls the persisted card-hash index from Spaces into the
// in-memory dedup cache. CLI tools can call this before RefreshTalkCards to
// get the same dedup behavior the prod server uses, without InitMediaRefresh's
// callback wiring or full-cache refresh.
func PreloadCardHashes(ctx *config.AppContext) {
	hashes, err := spaces.LoadHashes()
	if err != nil {
		ctx.Err.Printf("PreloadCardHashes: failed to load hashes: %s", err)
		return
	}
	cardHashesMu.Lock()
	for k, v := range hashes {
		cardHashes[k] = v
	}
	cardHashesMu.Unlock()
	ctx.Infos.Printf("PreloadCardHashes: loaded %d hashes", len(hashes))
}

func InitMediaRefresh(ctx *config.AppContext) {
	ctx.Infos.Println("InitMediaRefresh: starting...")

	// Load existing hashes from S3 to avoid regenerating unchanged cards
	ctx.Infos.Println("InitMediaRefresh: loading hashes from spaces...")
	PreloadCardHashes(ctx)

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

	// Initial sponsor card refresh
	ctx.Infos.Println("Running initial sponsor card refresh...")
	RefreshSponsorCards(ctx)
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

// SponsorCardURL returns the S3 URL for a sponsor card, falling back to dynamic PNG route
func SponsorCardURL(ctx *config.AppContext, confTag, card, sponsorRef string) string {
	if spaces.IsConfigured() {
		key := fmt.Sprintf("%s/sponsors/%s-%s.png", confTag, sponsorRef, card)
		return spaces.PublicURL(key)
	}
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
	return spaces.PublicURL("speakers/" + photo)
}
