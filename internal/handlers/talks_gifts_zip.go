package handlers

import (
	"archive/zip"
	"fmt"
	"net/http"
	"path/filepath"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// TalksGiftsClipartZip streams a zip of every clipart file referenced
// by the speaker-gifts list for one conf — one entry per row, named
// "{speaker-slug}.{ext}" so the export lines up with the CSV
// (speaker → photo) one-to-one. Drives the "Download clipart" button
// on /talks/gifts. Global-admin only, mirroring TalksGifts.
//
// Cliparts live in Spaces under talks/<filename> (uploaded by the
// per-conf clipart admin). Rows with an empty Clipart are skipped;
// rows whose clipart isn't yet in Spaces are also skipped (logged
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
	seen := map[string]int{}

	for _, t := range talks {
		if t == nil || t.Clipart == "" {
			continue
		}
		key := "talks/" + t.Clipart
		// Fetch the clipart bytes once per talk; multiple speakers
		// on the same talk reuse this fetch by writing it into the
		// zip under each speaker's name.
		data, err := spaces.Get(key)
		if err != nil {
			ctx.Infos.Printf("/talks/gifts/clipart.zip skip %s: %s", key, err)
			skipped += len(t.Speakers)
			continue
		}
		ext := filepath.Ext(t.Clipart)
		for _, sp := range t.Speakers {
			if sp == nil {
				continue
			}
			slug := socialZipSlug(sp.Name)
			if slug == "" {
				slug = sp.ID
			}
			name := slug + ext
			seen[name]++
			if seen[name] > 1 {
				name = fmt.Sprintf("%s-%d%s", slug, seen[name], ext)
			}
			f, err := zw.Create(name)
			if err != nil {
				ctx.Err.Printf("/talks/gifts/clipart.zip create %s: %s", name, err)
				continue
			}
			if _, err := f.Write(data); err != nil {
				ctx.Err.Printf("/talks/gifts/clipart.zip write %s: %s", name, err)
				continue
			}
			added++
		}
	}

	ctx.Infos.Printf("/talks/gifts/clipart.zip %s: %d entries, %d skipped", conf.Tag, added, skipped)
}
