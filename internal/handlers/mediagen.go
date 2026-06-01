package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

var (
	cardHashes     = make(map[string]string)
	cardHashesMu   sync.Mutex
	refreshRunning int32

	// In-memory caches of Spaces manifests, refreshed lazily on a
	// TTL. The manifests map bare asset filename → sha256(content),
	// which lets card hashing detect media changes without requiring
	// the original files to exist under static/img locally.
	talkManifestMu           sync.RWMutex
	talkManifest             map[string]string
	talkManifestFetchedAt    time.Time
	speakerManifestMu        sync.RWMutex
	speakerManifest          map[string]string
	speakerManifestFetchedAt time.Time
)

const talkManifestTTL = 5 * time.Minute

// InvalidateTalkManifest forces the next talkClipartFingerprint call
// to re-fetch from Spaces. Used by the clipart-upload admin handler
// after a successful PUT so subsequent card-hash computations see
// the new entry without waiting on the 5-min TTL.
func InvalidateTalkManifest() {
	talkManifestMu.Lock()
	talkManifest = nil
	talkManifestFetchedAt = time.Time{}
	talkManifestMu.Unlock()
}

// talkClipartFingerprint returns the manifest-recorded hash for a
// given clipart filename. Empty when the manifest hasn't been
// uploaded, the filename isn't listed, or the fetch errors. Cached
// in-process with a 5-minute TTL so card-hash computation doesn't
// fan out one Spaces GET per talk.
//
// Returning the recorded hash (rather than reading the file bytes
// directly) means a clipart change still busts every card that
// references it: re-running upload-talk-cliparts rewrites the
// manifest entry, the next talkClipartFingerprint call pulls the
// new value, and the next talkCardHash / speakerCardHash will
// differ — triggering card regen.
func talkClipartFingerprint(filename string) string {
	if filename == "" {
		return ""
	}
	talkManifestMu.RLock()
	stale := talkManifest == nil || time.Since(talkManifestFetchedAt) > talkManifestTTL
	cur := talkManifest[filename]
	talkManifestMu.RUnlock()
	if !stale {
		return cur
	}
	fresh, err := spaces.LoadJSONMap(spaces.TalkManifestKey)
	if err != nil {
		// Keep returning whatever we last had — better stale than
		// blank, since blank would invalidate every card on every
		// startup until the manifest comes back.
		return cur
	}
	talkManifestMu.Lock()
	talkManifest = fresh
	talkManifestFetchedAt = time.Now()
	talkManifestMu.Unlock()
	return fresh[filename]
}

func InvalidateSpeakerManifest() {
	speakerManifestMu.Lock()
	speakerManifest = nil
	speakerManifestFetchedAt = time.Time{}
	speakerManifestMu.Unlock()
}

func speakerPhotoFingerprint(filename string) string {
	if filename == "" {
		return ""
	}
	speakerManifestMu.RLock()
	stale := speakerManifest == nil || time.Since(speakerManifestFetchedAt) > talkManifestTTL
	cur := speakerManifest[filename]
	speakerManifestMu.RUnlock()
	if !stale {
		return cur
	}
	fresh, err := spaces.LoadJSONMap(spaces.SpeakerManifestKey)
	if err != nil {
		return cur
	}
	speakerManifestMu.Lock()
	speakerManifest = fresh
	speakerManifestFetchedAt = time.Now()
	speakerManifestMu.Unlock()
	return fresh[filename]
}

func speakerCardHash(speaker *types.Speaker, talk *types.Talk) string {
	h := sha256.New()
	h.Write([]byte(speaker.Name))
	h.Write([]byte(speaker.Photo))
	h.Write([]byte(speaker.Twitter.Handle))
	h.Write([]byte(speaker.Company))
	// Talk title isn't rendered on the speaker card anymore (only
	// Name / Company / Twitter handle), but the talk's clipart
	// is still the card's background so it stays in the hash.
	h.Write([]byte(talk.Clipart))
	h.Write([]byte(speakerPhotoFingerprint(speaker.Photo)))
	h.Write([]byte(talkClipartFingerprint(talk.Clipart)))
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func talkCardHash(talk *types.Talk) string {
	h := sha256.New()
	h.Write([]byte(talk.Name))
	h.Write([]byte(talk.Clipart))
	// Clipart fingerprint comes from the Spaces manifest now that
	// static/img/talks/ is gone — see talkClipartFingerprint.
	h.Write([]byte(talkClipartFingerprint(talk.Clipart)))
	// Include speaker data so card updates when speakers change
	sort.Slice(talk.Speakers, func(i, j int) bool {
		return talk.Speakers[i].ID < talk.Speakers[j].ID
	})
	for _, s := range talk.Speakers {
		h.Write([]byte(s.Name))
		h.Write([]byte(s.Photo))
		h.Write([]byte(speakerPhotoFingerprint(s.Photo)))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func updateSpeakerManifest(filename string, raw []byte) {
	filename = strings.TrimSpace(filename)
	if filename == "" || len(raw) == 0 || !spaces.IsConfigured() {
		return
	}
	manifest, err := spaces.LoadJSONMap(spaces.SpeakerManifestKey)
	if err != nil {
		return
	}
	sum := sha256.Sum256(raw)
	manifest[filename] = hex.EncodeToString(sum[:])
	if err := spaces.SaveJSONMap(spaces.SpeakerManifestKey, manifest); err != nil {
		return
	}
	InvalidateSpeakerManifest()
}

func generateAndUploadSpeakerPng(ctx *config.AppContext, confTag, card string, speaker *types.Speaker, talk *types.Talk) (string, error) {
	return generateAndUploadSpeakerPngOpt(ctx, confTag, card, speaker, talk, false)
}

// generateAndUploadSpeakerPngOpt is the force-aware variant. force=true
// bypasses both the in-memory hash dedup and the Spaces.Exists check
// so a re-render always happens — used by the CLI's -force flag for a
// full re-render sweep (e.g. when a template style change should
// propagate to every existing card without bumping per-talk inputs).
func generateAndUploadSpeakerPngOpt(ctx *config.AppContext, confTag, card string, speaker *types.Speaker, talk *types.Talk, force bool) (string, error) {
	return generateAndUploadSpeakerPngWithRenderer(ctx, nil, confTag, card, speaker, talk, force)
}

func generateAndUploadSpeakerPngWithRenderer(ctx *config.AppContext, renderer *helpers.MediaRenderer, confTag, card string, speaker *types.Speaker, talk *types.Talk, force bool) (string, error) {
	key := fmt.Sprintf("%s/speakers/%s-%s-%s.png", confTag, talk.ID, speaker.ID, card)
	hash := speakerCardHash(speaker, talk)

	if !force {
		cardHashesMu.Lock()
		if cardHashes[key] == hash {
			cardHashesMu.Unlock()
			return spaces.PublicURL(key), nil
		}
		cardHashesMu.Unlock()
	}

	ctx.Infos.Printf("generating speaker media %s (%s)", key, hash)
	var png []byte
	var err error
	if renderer != nil {
		png, err = renderer.MakeSpeakerPng(confTag, card, speaker.ID, talk.ID)
	} else {
		png, err = helpers.MakeSpeakerPng(ctx, confTag, card, speaker.ID, talk.ID)
	}
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
	return generateAndUploadTalkPngOpt(ctx, confTag, card, talk, false)
}

// generateAndUploadTalkPngOpt is the force-aware variant. See the
// speaker-side companion for the rationale.
func generateAndUploadTalkPngOpt(ctx *config.AppContext, confTag, card string, talk *types.Talk, force bool) (string, error) {
	return generateAndUploadTalkPngWithRenderer(ctx, nil, confTag, card, talk, force)
}

func generateAndUploadTalkPngWithRenderer(ctx *config.AppContext, renderer *helpers.MediaRenderer, confTag, card string, talk *types.Talk, force bool) (string, error) {
	key := fmt.Sprintf("%s/talks/%s-%s.png", confTag, talk.ID, card)
	hash := talkCardHash(talk)

	if !force {
		cardHashesMu.Lock()
		if cardHashes[key] == hash {
			cardHashesMu.Unlock()
			writeSocialCardPath(ctx, talk.ID, key, card)
			return spaces.PublicURL(key), nil
		}
		cardHashesMu.Unlock()
	}

	ctx.Infos.Printf("generating talks media %s (%s)", key, hash)
	var png []byte
	var err error
	if renderer != nil {
		png, err = renderer.MakeTalkPng(confTag, card, talk.ID)
	} else {
		png, err = helpers.MakeTalkPng(ctx, confTag, card, talk.ID)
	}
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
	if err := getters.ConfTalkSetSocialCard(ctx, talkID, "/"+key); err != nil {
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
	return generateAndUploadSponsorPngOpt(ctx, confTag, card, sp, false)
}

// generateAndUploadSponsorPngOpt is the force-aware variant. force=true
// bypasses the in-memory hash dedup AND the Spaces.Exists short-circuit
// so a re-render always happens — used by the CLI's -force flag when an
// admin needs to push a regen even though the cached hash matches (e.g.
// the org's logo file content changed but the filename didn't).
func generateAndUploadSponsorPngOpt(ctx *config.AppContext, confTag, card string, sp *types.Sponsorship, force bool) (string, error) {
	return generateAndUploadSponsorPngWithRenderer(ctx, nil, confTag, card, sp, force)
}

func generateAndUploadSponsorPngWithRenderer(ctx *config.AppContext, renderer *helpers.MediaRenderer, confTag, card string, sp *types.Sponsorship, force bool) (string, error) {
	key := fmt.Sprintf("%s/sponsors/%s-%s.png", confTag, sp.Ref, card)
	hash := sponsorCardHash(sp)

	if !force {
		cardHashesMu.Lock()
		if cardHashes[key] == hash {
			cardHashesMu.Unlock()
			return spaces.PublicURL(key), nil
		}
		cardHashesMu.Unlock()
	}

	ctx.Infos.Printf("generating sponsor media %s (%s)", key, hash)
	var png []byte
	var err error
	if renderer != nil {
		png, err = renderer.MakeSponsorPng(confTag, card, sp.Ref)
	} else {
		png, err = helpers.MakeSponsorPng(ctx, confTag, card, sp.Ref)
	}
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

// RefreshSponsorCardsForConf regenerates sponsor cards for a single
// conf, regardless of Active / InFuture status. Used by the CLI's
// -conf filter so an admin can back-fill sponsor cards on a specific
// event (including past ones) without sweeping every conf.
func RefreshSponsorCardsForConf(ctx *config.AppContext, conf *types.Conf) {
	RefreshSponsorCardsForConfOpt(ctx, conf, "", false)
}

// RefreshSponsorCardsForConfOpt is the filter-aware variant.
// orgFilter (case-insensitive substring on Org.Name; empty = all)
// narrows which sponsorships get refreshed. force=true bypasses the
// hash + Spaces.Exists short-circuits so the upload always rewrites
// the existing Spaces object — useful when the source logo file's
// content changed but its filename (the only logo bit hashed) didn't.
func RefreshSponsorCardsForConfOpt(ctx *config.AppContext, conf *types.Conf, orgFilter string, force bool) {
	if conf == nil {
		return
	}
	renderer := helpers.NewMediaRenderer(ctx)
	defer renderer.Close()
	sponsorships, err := getters.ListSponsorships(ctx, conf.Ref)
	if err != nil {
		ctx.Err.Printf("media refresh sponsors: failed to fetch sponsorships for %s: %s", conf.Tag, err)
		return
	}
	needle := strings.ToLower(strings.TrimSpace(orgFilter))
	matched := 0
	for _, sp := range sponsorships {
		if sp.Org == nil {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(sp.Org.Name), needle) {
			continue
		}
		matched++
		for _, card := range []string{"1080p", "insta", "social"} {
			if _, err := generateAndUploadSponsorPngWithRenderer(ctx, renderer, conf.Tag, card, sp, force); err != nil {
				ctx.Err.Printf("media refresh sponsors: %s", err)
			}
		}
	}
	if needle != "" {
		ctx.Infos.Printf("media refresh sponsors: finished %s (%d/%d matched %q, force=%t)", conf.Tag, matched, len(sponsorships), orgFilter, force)
	} else {
		ctx.Infos.Printf("media refresh sponsors: finished %s (%d sponsorships, force=%t)", conf.Tag, len(sponsorships), force)
	}
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
		RefreshSponsorCardsForConf(ctx, conf)
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
	refreshTalkCards(ctx, talks, true, false)
}

// RefreshTalkCardsForce is the CLI-friendly variant. No atomic guard (one-
// shot, single-process), and it does NOT skip talks on inactive confs —
// useful for back-filling cards on past events (e.g., when migrating to a
// new ConfTalk-keyed file layout).
func RefreshTalkCardsForce(ctx *config.AppContext, talks []*types.Talk) {
	refreshTalkCards(ctx, talks, false, false)
}

// RefreshTalkCardsForceOpt is the force-aware variant. force=true
// bypasses the in-memory hash dedup AND the Spaces.Exists short-
// circuit so every card actually re-renders. Used by the CLI's
// -force flag for full sweeps after a template change.
func RefreshTalkCardsForceOpt(ctx *config.AppContext, talks []*types.Talk, force bool) {
	refreshTalkCards(ctx, talks, false, force)
}

func refreshTalkCards(ctx *config.AppContext, talks []*types.Talk, requireActive, force bool) {
	confs, _ := getters.FetchConfsCached(ctx)
	confset := helpers.ConfTagSet(confs)
	renderer := helpers.NewMediaRenderer(ctx)
	defer renderer.Close()

	card := "1080p"
	for _, talk := range talks {
		conf, ok := confset[talk.Event]
		if !ok {
			continue
		}
		if requireActive && !conf.Active {
			continue
		}

		// Talk-card visual is built around talk.Clipart, so skip the
		// talk-card upload when no clipart is set yet. Speaker cards
		// use Clipart only as a background image; the speaker info
		// still renders without it, so let those generate regardless.
		if talk.Clipart != "" {
			if _, err := generateAndUploadTalkPngWithRenderer(ctx, renderer, talk.Event, card, talk, force); err != nil {
				ctx.Err.Printf("media refresh talks: %s", err)
			}
		}

		for _, speaker := range talk.Speakers {
			if speaker.Photo == "" {
				continue
			}
			for _, cardtype := range []string{card, "insta", "social"} {
				if _, err := generateAndUploadSpeakerPngWithRenderer(ctx, renderer, talk.Event, cardtype, speaker, talk, force); err != nil {
					ctx.Err.Printf("media refresh speakers: %s", err)
				}
			}
		}
	}

	ctx.Infos.Printf("media refresh talks: finished (%d talks, requireActive=%v, force=%v)", len(talks), requireActive, force)

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
