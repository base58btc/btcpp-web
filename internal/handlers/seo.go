package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/config"
)

// staticCache wraps an http.Handler with a 1-hour Cache-Control
// header so browsers can serve repeat-visitor /static/* assets from
// cache without revalidating. http.FileServer still emits
// Last-Modified, so a deploy invalidates stale assets via a
// conditional GET → 304 cycle once the hour elapses.
//
// Short max-age (3600s) is deliberate: mini.css has no content-hash
// in the filename, so a longer window could leave visitors on stale
// CSS after a Tailwind rebuild. Move to a fingerprinted-filename
// strategy if we want to push max-age much higher.
func staticCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		h.ServeHTTP(w, r)
	})
}

// redirectStripConfPrefix 301-redirects from the legacy `/conf/{tag}*`
// URL form to the canonical `/{tag}*` short form. The handler only
// rewrites the path; query string carries through, and the browser
// preserves the hash fragment across the redirect on its own.
func redirectStripConfPrefix(w http.ResponseWriter, r *http.Request) {
	target := strings.TrimPrefix(r.URL.Path, "/conf")
	if target == "" || target[0] != '/' {
		target = "/" + target
	}
	if r.URL.RawQuery != "" {
		target = target + "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
}

func noIndexRobots(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if shouldNoIndexPath(r.URL.Path) {
			w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		}
		h.ServeHTTP(w, r)
	})
}

func shouldNoIndexPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return false
	}
	if strings.HasSuffix(path, "/calendar.ics") {
		return true
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case "admin", "api", "auth", "callback", "check-in", "conf-reload",
		"dashboard", "i", "invite-speaker", "login", "media", "sendcal",
		"ticket", "tix", "logout", "trial-cal-invite", "trial-email", "vols",
		"webhook", "welcome-email":
		return true
	}
	if len(parts) >= 2 {
		switch parts[1] {
		case "admin", "success", "volcoord":
			return true
		}
	}
	if len(parts) >= 3 && parts[0] == "conf" && parts[2] == "success" {
		return true
	}
	return false
}

func confSocialImage(tag, card string) string {
	tag = strings.TrimSpace(tag)
	card = strings.TrimSpace(card)
	if card != "twitter" {
		card = "standard"
	}

	switch tag {
	case "atx22", "atx23", "cdmx22":
		return SEOHost + "/static/img/atxpromo.png"
	case "atx24":
		return SEOHost + "/static/img/atx24.png"
	case "atx25":
		return SEOHost + "/static/img/atx25_promo.png"
	case "ba24":
		return SEOHost + "/static/img/ba24.png"
	case "berlin23":
		return SEOHost + "/static/img/btcpp_berlin_twitter.png"
	case "berlin24":
		return SEOHost + "/static/img/berlin24_promo.png"
	case "floripa":
		return SEOHost + "/static/img/floripa_promo.png"
	default:
		return fmt.Sprintf("%s/static/img/%s/og_card_%s.png", SEOHost, tag, card)
	}
}

func absoluteSEOURL(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return SEOHost + "/"
	}
	if strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "http://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return SEOHost + path
}

// SEOHost is the canonical absolute base used in robots.txt + sitemap
// + OG tags. Hardcoded to match what's already baked into the
// templates/section/og_tags.tmpl partial — keep them in sync.
const SEOHost = "https://btcpp.dev"

// Robots serves /robots.txt. The file lives in the static/ tree so
// the policy is editable without a redeploy, but it's mounted at the
// site root (where crawlers look) via this handler rather than the
// /static/* prefix.
func Robots(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeFile(w, r, "static/robots.txt")
}

// Sitemap serves /sitemap.xml — rebuilt on each request from the
// confs cache so newly-created event pages are discoverable quickly.
// Inactive and past confs stay in the map because their public pages
// are still useful archives; active upcoming confs get a higher
// priority so crawl budget skews to current campaigns.
//
// Conf-talks page (`/{tag}/talks`) is only included when at
// least one of the conf's talks is Status=Scheduled — same gate as
// the nav-bar link, so the sitemap never points at a soft-empty
// page.
func Sitemap(w http.ResponseWriter, r *http.Request, ctx *config.AppContext) {
	confs, err := getters.FetchConfsCached(ctx)
	if err != nil {
		ctx.Err.Printf("/sitemap.xml confs: %s", err)
		http.Error(w, "Unable to load confs", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")

	fmt.Fprintln(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprintln(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)

	// Evergreen public pages — homepage + apply / contact / legal.
	static := []struct {
		Path, Freq, Prio string
	}{
		{"/", "weekly", "1.0"},
		{"/talk", "monthly", "0.7"},
		{"/volunteer", "monthly", "0.7"},
		{"/sponsor", "monthly", "0.6"},
		{"/contact", "monthly", "0.5"},
		{"/privacy", "yearly", "0.2"},
		{"/terms", "yearly", "0.2"},
	}
	for _, s := range static {
		writeSitemapURL(w, SEOHost+s.Path, "", s.Freq, s.Prio)
	}

	for _, c := range confs {
		if c == nil || c.Tag == "" {
			continue
		}
		prio := "0.6"
		freq := "monthly"
		if c.Active && !c.HasEnded() {
			prio = "0.9"
			freq = "daily"
		}
		writeSitemapURL(w, SEOHost+"/"+c.Tag, "", freq, prio)
		// Talks page is gated on Conf.HasAgenda — populated at
		// render time, not on the cached Conf, so compute it here
		// against the live talks slice.
		talks, _ := getters.GetTalksFor(ctx, c.Tag)
		if anyScheduledTalk(talks) {
			writeSitemapURL(w, SEOHost+"/"+c.Tag+"/talks", "", freq, "0.6")
		}
	}

	fmt.Fprintln(w, `</urlset>`)
}

func writeSitemapURL(w http.ResponseWriter, loc, lastmod, changefreq, priority string) {
	fmt.Fprintln(w, `  <url>`)
	fmt.Fprintf(w, "    <loc>%s</loc>\n", loc)
	if lastmod != "" {
		fmt.Fprintf(w, "    <lastmod>%s</lastmod>\n", lastmod)
	}
	if changefreq != "" {
		fmt.Fprintf(w, "    <changefreq>%s</changefreq>\n", changefreq)
	}
	if priority != "" {
		fmt.Fprintf(w, "    <priority>%s</priority>\n", priority)
	}
	fmt.Fprintln(w, `  </url>`)
}
