package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"text/template"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/external/tokens"
	youtubepkg "btcpp-web/external/youtube"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	youtubeapi "google.golang.org/api/youtube/v3"
)

const tokenKey = "youtube"

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

type cfgFile struct {
	Notion struct {
		Token          string `toml:"token"`
		PurchasesDb    string `toml:"purchasesdb"`
		SpeakersDb     string `toml:"speakersdb"`
		ConfsDb        string `toml:"confsdb"`
		ConfsTixDb     string `toml:"confstixdb"`
		DiscountsDb    string `toml:"discountsdb"`
		HotelsDb       string `toml:"hotelsdb"`
		VolunteerDb    string `toml:"volunteerdb"`
		JobTypeDb      string `toml:"jobtypedb"`
		ProposalDb     string `toml:"proposaldb"`
		SpeakerConfDb  string `toml:"speakerconfdb"`
		ConfTalkDb     string `toml:"conftalkdb"`
		RecordingsDb   string `toml:"recordingsdb"`
		ShiftDb        string `toml:"shiftdb"`
		OrgDb          string `toml:"orgdb"`
		SocialPostsDb  string `toml:"socialpostsdb"`
		SponsorshipsDb string `toml:"sponsorshipsdb"`
	} `toml:"notion"`
	Spaces struct {
		Endpoint string `toml:"endpoint"`
		Region   string `toml:"region"`
		Bucket   string `toml:"bucket"`
		Key      string `toml:"key"`
		Secret   string `toml:"secret"`
	} `toml:"spaces"`
	YouTube struct {
		ClientID     string `toml:"clientID"`
		ClientSecret string `toml:"clientSecret"`
		RedirectURL  string `toml:"redirectURL"`
	} `toml:"youtube"`
	Recordings struct {
		EncryptionKey      string `toml:"encryptionKey"`
		YouTubeTokenObject string `toml:"youtubeTokenObject"`
	} `toml:"recordings"`
}

type video struct {
	ID            string
	Title         string
	PublishedAt   string
	PrivacyStatus string
	UploadStatus  string
}

func main() {
	configFile := flag.String("config", "config.toml", "Path to TOML config file")
	tokenDB := flag.String("tokens", "tokens.bolt", "Path to local OAuth token bolt DB")
	confTag := flag.String("conf", "vienna", "Conference tag to match in FileURI")
	listFilter := flag.String("list-videos", "", "Print fetched YouTube videos whose normalized title contains this text, then exit")
	updateDraftMetadata := flag.Bool("update-draft-metadata", false, "Update old-style draft YouTube titles/descriptions from recording dashboard copy")
	updateDraftThumbnails := flag.Bool("update-draft-thumbnails", false, "Upload 1080p talk cards as thumbnails for main-stage YouTube videos")
	addDraftsToPlaylist := flag.String("add-drafts-to-playlist", "", "Add conf recording drafts to the existing YouTube playlist whose title contains this text")
	fixRecordingFile := flag.String("fix-recording-file", "", "Retarget the Recording row whose FileURI basename matches this file")
	fixTalkTitle := flag.String("fix-talk-title", "", "Proposal title to use with --fix-recording-file")
	apply := flag.Bool("apply", false, "Write matched YTLink values to Notion")
	flag.Parse()

	cfg := loadCfg(*configFile)
	ctx := appContext(cfg)
	svc := youtubeService(cfg, *tokenDB)
	videos := listYouTubeVideos(svc)
	if strings.TrimSpace(*listFilter) != "" {
		filter := normalizeTitle(*listFilter)
		for _, v := range videos {
			if strings.Contains(normalizeTitle(v.Title), filter) {
				fmt.Printf("https://youtu.be/%s | %s | %s\n", v.ID, v.PublishedAt, v.Title)
			}
		}
		return
	}
	if *updateDraftMetadata {
		updateDraftVideoMetadata(ctx, svc, *confTag, *apply)
		return
	}
	if *updateDraftThumbnails {
		updateDraftVideoThumbnails(ctx, svc, *confTag, *apply)
		return
	}
	if strings.TrimSpace(*addDraftsToPlaylist) != "" {
		addDraftsToPlaylistByTitle(ctx, svc, *confTag, *addDraftsToPlaylist, *apply)
		return
	}
	if strings.TrimSpace(*fixRecordingFile) != "" {
		fixRecordingForFile(ctx, svc, *confTag, *fixRecordingFile, *fixTalkTitle, *apply)
		return
	}
	recs, err := getters.ListRecordings(ctx)
	if err != nil {
		log.Fatalf("list recordings: %s", err)
	}

	videoByKey := map[string][]video{}
	for _, v := range videos {
		for _, key := range titleKeys(v.Title) {
			videoByKey[key] = append(videoByKey[key], v)
		}
	}

	var matched, ambiguous, missing, already int
	for _, rec := range recs {
		if rec == nil || !recordingForConf(rec, *confTag) {
			continue
		}
		if strings.TrimSpace(rec.YTLink) != "" {
			already++
			continue
		}
		candidates := []video{}
		for _, key := range recordingTitleKeys(rec.TalkName) {
			candidates = append(candidates, videoByKey[key]...)
		}
		candidates = dedupeVideos(candidates)
		switch len(candidates) {
		case 0:
			missing++
			fmt.Printf("MISS  %s | %s\n", rec.ID, rec.TalkName)
		case 1:
			matched++
			yt := "https://youtu.be/" + candidates[0].ID
			if *apply {
				if err := getters.UpdateRecordingYTLink(ctx, rec.ID, yt); err != nil {
					log.Fatalf("update %s: %s", rec.ID, err)
				}
				fmt.Printf("SET   %s | %s | %s\n", rec.ID, rec.TalkName, yt)
			} else {
				fmt.Printf("MATCH %s | %s | %s | %s\n", rec.ID, rec.TalkName, yt, candidates[0].Title)
			}
		default:
			ambiguous++
			fmt.Printf("AMBIG %s | %s\n", rec.ID, rec.TalkName)
			for _, v := range candidates {
				fmt.Printf("      https://youtu.be/%s | %s | %s\n", v.ID, v.PublishedAt, v.Title)
			}
		}
	}
	fmt.Printf("done: matched=%d ambiguous=%d missing=%d already=%d videos=%d apply=%v\n", matched, ambiguous, missing, already, len(videos), *apply)
}

func loadCfg(configFile string) cfgFile {
	var cfg cfgFile
	if _, err := toml.DecodeFile(configFile, &cfg); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if cfg.Notion.Token == "" || cfg.Notion.RecordingsDb == "" {
		log.Fatalf("missing notion.token / notion.recordingsdb in %s", configFile)
	}
	if cfg.YouTube.ClientID == "" || cfg.YouTube.ClientSecret == "" {
		log.Fatalf("missing youtube.clientID / youtube.clientSecret in %s", configFile)
	}
	return cfg
}

func appContext(cfg cfgFile) *config.AppContext {
	n := &types.Notion{Config: &types.NotionConfig{
		Token:          cfg.Notion.Token,
		PurchasesDb:    cfg.Notion.PurchasesDb,
		SpeakersDb:     cfg.Notion.SpeakersDb,
		ConfsDb:        cfg.Notion.ConfsDb,
		ConfsTixDb:     cfg.Notion.ConfsTixDb,
		DiscountsDb:    cfg.Notion.DiscountsDb,
		HotelsDb:       cfg.Notion.HotelsDb,
		VolunteerDb:    cfg.Notion.VolunteerDb,
		JobTypeDb:      cfg.Notion.JobTypeDb,
		ProposalDb:     cfg.Notion.ProposalDb,
		SpeakerConfDb:  cfg.Notion.SpeakerConfDb,
		ConfTalkDb:     cfg.Notion.ConfTalkDb,
		RecordingsDb:   cfg.Notion.RecordingsDb,
		ShiftDb:        cfg.Notion.ShiftDb,
		OrgDb:          cfg.Notion.OrgDb,
		SocialPostsDb:  cfg.Notion.SocialPostsDb,
		SponsorshipsDb: cfg.Notion.SponsorshipsDb,
	}}
	n.Setup(cfg.Notion.Token)
	return &config.AppContext{
		Env:    &types.EnvConfig{CacheTTLSec: 60},
		Notion: n,
		Err:    log.New(os.Stderr, "", log.LstdFlags),
		Infos:  log.New(os.Stdout, "", log.LstdFlags),
	}
}

func youtubeService(cfg cfgFile, tokenDB string) *youtubeapi.Service {
	if err := tokens.Init(tokenDB); err != nil {
		log.Fatalf("open tokens: %s", err)
	}
	initRemoteTokenStore(cfg)
	stored, err := tokens.Get(tokenKey)
	if err != nil {
		log.Fatalf("load youtube token: %s", err)
	}
	if stored == nil {
		log.Fatalf("no youtube token found in %s", tokenDB)
	}
	oauthCfg := &oauth2.Config{
		ClientID:     cfg.YouTube.ClientID,
		ClientSecret: cfg.YouTube.ClientSecret,
		RedirectURL:  cfg.YouTube.RedirectURL,
		Scopes: []string{
			youtubeapi.YoutubeReadonlyScope,
			youtubeapi.YoutubeForceSslScope,
		},
		Endpoint: google.Endpoint,
	}
	tok := &oauth2.Token{
		AccessToken:  stored.AccessToken,
		RefreshToken: stored.RefreshToken,
		TokenType:    stored.TokenType,
		Expiry:       stored.Expiry,
	}
	client := oauthCfg.Client(context.Background(), tok)
	svc, err := youtubeapi.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("youtube service: %s", err)
	}

	_, err = svc.Channels.List([]string{"contentDetails"}).Mine(true).Do()
	if err != nil {
		if strings.Contains(err.Error(), "invalid_grant") {
			refreshed, refreshErr := tokens.RefreshLocalFromRemote(tokenKey)
			if refreshErr != nil {
				log.Fatalf("youtube token invalid and remote refresh failed: %s", refreshErr)
			}
			if refreshed == nil {
				log.Fatalf("youtube token invalid and no remote token is configured")
			}
			tok.AccessToken = refreshed.AccessToken
			tok.RefreshToken = refreshed.RefreshToken
			tok.TokenType = refreshed.TokenType
			tok.Expiry = refreshed.Expiry
			client = oauthCfg.Client(context.Background(), tok)
			svc, err = youtubeapi.NewService(context.Background(), option.WithHTTPClient(client))
			if err != nil {
				log.Fatalf("youtube service after remote token refresh: %s", err)
			}
			_, err = svc.Channels.List([]string{"contentDetails"}).Mine(true).Do()
		}
		if err != nil {
			log.Fatalf("youtube channels.list mine: %s", err)
		}
	}
	return svc
}

func listYouTubeVideos(svc *youtubeapi.Service) []video {
	chans, err := svc.Channels.List([]string{"contentDetails"}).Mine(true).Do()
	if err != nil {
		log.Fatalf("youtube channels.list mine: %s", err)
	}
	if chans == nil || len(chans.Items) == 0 || chans.Items[0].ContentDetails == nil || chans.Items[0].ContentDetails.RelatedPlaylists == nil {
		log.Fatalf("youtube channels.list returned no uploads playlist")
	}
	uploads := chans.Items[0].ContentDetails.RelatedPlaylists.Uploads
	var out []video
	pageToken := ""
	for {
		call := svc.PlaylistItems.List([]string{"snippet", "contentDetails"}).PlaylistId(uploads).MaxResults(50)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		page, err := call.Do()
		if err != nil {
			log.Fatalf("youtube playlistItems.list: %s", err)
		}
		for _, item := range page.Items {
			if item == nil || item.Snippet == nil || item.ContentDetails == nil || item.ContentDetails.VideoId == "" {
				continue
			}
			out = append(out, video{
				ID:          item.ContentDetails.VideoId,
				Title:       item.Snippet.Title,
				PublishedAt: item.ContentDetails.VideoPublishedAt,
			})
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].PublishedAt > out[j].PublishedAt
	})
	return out
}

func initRemoteTokenStore(cfg cfgFile) {
	if cfg.Spaces.Endpoint == "" || cfg.Spaces.Bucket == "" || cfg.Spaces.Key == "" || cfg.Spaces.Secret == "" {
		return
	}
	spaces.Init(types.SpacesConfig{
		Endpoint: cfg.Spaces.Endpoint,
		Region:   cfg.Spaces.Region,
		Bucket:   cfg.Spaces.Bucket,
		Key:      cfg.Spaces.Key,
		Secret:   cfg.Spaces.Secret,
	})
	if !spaces.IsConfigured() || cfg.Recordings.YouTubeTokenObject == "" || cfg.Recordings.EncryptionKey == "" {
		return
	}
	if err := tokens.InitRemote(cfg.Recordings.YouTubeTokenObject, cfg.Recordings.EncryptionKey); err != nil {
		log.Printf("remote token store disabled: %s", err)
	}
}

func updateDraftVideoMetadata(ctx *config.AppContext, svc *youtubeapi.Service, confTag string, apply bool) {
	getters.WaitFetch(ctx)
	recs, err := getters.ListRecordings(ctx)
	if err != nil {
		log.Fatalf("list recordings: %s", err)
	}
	var matched, changed, skipped int
	for _, rec := range recs {
		if rec == nil || !recordingForConf(rec, confTag) || strings.TrimSpace(rec.YTLink) == "" {
			continue
		}
		videoID := youtubeID(rec.YTLink)
		if videoID == "" {
			fmt.Printf("SKIP  %s | %s | invalid YTLink %q\n", rec.ID, rec.TalkName, rec.YTLink)
			skipped++
			continue
		}
		vid := fetchVideo(svc, videoID)
		if vid == nil || vid.Snippet == nil {
			fmt.Printf("SKIP  %s | %s | video not found %s\n", rec.ID, rec.TalkName, videoID)
			skipped++
			continue
		}
		if !oldDraftTitle(vid.Snippet.Title) {
			skipped++
			continue
		}
		matched++
		title, body := dashboardYouTubeCopy(ctx, rec)
		if title == "" || body == "" {
			fmt.Printf("SKIP  %s | %s | could not generate dashboard copy\n", rec.ID, rec.TalkName)
			skipped++
			continue
		}
		if vid.Snippet.Title == title && vid.Snippet.Description == body {
			fmt.Printf("OK    %s | %s | already up to date\n", rec.ID, title)
			continue
		}
		changed++
		privacy, upload := "", ""
		if vid.Status != nil {
			privacy = vid.Status.PrivacyStatus
			upload = vid.Status.UploadStatus
		}
		fmt.Printf("VIDEO %s | privacy=%s upload=%s\n", videoID, statusString(privacy), statusString(upload))
		fmt.Printf("      old: %s\n", vid.Snippet.Title)
		fmt.Printf("      new: %s\n", title)
		fmt.Printf("      description: %d -> %d chars\n", len(vid.Snippet.Description), len(body))
		if apply {
			vid.Snippet.Title = title
			vid.Snippet.Description = body
			if _, err := svc.Videos.Update([]string{"snippet", "status"}, vid).Do(); err != nil {
				log.Fatalf("youtube videos.update %s: %s", videoID, err)
			}
			fmt.Printf("SET   %s | %s\n", videoID, title)
		}
	}
	fmt.Printf("done: matched_old_titles=%d changed=%d skipped=%d apply=%v\n", matched, changed, skipped, apply)
}

func updateDraftVideoThumbnails(ctx *config.AppContext, svc *youtubeapi.Service, confTag string, apply bool) {
	getters.WaitFetch(ctx)
	recs, err := getters.ListRecordings(ctx)
	if err != nil {
		log.Fatalf("list recordings: %s", err)
	}
	var matched, changed, skipped int
	for _, rec := range recs {
		if rec == nil || !recordingForConf(rec, confTag) || !recordingIsMainStage(rec) || strings.TrimSpace(rec.YTLink) == "" {
			continue
		}
		videoID := youtubeID(rec.YTLink)
		if videoID == "" {
			fmt.Printf("SKIP  %s | %s | invalid YTLink %q\n", rec.ID, rec.TalkName, rec.YTLink)
			skipped++
			continue
		}
		key := recordingCardKey(rec)
		if key == "" {
			fmt.Printf("SKIP  %s | %s | no talk card key\n", rec.ID, rec.TalkName)
			skipped++
			continue
		}
		data, err := spaces.Get(key)
		if err != nil {
			fmt.Printf("SKIP  %s | %s | load %s: %s\n", rec.ID, rec.TalkName, key, err)
			skipped++
			continue
		}
		matched++
		changed++
		prepared, contentType, err := youtubepkg.PrepareThumbnail(key, data)
		if err != nil {
			fmt.Printf("SKIP  %s | %s | prepare %s: %s\n", rec.ID, rec.TalkName, key, err)
			skipped++
			continue
		}
		fmt.Printf("THUMB %s | %s | %s (%d -> %d bytes, %s)\n", videoID, rec.TalkName, key, len(data), len(prepared), contentType)
		if apply {
			_, err := svc.Thumbnails.Set(videoID).Media(bytes.NewReader(prepared), googleapi.ContentType(contentType)).Do()
			if err != nil {
				log.Fatalf("youtube thumbnails.set %s: %s", videoID, err)
			}
			fmt.Printf("SET   %s | %s\n", videoID, key)
		}
	}
	fmt.Printf("done: mainstage=%d thumbnails=%d skipped=%d apply=%v\n", matched, changed, skipped, apply)
}

func addDraftsToPlaylistByTitle(ctx *config.AppContext, svc *youtubeapi.Service, confTag, playlistTitleContains string, apply bool) {
	getters.WaitFetch(ctx)
	playlist := findPlaylistByTitle(svc, playlistTitleContains)
	if playlist == nil || playlist.Id == "" || playlist.Snippet == nil {
		log.Fatalf("no playlist found whose title contains %q", playlistTitleContains)
	}
	existing := playlistVideoIDs(svc, playlist.Id)
	recs, err := getters.ListRecordings(ctx)
	if err != nil {
		log.Fatalf("list recordings: %s", err)
	}
	var drafts, added, already, skipped int
	for _, rec := range recs {
		if rec == nil || !recordingForConf(rec, confTag) || strings.TrimSpace(rec.YTLink) == "" {
			continue
		}
		videoID := youtubeID(rec.YTLink)
		if videoID == "" {
			fmt.Printf("SKIP  %s | %s | invalid YTLink %q\n", rec.ID, rec.TalkName, rec.YTLink)
			skipped++
			continue
		}
		vid := fetchVideo(svc, videoID)
		if vid == nil || vid.Snippet == nil {
			fmt.Printf("SKIP  %s | %s | video not found %s\n", rec.ID, rec.TalkName, videoID)
			skipped++
			continue
		}
		if videoStatus(vid) != "private" {
			skipped++
			continue
		}
		drafts++
		if existing[videoID] {
			fmt.Printf("HAVE  %s | %s | %s\n", videoID, rec.TalkName, vid.Snippet.Title)
			already++
			continue
		}
		fmt.Printf("ADD   %s | %s | %s -> %s\n", videoID, rec.TalkName, vid.Snippet.Title, playlist.Snippet.Title)
		if apply {
			item := &youtubeapi.PlaylistItem{
				Snippet: &youtubeapi.PlaylistItemSnippet{
					PlaylistId: playlist.Id,
					ResourceId: &youtubeapi.ResourceId{
						Kind:    "youtube#video",
						VideoId: videoID,
					},
				},
			}
			if _, err := svc.PlaylistItems.Insert([]string{"snippet"}, item).Do(); err != nil {
				log.Fatalf("youtube playlistItems.insert %s -> %s: %s", videoID, playlist.Id, err)
			}
			existing[videoID] = true
			added++
		}
	}
	fmt.Printf("done: playlist=%q drafts=%d added=%d already=%d skipped=%d apply=%v\n", playlist.Snippet.Title, drafts, added, already, skipped, apply)
}

func fixRecordingForFile(ctx *config.AppContext, svc *youtubeapi.Service, confTag, filename, talkTitle string, apply bool) {
	if strings.TrimSpace(talkTitle) == "" {
		log.Fatalf("--fix-talk-title is required with --fix-recording-file")
	}
	getters.WaitFetch(ctx)
	recs, err := getters.ListRecordings(ctx)
	if err != nil {
		log.Fatalf("list recordings: %s", err)
	}
	rec := findRecordingByFilename(recs, confTag, filename)
	if rec == nil {
		log.Fatalf("no recording found for file %q in conf %s", filename, confTag)
	}
	ct := findConfTalkByProposalTitle(ctx, talkTitle, confTag)
	if ct == nil || ct.Proposal == nil {
		log.Fatalf("no conftalk found for proposal title %q in conf %s", talkTitle, confTag)
	}
	if existing := findRecordingByConfTalk(recs, ct.ID, rec.ID); existing != nil {
		fmt.Printf("WARN  target talk already has another Recording row: %s | %s | %s\n", existing.ID, existing.TalkName, existing.YTLink)
	}
	if strings.TrimSpace(rec.YTLink) == "" {
		log.Fatalf("recording %s has no YTLink", rec.ID)
	}
	videoID := youtubeID(rec.YTLink)
	if videoID == "" {
		log.Fatalf("could not parse YTLink %q", rec.YTLink)
	}
	corrected := *rec
	corrected.ConfTalkID = ct.ID
	corrected.TalkName = ct.Proposal.Title
	targetKey := correctedRecordingKey(rec.FileURI, ct)
	if targetKey != "" {
		corrected.FileURI = targetKey
	}
	title, body := dashboardYouTubeCopy(ctx, &corrected)
	if title == "" || body == "" {
		log.Fatalf("could not generate dashboard YouTube copy for %s", ct.ID)
	}
	key := recordingCardKey(&corrected)
	var thumb []byte
	var thumbType string
	if key != "" {
		data, err := spaces.Get(key)
		if err != nil {
			log.Fatalf("load thumbnail %s: %s", key, err)
		}
		thumb, thumbType, err = youtubepkg.PrepareThumbnail(key, data)
		if err != nil {
			log.Fatalf("prepare thumbnail %s: %s", key, err)
		}
	}
	vid := fetchVideo(svc, videoID)
	if vid == nil || vid.Snippet == nil {
		log.Fatalf("video not found %s", videoID)
	}
	fmt.Printf("RECORDING %s\n", rec.ID)
	fmt.Printf("      file: %s\n", rec.FileURI)
	if targetKey != "" && targetKey != rec.FileURI {
		fmt.Printf("  new file: %s\n", targetKey)
	}
	fmt.Printf("       old: %s | talk=%s\n", rec.TalkName, rec.ConfTalkID)
	fmt.Printf("       new: %s | talk=%s\n", corrected.TalkName, corrected.ConfTalkID)
	fmt.Printf("VIDEO %s | privacy=%s upload=%s\n", videoID, statusString(videoStatus(vid)), statusString(videoUploadStatus(vid)))
	fmt.Printf("       old: %s\n", vid.Snippet.Title)
	fmt.Printf("       new: %s\n", title)
	fmt.Printf("description: %d -> %d chars\n", len(vid.Snippet.Description), len(body))
	if key != "" {
		fmt.Printf("THUMB %s | %s (%d bytes, %s)\n", videoID, key, len(thumb), thumbType)
	}
	if !apply {
		fmt.Printf("done: apply=false\n")
		return
	}
	if targetKey != "" && targetKey != rec.FileURI {
		if err := spaces.MovePublic(rec.FileURI, targetKey); err != nil {
			log.Fatalf("move recording object %s -> %s: %s", rec.FileURI, targetKey, err)
		}
	}
	if err := updateRecordingTalk(ctx, rec.ID, corrected.ConfTalkID, corrected.TalkName, corrected.FileURI); err != nil {
		log.Fatalf("update recording %s: %s", rec.ID, err)
	}
	vid.Snippet.Title = title
	vid.Snippet.Description = body
	if _, err := svc.Videos.Update([]string{"snippet", "status"}, vid).Do(); err != nil {
		log.Fatalf("youtube videos.update %s: %s", videoID, err)
	}
	if key != "" {
		if _, err := svc.Thumbnails.Set(videoID).Media(bytes.NewReader(thumb), googleapi.ContentType(thumbType)).Do(); err != nil {
			log.Fatalf("youtube thumbnails.set %s: %s", videoID, err)
		}
	}
	fmt.Printf("SET   recording=%s video=%s title=%q\n", rec.ID, videoID, title)
}

func recordingIsMainStage(rec *types.Recording) bool {
	fileURI := strings.ToLower(rec.FileURI)
	return strings.Contains(fileURI, "/01main") || strings.Contains(fileURI, "/02main")
}

func recordingCardKey(rec *types.Recording) string {
	if rec == nil || rec.ConfTalkID == "" {
		return ""
	}
	ct := getters.FetchConfTalkByID(rec.ConfTalkID)
	if ct == nil {
		return ""
	}
	if strings.TrimSpace(ct.SocialCard) != "" {
		return strings.TrimPrefix(strings.TrimSpace(ct.SocialCard), "/")
	}
	if ct.Conf == nil || ct.Conf.Tag == "" {
		return ""
	}
	return fmt.Sprintf("%s/talks/%s-1080p.png", ct.Conf.Tag, ct.ID)
}

func findPlaylistByTitle(svc *youtubeapi.Service, contains string) *youtubeapi.Playlist {
	want := normalizeTitle(contains)
	var found *youtubeapi.Playlist
	pageToken := ""
	for {
		call := svc.Playlists.List([]string{"snippet"}).Mine(true).MaxResults(50)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		page, err := call.Do()
		if err != nil {
			log.Fatalf("youtube playlists.list mine: %s", err)
		}
		for _, pl := range page.Items {
			if pl == nil || pl.Snippet == nil {
				continue
			}
			if !strings.Contains(normalizeTitle(pl.Snippet.Title), want) {
				continue
			}
			if found != nil {
				log.Fatalf("multiple playlists matched %q: %q (%s) and %q (%s)", contains, found.Snippet.Title, found.Id, pl.Snippet.Title, pl.Id)
			}
			found = pl
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return found
}

func playlistVideoIDs(svc *youtubeapi.Service, playlistID string) map[string]bool {
	out := map[string]bool{}
	pageToken := ""
	for {
		call := svc.PlaylistItems.List([]string{"contentDetails"}).PlaylistId(playlistID).MaxResults(50)
		if pageToken != "" {
			call.PageToken(pageToken)
		}
		page, err := call.Do()
		if err != nil {
			log.Fatalf("youtube playlistItems.list %s: %s", playlistID, err)
		}
		for _, item := range page.Items {
			if item == nil || item.ContentDetails == nil || item.ContentDetails.VideoId == "" {
				continue
			}
			out[item.ContentDetails.VideoId] = true
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return out
}

func findRecordingByFilename(recs []*types.Recording, confTag, filename string) *types.Recording {
	want := normalizeFilename(filename)
	for _, rec := range recs {
		if rec == nil || !recordingForConf(rec, confTag) {
			continue
		}
		if normalizeFilename(rec.FileURI) == want {
			return rec
		}
	}
	return nil
}

func findRecordingByConfTalk(recs []*types.Recording, confTalkID, exceptRecordingID string) *types.Recording {
	for _, rec := range recs {
		if rec != nil && rec.ID != exceptRecordingID && rec.ConfTalkID == confTalkID {
			return rec
		}
	}
	return nil
}

func normalizeFilename(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(strings.TrimPrefix(s, "/"), "/")
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	return strings.ToLower(s)
}

func correctedRecordingKey(current string, ct *types.ConfTalk) string {
	if ct == nil || ct.Conf == nil || ct.Proposal == nil || ct.Sched == nil || ct.Sched.Start.IsZero() {
		return ""
	}
	ext := strings.ToLower(path.Ext(current))
	if ext == "" {
		ext = ".mov"
	}
	loc := confLoc(ct.Conf)
	start := ct.Sched.Start.In(loc)
	day := confDay(ct.Conf, ct.Sched.Start)
	return fmt.Sprintf("%s/recordings/edits/talks/%02d%s%s_%s%s",
		ct.Conf.Tag,
		day,
		stageSlug(ct.Venue),
		start.Format("1504"),
		slug(ct.Proposal.Title),
		ext,
	)
}

func confDay(conf *types.Conf, when time.Time) int {
	loc := confLoc(conf)
	start := when.In(loc)
	dayStart := time.Date(conf.StartDate.In(loc).Year(), conf.StartDate.In(loc).Month(), conf.StartDate.In(loc).Day(), 0, 0, 0, 0, loc)
	target := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	return int(target.Sub(dayStart).Hours()/24) + 1
}

func confLoc(conf *types.Conf) *time.Location {
	if conf != nil && strings.TrimSpace(conf.Timezone) != "" {
		if loc, err := time.LoadLocation(conf.Timezone); err == nil {
			return loc
		}
	}
	return time.Local
}

func stageSlug(venue string) string {
	switch strings.ToLower(strings.TrimSpace(venue)) {
	case "one", "main", "main stage":
		return "main"
	case "two", "talks", "talks stage":
		return "talks"
	case "three", "workshop", "workshops", "workshops stage":
		return "workshop"
	case "four", "lounge", "lounge stage":
		return "lounge"
	default:
		return slug(venue)
	}
}

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonAlnum.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func findConfTalkByProposalTitle(ctx *config.AppContext, title, confTag string) *types.ConfTalk {
	want := normalizeTitle(title)
	props, err := getters.FetchProposalsCached(ctx)
	if err != nil {
		log.Fatalf("fetch cached proposals: %s", err)
	}
	var foundProposal *types.Proposal
	for _, prop := range props {
		if prop == nil || prop.ScheduleFor == nil || prop.ScheduleFor.Tag != confTag {
			continue
		}
		if normalizeTitle(prop.Title) != want {
			continue
		}
		if foundProposal != nil {
			log.Fatalf("multiple proposals matched %q: %s and %s", title, foundProposal.ID, prop.ID)
		}
		foundProposal = prop
	}
	if foundProposal == nil {
		return nil
	}
	return getters.FetchConfTalkByProposal(foundProposal.ID)
}

func updateRecordingTalk(ctx *config.AppContext, recordingID, confTalkID, talkName, fileURI string) error {
	if recordingID == "" || confTalkID == "" || strings.TrimSpace(talkName) == "" {
		return fmt.Errorf("recordingID, confTalkID, and talkName are required")
	}
	props := map[string]*notion.PropertyValue{
		"talk": {
			Type: notion.PropertyRelation,
			Relation: []*notion.ObjectReference{
				{Object: notion.ObjectPage, ID: confTalkID},
			},
		},
		"TalkName": notion.NewTitlePropertyValue(
			&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: talkName}},
		),
	}
	if strings.TrimSpace(fileURI) != "" {
		props["FileURI"] = &notion.PropertyValue{
			Type: notion.PropertyRichText,
			RichText: []*notion.RichText{{
				Type: notion.RichTextText,
				Text: &notion.Text{Content: fileURI},
			}},
		}
	}
	_, err := ctx.Notion.Client.UpdatePageProperties(context.Background(), recordingID, props)
	if err != nil {
		return fmt.Errorf("notion update recording talk: %w", err)
	}
	return nil
}

func fetchVideo(svc *youtubeapi.Service, videoID string) *youtubeapi.Video {
	resp, err := svc.Videos.List([]string{"snippet", "status"}).Id(videoID).Do()
	if err != nil {
		log.Fatalf("youtube videos.list %s: %s", videoID, err)
	}
	if resp == nil || len(resp.Items) == 0 {
		return nil
	}
	return resp.Items[0]
}

func videoStatus(vid *youtubeapi.Video) string {
	if vid == nil || vid.Status == nil {
		return ""
	}
	return vid.Status.PrivacyStatus
}

func videoUploadStatus(vid *youtubeapi.Video) string {
	if vid == nil || vid.Status == nil {
		return ""
	}
	return vid.Status.UploadStatus
}

func youtubeID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case strings.Contains(host, "youtu.be"):
		return strings.Trim(strings.TrimPrefix(u.Path, "/"), "/")
	case strings.Contains(host, "youtube.com"):
		if id := u.Query().Get("v"); id != "" {
			return id
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && (parts[0] == "shorts" || parts[0] == "embed") {
			return parts[1]
		}
	}
	return ""
}

func oldDraftTitle(title string) bool {
	return strings.HasPrefix(normalizeTitle(title), "y26e3r")
}

func statusString(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

var ytCopy = template.Must(template.New("yt").Funcs(template.FuncMap{
	"joinSpeakers": joinSpeakerCredits,
}).Parse(`{{ .TalkName }}

{{- if .Speakers }}

By: {{ joinSpeakers .Speakers }}
{{- end }}
{{- if .Conf }}

Recorded {{ if .RecordedOn }}{{ .RecordedOn }} {{ end }}at {{ .Conf.Desc }}{{ if .Conf.Location }} — {{ .Conf.Location }}{{ end }}.
{{- end }}

{{- if .TalkDesc }}

{{ .TalkDesc }}
{{- end }}

{{- if .Conf }}

Find out more about this talk: https://btcpp.dev/{{ .Conf.Tag }}#talks
{{- end }}

Follow @btcplusplus for upcoming events: https://x.com/btcplusplus
Future bitcoin++ events: https://btcpp.dev
`))

type ytCopyData struct {
	TalkName   string
	TalkDesc   string
	Speakers   []*types.Speaker
	Conf       *types.Conf
	RecordedOn string
}

func dashboardYouTubeCopy(ctx *config.AppContext, rec *types.Recording) (string, string) {
	talkName := rec.TalkName
	talkDesc := ""
	var conf *types.Conf
	var recordedOn string
	var speakers []*types.Speaker
	if rec.ConfTalkID != "" {
		ct := getters.FetchConfTalkByID(rec.ConfTalkID)
		if ct != nil {
			conf = ct.Conf
			if ct.Proposal != nil {
				if ct.Proposal.Title != "" {
					talkName = ct.Proposal.Title
				}
				talkDesc = ct.Proposal.Description
				speakers = recordingSpeakersForProposal(ct.Proposal)
			}
			if ct.Sched != nil && !ct.Sched.Start.IsZero() {
				recordedOn = ct.Sched.Start.Format("January 2, 2006")
			}
		}
	}
	title := buildYTTitle(talkName, speakers, conf)
	var buf bytes.Buffer
	if err := ytCopy.Execute(&buf, ytCopyData{
		TalkName:   talkName,
		TalkDesc:   talkDesc,
		Speakers:   speakers,
		Conf:       conf,
		RecordedOn: recordedOn,
	}); err != nil {
		log.Printf("yt copy gen: %s", err)
		return title, ""
	}
	return title, strings.TrimSpace(buf.String()) + "\n"
}

func recordingSpeakersForProposal(proposal *types.Proposal) []*types.Speaker {
	if proposal == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []*types.Speaker
	appendSpeakerConf := func(sc *types.SpeakerConf) {
		if sc == nil || sc.Speaker == nil || seen[sc.Speaker.ID] {
			return
		}
		seen[sc.Speaker.ID] = true
		out = append(out, sc.Speaker)
	}
	for _, ref := range proposal.SpeakerConfRefs {
		appendSpeakerConf(getters.FetchSpeakerConfByID(ref))
	}
	for _, sc := range proposal.Speakers {
		appendSpeakerConf(sc)
	}
	return out
}

func buildYTTitle(talkName string, speakers []*types.Speaker, conf *types.Conf) string {
	var sb strings.Builder
	sb.WriteString(talkName)
	if names := joinSpeakerNames(speakers); names != "" {
		sb.WriteString(" — ")
		sb.WriteString(names)
	}
	if conf != nil && conf.Desc != "" {
		sb.WriteString(" | ")
		sb.WriteString(conf.Desc)
	}
	out := sb.String()
	if len(out) <= 100 {
		return out
	}
	return strings.TrimRight(out[:97], " ,-—|") + "..."
}

func joinSpeakerNames(speakers []*types.Speaker) string {
	var names []string
	for _, s := range speakers {
		if s == nil || s.Name == "" {
			continue
		}
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

func joinSpeakerCredits(speakers []*types.Speaker) string {
	var parts []string
	for _, s := range speakers {
		if s == nil || s.Name == "" {
			continue
		}
		if s.Twitter.Handle != "" {
			parts = append(parts, fmt.Sprintf("%s (@%s)", s.Name, s.Twitter.Handle))
		} else {
			parts = append(parts, s.Name)
		}
	}
	return strings.Join(parts, ", ")
}

func recordingForConf(rec *types.Recording, confTag string) bool {
	fileURI := strings.ToLower(strings.TrimSpace(rec.FileURI))
	return strings.HasPrefix(fileURI, strings.ToLower(confTag)+"/")
}

func titleKeys(title string) []string {
	keys := []string{normalizeTitle(title)}
	for _, sep := range []string{"|", "-", "—", "–"} {
		if idx := strings.Index(title, sep); idx > 0 {
			keys = append(keys, normalizeTitle(title[:idx]))
		}
	}
	return keys
}

func recordingTitleKeys(title string) []string {
	base := normalizeTitle(title)
	keys := []string{base}
	aliases := map[string][]string{
		"kickoff": {
			"y26e3r1d1s01 kickoff",
		},
		"economics of protocol design": {
			"y26e3r1d1s02 renee",
		},
		"does this make bitcoin better money": {
			"y26e3r1d1s03 knut",
		},
		"defending bitcoin as critical infrastructure": {
			"y26e3r1d1s04 luke",
		},
		"bitcoin backed loans with arkade and blockrise": {
			"y26e3r1d1s05 arkadepanel",
		},
		"the case for bitcoin freedom tech open society": {
			"y26e3r1d1s06 anita",
		},
		"the death of the middleman survival strategies for the 2026 bitcoin broker": {
			"y26e3r1d1s07 thomas",
		},
		"are we building the products bitcoin deserves": {
			"y26e3r1d1s08 jos",
		},
		"the economics of bitcoin": {
			"y26e3r1d1s09 paneleconomicsbitcoin",
		},
		"covenants make bitcoin better money": {
			"y26e3r1d2s01 matthew",
		},
		"the praxeology of privacy": {
			"y26e3r1d2s02 max",
		},
		"from volatile bitcoin to a stable monetary system": {
			"y26e3r1d2s03 hubertus",
		},
		"the age of corporate nihilism": {
			"y26e3r1d2s04 ben",
		},
		"austrian economics as a tool of thought": {
			"y26e3r1d2s05 rahim",
		},
		"is bitcoin the austrian school s future": {
			"y26e3r1d2s07 panel",
		},
		"closing": {
			"y26e3r1d2s06 video",
		},
		"the economics of running a lightning node": {
			"lightning node economics",
		},
		"century metadata small encrypted data long storage": {
			"century metadata",
		},
		"p2poolv2 market design for a trustless hash rate commodity": {
			"p2poolv2",
		},
		"the design space of native bitcoin hashrate derivatives": {
			"bitcoin hashrate derivatives",
		},
		"bitcoin incentives and the next billion users": {
			"next billion users",
		},
		"getting bitcoin for printed money how to play": {
			"bitcoin for printed money",
		},
		"what happened to bitcoin layer 2s": {
			"what happened to bitcoin l2s",
		},
	}
	if base == "kickoff" {
		keys = nil
	}
	for _, alias := range aliases[base] {
		keys = append(keys, normalizeTitle(alias))
	}
	return keys
}

func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "’", "'")
	s = strings.ReplaceAll(s, "‘", "'")
	s = strings.ReplaceAll(s, "“", "\"")
	s = strings.ReplaceAll(s, "”", "\"")
	s = nonAlnum.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}

func dedupeVideos(in []video) []video {
	seen := map[string]bool{}
	var out []video
	for _, v := range in {
		if seen[v.ID] {
			continue
		}
		seen[v.ID] = true
		if _, err := time.Parse(time.RFC3339, v.PublishedAt); v.PublishedAt != "" && err != nil {
			v.PublishedAt = ""
		}
		out = append(out, v)
	}
	return out
}
