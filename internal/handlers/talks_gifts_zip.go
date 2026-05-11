package handlers

import (
	"archive/zip"
	"fmt"
	"net/http"
	"os"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/auth"
	"btcpp-web/internal/config"
	"btcpp-web/internal/helpers"
	"btcpp-web/internal/types"
)

// staffSpeakersForConf returns every Speaker whose Roles include
// "{conf}-staff" (case-insensitive). Used by the gifts page to
// surface non-talk staffers — folks who don't have a ConfTalk row
// but still need a clipart-handed gift on the print run.
//
// admin and volcoord do NOT count: only an explicit `{conf}-staff`
// tag adds someone here. (admin gets the gifts page; staff gets a
// gift bag.)
func staffSpeakersForConf(ctx *config.AppContext, confTag string) []*types.Speaker {
	if confTag == "" {
		return nil
	}
	all, err := getters.FetchSpeakersCached(ctx)
	if err != nil || len(all) == 0 {
		return nil
	}
	want := strings.ToLower(strings.TrimSpace(confTag)) + "-" + auth.RoleStaff
	out := make([]*types.Speaker, 0)
	for _, sp := range all {
		if sp == nil {
			continue
		}
		for _, raw := range sp.Roles {
			if strings.EqualFold(strings.TrimSpace(raw), want) {
				out = append(out, sp)
				break
			}
		}
	}
	return out
}

// AdminGiftsClipartZip streams a zip of every clipart file referenced
// by the speaker-gifts list for one conf. Entries are named with the
// raw ConfTalk.Clipart filename ("vienna_bitcoin.png"), so a clipart
// shared across multiple speakers on the same talk lands once in
// the zip rather than duplicated. Drives the "Download clipart"
// button on /{conf}/admin/gifts. Conf comes from the URL; auth via
// requireConfStaff.
//
// Cliparts live in Spaces under talks/<filename> (uploaded by the
// per-conf clipart admin). Talks with an empty Clipart are skipped;
// talks whose clipart isn't yet in Spaces are also skipped (logged
// but not fatal — the admin still gets the cliparts that exist).
func AdminGiftsClipartZip(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireConfStaff(w, r, ctx); id == nil {
		return
	}
	conf, err := helpers.FindConf(r, ctx)
	if err != nil || conf == nil {
		handle404(w, r, ctx)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/%s/admin/gifts/clipart.zip talks: %s", conf.Tag, err)
		http.Error(w, "Unable to load talks", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-cliparts.zip"`, conf.Tag))

	zw := zip.NewWriter(w)
	defer zw.Close()

	added := 0
	skipped := 0
	seen := map[string]bool{}

	for _, t := range talks {
		if t == nil || t.Clipart == "" {
			continue
		}
		// Multiple talks with the same Clipart filename are
		// possible (a shared visual reused). The filename inside
		// the zip is the Clipart string verbatim, so dedupe to
		// one entry per filename — the bytes are the same.
		if seen[t.Clipart] {
			continue
		}
		seen[t.Clipart] = true

		key := "talks/" + t.Clipart
		data, err := spaces.Get(key)
		if err != nil {
			ctx.Infos.Printf("/%s/admin/gifts/clipart.zip skip %s: %s", conf.Tag, key, err)
			skipped++
			continue
		}
		f, err := zw.Create(t.Clipart)
		if err != nil {
			ctx.Err.Printf("/%s/admin/gifts/clipart.zip create %s: %s", conf.Tag, t.Clipart, err)
			continue
		}
		if _, err := f.Write(data); err != nil {
			ctx.Err.Printf("/%s/admin/gifts/clipart.zip write %s: %s", conf.Tag, t.Clipart, err)
			continue
		}
		added++
	}

	// {conf}-staff users (who don't have a talk) get the conf's
	// leading.png as their gift clipart. Add a single "leading.png"
	// entry to the zip when there's at least one such staffer; the
	// CSV already names every staff row's photo as "leading.png",
	// so one file in the zip covers the whole staff cohort.
	if len(staffSpeakersForConf(ctx, conf.Tag)) > 0 && !seen["leading.png"] {
		path := "static/img/" + conf.Tag + "/leading.png"
		data, err := os.ReadFile(path)
		if err != nil {
			ctx.Infos.Printf("/%s/admin/gifts/clipart.zip skip staff leading.png (%s): %s", conf.Tag, path, err)
			skipped++
		} else {
			f, err := zw.Create("leading.png")
			if err != nil {
				ctx.Err.Printf("/%s/admin/gifts/clipart.zip create leading.png: %s", conf.Tag, err)
			} else if _, err := f.Write(data); err != nil {
				ctx.Err.Printf("/%s/admin/gifts/clipart.zip write leading.png: %s", conf.Tag, err)
			} else {
				added++
			}
		}
	}

	ctx.Infos.Printf("/%s/admin/gifts/clipart.zip: %d entries, %d skipped", conf.Tag, added, skipped)
}
