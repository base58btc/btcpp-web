package handlers

import (
	"archive/zip"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

// AdminSocialCardsZip streams a zip of every 1080p social card for
// the conf — both the per-talk cards and per-speaker cards, fetched
// directly from Spaces (no public-CDN hop). Cards missing from
// Spaces are silently skipped: the talk-card pipeline only generates
// when Clipart is set, and a brand-new talk may not have run through
// refresh yet.
//
// Entries inside the zip use schedule-prefixed, slugified names so a
// Finder/Explorer browse sorts in run-of-show order:
// "talks/01main1530_why-bitcoin-matters.png",
// "speakers/01main1530_jos-lazet-why-bitcoin-matters.png".
// Collisions (two talks with the same slug, or a speaker on multiple
// talks) are suffixed "-2", "-3" so nothing overwrites.
func AdminSocialCardsZip(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfStaff(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil {
		handle404(w, r, ctx)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/admin/social-cards.zip list talks: %s", conf.Tag, err)
		http.Error(w, "Unable to load talks", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-cards.zip"`, conf.Tag))

	zw := zip.NewWriter(w)
	defer zw.Close()

	added := 0
	skipped := 0
	seen := map[string]int{}

	addEntry := func(folder, baseName, key string) {
		data, err := spaces.Get(key)
		if err != nil {
			// Almost always "object not found" — log at debug
			// level (Infos) since admins expect a partial zip
			// when some cards haven't been generated.
			ctx.Infos.Printf("/%s/admin/social-cards.zip skip %s: %s", conf.Tag, key, err)
			skipped++
			return
		}
		name := folder + "/" + baseName + ".png"
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s/%s-%d.png", folder, baseName, seen[name])
		}
		f, err := zw.Create(name)
		if err != nil {
			ctx.Err.Printf("/%s/admin/social-cards.zip create %s: %s", conf.Tag, name, err)
			return
		}
		if _, err := f.Write(data); err != nil {
			ctx.Err.Printf("/%s/admin/social-cards.zip write %s: %s", conf.Tag, name, err)
			return
		}
		added++
	}

	for _, t := range talks {
		if t == nil {
			continue
		}
		prefix := socialZipSchedulePrefix(conf, t)
		talkSlug := socialZipSlug(t.Name)
		if talkSlug == "" {
			talkSlug = t.ID
		}
		talkName := prefix + talkSlug
		// Talk's own card. Generated only when Clipart is set;
		// otherwise the Spaces.Get call returns a NotFound and
		// we move on.
		addEntry("talks", talkName, fmt.Sprintf("%s/talks/%s-1080p.png", conf.Tag, t.ID))
		// Per-speaker cards. One PNG per (talk, speaker) pair.
		for _, sp := range t.Speakers {
			if sp == nil {
				continue
			}
			spSlug := socialZipSlug(sp.Name)
			if spSlug == "" {
				spSlug = sp.ID
			}
			entryName := fmt.Sprintf("%s%s-%s", prefix, spSlug, talkSlug)
			addEntry("speakers", entryName, fmt.Sprintf("%s/speakers/%s-%s-1080p.png", conf.Tag, t.ID, sp.ID))
		}
	}

	ctx.Infos.Printf("/%s/admin/social-cards.zip: %d entries, %d skipped", conf.Tag, added, skipped)
}

var socialZipSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// socialZipSlug lowercases, replaces every non-alphanumeric run
// with "-", and trims edges. "Why Bitcoin++ Matters!" → "why-bitcoin-matters".
func socialZipSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = socialZipSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func socialZipSchedulePrefix(conf *types.Conf, talk *types.Talk) string {
	if conf == nil || talk == nil || talk.Sched == nil {
		return ""
	}
	loc := socialZipConfLoc(conf)
	start := talk.Sched.Start.In(loc)
	day := dayIndex(dayStart(conf.StartDate, loc), start, loc)
	if day < 1 {
		return ""
	}
	return fmt.Sprintf("%02d%s%s_", day, socialZipStageSlug(talk.Venue), start.Format("1504"))
}

func socialZipConfLoc(conf *types.Conf) *time.Location {
	if conf == nil {
		return time.Local
	}
	if conf.Timezone != "" {
		if loc, err := time.LoadLocation(conf.Timezone); err == nil {
			return loc
		}
	}
	return conf.Loc()
}

func socialZipStageSlug(venue string) string {
	switch strings.ToLower(strings.TrimSpace(venue)) {
	case "one", "main", "main stage":
		return "main"
	case "two", "talks", "talks stage":
		return "talks"
	case "three", "workshop", "workshops", "workshops stage":
		return "workshop"
	case "four", "lounge", "lounge stage":
		return "lounge"
	}
	if slug := socialZipSlug(types.NameVenue(venue)); slug != "" && slug != "not-listed-yet" {
		return slug
	}
	return socialZipSlug(venue)
}
