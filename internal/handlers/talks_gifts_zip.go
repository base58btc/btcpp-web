package handlers

import (
	"archive/zip"
	"fmt"
	"net/http"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// TalksGiftsClipartZip streams a zip of every clipart file referenced
// by the speaker-gifts list for one conf. Entries are named with the
// raw ConfTalk.Clipart filename ("vienna_bitcoin.png"), so a clipart
// shared across multiple speakers on the same talk lands once in
// the zip rather than duplicated. Drives the "Download clipart"
// button on /talks/gifts. Global-admin only, mirroring TalksGifts.
//
// Cliparts live in Spaces under talks/<filename> (uploaded by the
// per-conf clipart admin). Talks with an empty Clipart are skipped;
// talks whose clipart isn't yet in Spaces are also skipped (logged
// but not fatal — the admin still gets the cliparts that exist).
func TalksGiftsClipartZip(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	if id := requireGlobalAdmin(w, r, ctx); id == nil {
		return
	}

	confTag := r.URL.Query().Get("conf")
	if confTag == "" {
		http.Error(w, "conf query param required", http.StatusBadRequest)
		return
	}

	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/talks/gifts/clipart.zip fetch confs: %s", err)
		http.Error(w, "Unable to load conferences", http.StatusInternalServerError)
		return
	}
	var conf *types.Conf
	for _, c := range confs {
		if c != nil && c.Tag == confTag {
			conf = c
			break
		}
	}
	if conf == nil {
		http.NotFound(w, r)
		return
	}

	talks, err := getters.GetTalksFor(ctx, conf.Tag)
	if err != nil {
		ctx.Err.Printf("/talks/gifts/clipart.zip talks for %s: %s", conf.Tag, err)
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
			ctx.Infos.Printf("/talks/gifts/clipart.zip skip %s: %s", key, err)
			skipped++
			continue
		}
		f, err := zw.Create(t.Clipart)
		if err != nil {
			ctx.Err.Printf("/talks/gifts/clipart.zip create %s: %s", t.Clipart, err)
			continue
		}
		if _, err := f.Write(data); err != nil {
			ctx.Err.Printf("/talks/gifts/clipart.zip write %s: %s", t.Clipart, err)
			continue
		}
		added++
	}

	ctx.Infos.Printf("/talks/gifts/clipart.zip %s: %d entries, %d skipped", conf.Tag, added, skipped)
}
