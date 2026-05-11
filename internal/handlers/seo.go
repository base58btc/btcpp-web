package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

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

// redirectStripConfPrefix 301-redirects from the legacy `/{tag}*`
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
// confs cache so newly-published events show up as soon as their
// Notion row flips Active. Past confs stay in the map (Vienna-2024
// search hits still resolve to a real page); upcoming confs get a
// daily changefreq and higher priority, ended confs a monthly /
// lower priority so crawl budget skews to current campaigns.
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
	today := time.Now().UTC().Format("2006-01-02")

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
	}
	for _, s := range static {
		writeSitemapURL(w, SEOHost+s.Path, today, s.Freq, s.Prio)
	}

	for _, c := range confs {
		if c == nil || c.Tag == "" {
			continue
		}
		prio := "0.6"
		freq := "monthly"
		if !c.HasEnded() {
			prio = "0.9"
			freq = "daily"
		}
		writeSitemapURL(w, SEOHost+"/"+c.Tag, today, freq, prio)
		// Talks page is gated on Conf.HasAgenda — populated at
		// render time, not on the cached Conf, so compute it here
		// against the live talks slice.
		talks, _ := getters.GetTalksFor(ctx, c.Tag)
		if anyScheduledTalk(talks) {
			writeSitemapURL(w, SEOHost+"/"+c.Tag+"/talks", today, freq, "0.6")
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
