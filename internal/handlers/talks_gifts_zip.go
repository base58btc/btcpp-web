package handlers

import (
	"archive/zip"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/external/spaces"
	"btcpp-web/internal/auth"
	"btcpp-web/internal/config"
	"btcpp-web/internal/types"
)

// giftsRequireConfAccess gates the /talks/gifts pages on staff-or-
// above for at least one conf. Returns the resolved identity on
// success; on failure it has already written the redirect (login
// when unauthed, /dashboard when authed-but-no-roles) and the
// caller should return immediately.
func giftsRequireConfAccess(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) *auth.Identity {
	id := auth.RequireOptional(r, ctx)
	if id == nil {
		next := r.URL.RequestURI()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
		return nil
	}
	// Global admin or any per-conf staff-or-above qualifies. We do
	// NOT require a specific conf here — the caller will narrow on
	// ?conf= once we know which event is in play.
	if id.IsGlobalAdmin() {
		return id
	}
	allConfs, _ := getters.FetchConfsCached(ctx)
	for _, c := range allConfs {
		if c == nil {
			continue
		}
		if id.HasRoleForConf(c.Tag, auth.RoleStaff) {
			return id
		}
	}
	ctx.Infos.Printf("auth deny /talks/gifts for %s — no staff/admin role for any conf", id.Email)
	http.Redirect(w, r, "/dashboard?error="+url.QueryEscape("You don't have access to gifts CSV for any event."), http.StatusSeeOther)
	return nil
}

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

// visibleGiftConfs filters the cached conf list to those the
// identity has staff-or-above for. Global admins see every conf;
// per-conf staff/admin only see their own.
func visibleGiftConfs(id *auth.Identity, all []*types.Conf) []*types.Conf {
	if id == nil {
		return nil
	}
	isGlobal := id.IsGlobalAdmin()
	out := make([]*types.Conf, 0, len(all))
	for _, c := range all {
		if c == nil {
			continue
		}
		if isGlobal || id.HasRoleForConf(c.Tag, auth.RoleStaff) {
			out = append(out, c)
		}
	}
	return out
}

// TalksGiftsClipartZip streams a zip of every clipart file referenced
// by the speaker-gifts list for one conf. Entries are named with the
// raw ConfTalk.Clipart filename ("vienna_bitcoin.png"), so a clipart
// shared across multiple speakers on the same talk lands once in
// the zip rather than duplicated. Drives the "Download clipart"
// button on /talks/gifts. Staff-or-above for the requested conf
// (or any global admin), mirroring TalksGifts.
//
// Cliparts live in Spaces under talks/<filename> (uploaded by the
// per-conf clipart admin). Talks with an empty Clipart are skipped;
// talks whose clipart isn't yet in Spaces are also skipped (logged
// but not fatal — the admin still gets the cliparts that exist).
func TalksGiftsClipartZip(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	id := giftsRequireConfAccess(w, r, ctx)
	if id == nil {
		return
	}

	confTag := r.URL.Query().Get("conf")
	if confTag == "" {
		http.Error(w, "conf query param required", http.StatusBadRequest)
		return
	}
	if !id.HasRoleForConf(confTag, auth.RoleStaff) {
		ctx.Infos.Printf("auth deny /talks/gifts/clipart.zip ?conf=%s for %s", confTag, id.Email)
		http.Redirect(w, r, "/dashboard?error="+url.QueryEscape("You don't have access to that event's gifts."), http.StatusSeeOther)
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

	// {conf}-staff users (who don't have a talk) get the conf's
	// leading.png as their gift clipart. Add a single "leading.png"
	// entry to the zip when there's at least one such staffer; the
	// CSV already names every staff row's photo as "leading.png",
	// so one file in the zip covers the whole staff cohort.
	if len(staffSpeakersForConf(ctx, conf.Tag)) > 0 && !seen["leading.png"] {
		path := "static/img/" + conf.Tag + "/leading.png"
		data, err := os.ReadFile(path)
		if err != nil {
			ctx.Infos.Printf("/talks/gifts/clipart.zip skip staff leading.png (%s): %s", path, err)
			skipped++
		} else {
			f, err := zw.Create("leading.png")
			if err != nil {
				ctx.Err.Printf("/talks/gifts/clipart.zip create leading.png: %s", err)
			} else if _, err := f.Write(data); err != nil {
				ctx.Err.Printf("/talks/gifts/clipart.zip write leading.png: %s", err)
			} else {
				added++
			}
		}
	}

	ctx.Infos.Printf("/talks/gifts/clipart.zip %s: %d entries, %d skipped", conf.Tag, added, skipped)
}
