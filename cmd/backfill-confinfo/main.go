// backfill-confinfo seeds ConfInfoDb from the per-conf agenda blocks in
// templates/conf/*.tmpl. For every day h3 in a conf's agenda section, it
// extracts the Check-in/Venue Opens, Lunch, and Afternoon Break times and
// writes one ConfInfo row per (conf, day).
//
// Idempotent: queries the DB up-front and skips any (Conf, Day) that
// already exists. Safe to re-run after editing templates — only new
// (conf, day) pairs are created.
//
// Required config.toml additions:
//
//	[notion]
//	token       = "..."
//	confinfodb  = "<page id>"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"btcpp-web/internal/types"

	"github.com/BurntSushi/toml"
	notion "github.com/niftynei/go-notion"
)

const (
	configFile  = "config.toml"
	templateDir = "templates/conf"
)

type cfgFile struct {
	Notion struct {
		Token      string `toml:"token"`
		ConfInfoDb string `toml:"confinfodb"`
	} `toml:"notion"`
}

// dayPlan is one row's worth of data extracted from a template. Doors is
// a single military "HH:MM"; Lunch and Coffee are "HH:MM,HH:MM" ranges.
// Empty strings mean the field wasn't found in the day's spans.
type dayPlan struct {
	Tag    string
	Day    int
	Doors  string
	Lunch  string
	Coffee string
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Preview without writing")
	flag.Parse()

	var c cfgFile
	if _, err := toml.DecodeFile(configFile, &c); err != nil {
		log.Fatalf("read %s: %s", configFile, err)
	}
	if c.Notion.Token == "" || c.Notion.ConfInfoDb == "" {
		log.Fatalf("missing notion.token / confinfodb in %s", configFile)
	}

	n := &types.Notion{}
	n.Setup(c.Notion.Token)

	plans, err := planFromTemplates(templateDir)
	if err != nil {
		log.Fatalf("parse templates: %s", err)
	}
	log.Printf("parsed %d day-rows from templates", len(plans))

	existing, err := loadExisting(n.Client, c.Notion.ConfInfoDb)
	if err != nil {
		log.Fatalf("load existing rows: %s", err)
	}
	log.Printf("found %d existing (tag, day) rows in ConfInfoDb", len(existing))

	created, skippedExist, failed := 0, 0, 0
	for _, p := range plans {
		key := fmt.Sprintf("%s/%d", p.Tag, p.Day)
		if existing[key] {
			log.Printf("· %s day=%d already in DB — skip", p.Tag, p.Day)
			skippedExist++
			continue
		}
		log.Printf("→ %s day=%d  doors=%q lunch=%q coffee=%q",
			p.Tag, p.Day, p.Doors, p.Lunch, p.Coffee)
		if *dryRun {
			created++
			continue
		}
		id, err := createConfInfo(n, c.Notion.ConfInfoDb, p)
		if err != nil {
			log.Printf("  FAILED: %s", err)
			failed++
			continue
		}
		log.Printf("  ✓ %s", id)
		created++
	}
	log.Printf("done: created=%d skipped-existing=%d failed=%d total=%d",
		created, skippedExist, failed, len(plans))
}

// planFromTemplates walks every .tmpl in dir and returns one dayPlan per
// (conf, day) found in the agenda block. The conf tag is the filename
// without the .tmpl suffix.
func planFromTemplates(dir string) ([]dayPlan, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.tmpl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	var out []dayPlan
	for _, path := range matches {
		tag := strings.TrimSuffix(filepath.Base(path), ".tmpl")
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		days := parseAgenda(tag, string(raw))
		for i, d := range days {
			d.Tag = tag
			d.Day = i + 1
			out = append(out, d)
		}
	}
	return out, nil
}

// agendaSectionRE bounds the search to the agenda <section>. Without
// this, day h3s inside other sections (e.g. "Where to Stay") would be
// captured. (?s) makes . match newlines for the cross-line section
// body; non-greedy so the match stops at the first </section>.
var agendaSectionRE = regexp.MustCompile(
	`(?s)<section id="agenda">(.*?)</section>`)

// dayHeaderRE matches a per-day h3 inside the agenda block. Templates
// use mt-8 most often but istanbul (and likely others) uses mt-32 on
// the first / third day h3, so accept any mt-N spacing class.
var dayHeaderRE = regexp.MustCompile(
	`<h3 class="mt-\d+ text-xl tracking-tight text-gray-900 sm:text-xl">[^<]+</h3>`)

// agendaSpanRE captures the schedule strip spans, e.g.
//
//	<span class="bg-white px-2 text-sm text-gray-500">Lunch, 12 pm - 1 pm</span>
var agendaSpanRE = regexp.MustCompile(
	`<span class="bg-white px-2 text-sm text-gray-500">([^<]+)</span>`)

// parseAgenda returns one dayPlan per day h3 in the template, in
// document order (Day 1 = first h3). Empty fields mean the span wasn't
// present for that day; we skip rather than fail because confs vary
// (e.g. floripa later days have "Venue Opens" instead of "Check-in").
//
// tag is passed through purely for log context — per-field parse errors
// are logged but don't abort the conf.
func parseAgenda(tag, content string) []dayPlan {
	section := agendaSectionRE.FindStringSubmatch(content)
	if section == nil {
		return nil
	}
	body := section[1]

	headers := dayHeaderRE.FindAllStringIndex(body, -1)
	if len(headers) == 0 {
		return nil
	}
	var days []dayPlan
	for i, h := range headers {
		end := len(body)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		block := body[h[1]:end]
		spans := agendaSpanRE.FindAllStringSubmatch(block, -1)
		days = append(days, planFromSpans(tag, i+1, spans))
	}
	return days
}

// planFromSpans walks the schedule strip spans for a single day and
// fills the doors/lunch/coffee fields. Unrecognized labels (e.g.
// "Venue Closes") are silently ignored — they're not in the schema.
// Per-field parse errors are logged with tag/day context and the field
// is left empty rather than aborting the whole row.
//
// Label variants handled:
//
//	Doors  : "Check-in Opens" / "Venue Opens" / "Doors Open" / "Opens"
//	Lunch  : "Lunch" / "Lunch Break"
//	Coffee : "Afternoon Break" / bare "Break"
//
// "Dinner Break" / "Evening Break" are explicitly skipped — they're
// post-program social events, not a coffee break.
func planFromSpans(tag string, day int, spans [][]string) dayPlan {
	var d dayPlan
	for _, m := range spans {
		text := strings.TrimSpace(m[1])
		label, val, found := strings.Cut(text, ",")
		if !found {
			continue
		}
		label = strings.TrimSpace(label)
		val = strings.TrimSpace(val)

		// Skip non-coffee "Break" variants before they fall through
		// to the bare-Break case below.
		if strings.HasPrefix(label, "Dinner") || strings.HasPrefix(label, "Evening") {
			continue
		}

		switch {
		case strings.HasPrefix(label, "Check-in Opens"),
			strings.HasPrefix(label, "Venue Opens"),
			strings.HasPrefix(label, "Doors Open"),
			strings.HasPrefix(label, "Opens"):
			t, err := parseHumanTime(val)
			if err != nil {
				log.Printf("WARN: %s day=%d doors %q: %s", tag, day, val, err)
				continue
			}
			d.Doors = t
		case strings.HasPrefix(label, "Lunch"):
			r, err := parseTimeRange(val)
			if err != nil {
				log.Printf("WARN: %s day=%d lunch %q: %s", tag, day, val, err)
				continue
			}
			d.Lunch = r
		case strings.HasPrefix(label, "Afternoon Break"),
			strings.HasPrefix(label, "Break"):
			r, err := parseTimeRange(val)
			if err != nil {
				log.Printf("WARN: %s day=%d coffee %q: %s", tag, day, val, err)
				continue
			}
			d.Coffee = r
		}
	}
	return d
}

// parseTimeRange parses "12 pm - 1 pm" / "2:30 pm - 3:00 pm" /
// "12:00hs - 13:00hs" into "HH:MM,HH:MM" military.
//
// Returns an error when end < start — indicates a typo in the source
// template (e.g. "11:45 pm" where the author meant "am"). The caller
// surfaces the warning and leaves the field empty rather than writing
// nonsense to Notion.
func parseTimeRange(s string) (string, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("no range delimiter in %q", s)
	}
	start, err := parseHumanTime(parts[0])
	if err != nil {
		return "", err
	}
	end, err := parseHumanTime(parts[1])
	if err != nil {
		return "", err
	}
	if end < start {
		return "", fmt.Errorf("end before start in %q (got %s,%s) — likely am/pm typo in template", s, start, end)
	}
	return start + "," + end, nil
}

// parseHumanTime accepts the variants used across the conf templates and
// returns "HH:MM" military:
//
//	"9:00 am" / "9 am" / "12 pm" / "2:30 pm"   12-hour with am/pm
//	"12:00hs" / "13:00hs"                       military with Brazilian "hs"
func parseHumanTime(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "hs")
	s = strings.TrimSpace(s)

	isPM := strings.HasSuffix(s, "pm")
	isAM := strings.HasSuffix(s, "am")
	s = strings.TrimSuffix(s, "pm")
	s = strings.TrimSuffix(s, "am")
	s = strings.TrimSpace(s)

	parts := strings.Split(s, ":")
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return "", fmt.Errorf("bad hour in %q", s)
	}
	min := 0
	if len(parts) > 1 {
		min, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return "", fmt.Errorf("bad minute in %q", s)
		}
	}
	if isPM && hour < 12 {
		hour += 12
	}
	if isAM && hour == 12 {
		hour = 0
	}
	if hour < 0 || hour > 23 || min < 0 || min > 59 {
		return "", fmt.Errorf("out of range %q", s)
	}
	return fmt.Sprintf("%02d:%02d", hour, min), nil
}

// loadExisting returns the set of "tag/day" keys already present in the
// DB so we can skip duplicates. Reads the Conf column as Select first,
// falls back to rich_text — matches the parser's lookup order.
func loadExisting(client notion.API, db string) (map[string]bool, error) {
	out := map[string]bool{}
	cur := ""
	more := true
	for more {
		pages, next, hm, err := client.QueryDatabase(context.Background(), db,
			notion.QueryDatabaseParam{StartCursor: cur})
		if err != nil {
			return nil, err
		}
		cur = next
		more = hm
		for _, pg := range pages {
			tag := ""
			if sel := pg.Properties["Conf"].Select; sel != nil {
				tag = sel.Name
			}
			if tag == "" {
				for _, rt := range pg.Properties["Conf"].RichText {
					if rt != nil && rt.Text != nil {
						tag += rt.Text.Content
					}
				}
			}
			day := int(pg.Properties["Day"].Number)
			if tag == "" || day == 0 {
				continue
			}
			out[fmt.Sprintf("%s/%d", tag, day)] = true
		}
	}
	return out, nil
}

// createConfInfo writes one row. Name (title) is set to the conf tag —
// the user's stated convention, since Notion DB titles must be non-empty
// and we have no better label.
func createConfInfo(n *types.Notion, db string, p dayPlan) (string, error) {
	vals := map[string]*notion.PropertyValue{
		"Name": notion.NewTitlePropertyValue(
			&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: p.Tag}},
		),
		"Conf": {
			Type:   notion.PropertySelect,
			Select: &notion.SelectOption{Name: p.Tag},
		},
		"Day": {
			Type:   notion.PropertyNumber,
			Number: float64(p.Day),
		},
	}
	if p.Doors != "" {
		vals["Doors"] = richText("Doors", p.Doors)
	}
	if p.Lunch != "" {
		vals["Lunch"] = richText("Lunch", p.Lunch)
	}
	if p.Coffee != "" {
		vals["Coffee"] = richText("Coffee", p.Coffee)
	}
	page, err := n.Client.CreatePage(context.Background(),
		notion.NewDatabaseParent(db), vals)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

// richText is a tiny helper to keep createConfInfo readable. Field name
// is unused but kept on the call site as documentation.
func richText(_ string, content string) *notion.PropertyValue {
	return notion.NewRichTextPropertyValue(
		&notion.RichText{Type: notion.RichTextText, Text: &notion.Text{Content: content}},
	)
}
