package getters

import (
	"time"

	"btcpp-web/internal/config"
)

// SiteStatsValues holds the raw counts behind the about-page numbers.
// Format-for-display is left to callers.
type SiteStatsValues struct {
	PastConfs int // count of confs where EndDate is in the past
	PastTalks int // count of Accepted ConfTalks at past confs
	Attendees int // total registration rows, rough attendee count
}

// getSiteStats recomputes the about-page aggregate counters from the already
// warm Confs / ConfTalks caches plus a backend attendee count.
func getSiteStats(ctx *config.AppContext) {
	ctx.Infos.Printf("getting site stats...")
	var s SiteStatsValues

	for _, c := range confs {
		if c != nil && c.HasEnded() {
			s.PastConfs++
		}
	}

	confTalkCacheMu.RLock()
	for _, ct := range cacheConfTalks {
		if ct == nil || ct.Conf == nil || !ct.Conf.HasEnded() {
			continue
		}
		if ct.Proposal != nil && ct.Proposal.Status == "Accepted" {
			s.PastTalks++
		}
	}
	confTalkCacheMu.RUnlock()

	attendees, err := siteStatsAttendees(ctx)
	if err != nil {
		ctx.Err.Printf("site stats registrations scan: %s", err)
	} else {
		s.Attendees = attendees
	}

	siteStatsMu.Lock()
	siteStats = s
	siteStatsMu.Unlock()
	ctx.Infos.Printf("Loaded site stats: confs=%d talks=%d attendees=%d",
		s.PastConfs, s.PastTalks, s.Attendees)
}

func siteStatsAttendees(ctx *config.AppContext) (int, error) {
	if UsePostgresBackend(ctx) {
		return siteStatsAttendeesPostgres(ctx)
	}
	return siteStatsAttendeesNotion(ctx)
}

// FetchSiteStats returns the cached about-page counters and queues a
// background refresh on TTL expiry.
func FetchSiteStats(ctx *config.AppContext) SiteStatsValues {
	siteStatsMu.RLock()
	s := siteStats
	stale := lastSiteStatsFetch.Before(time.Now().Add(-cacheTTL))
	siteStatsMu.RUnlock()
	if stale {
		lastSiteStatsFetch = time.Now()
		queueRefresh(JobSiteStats)
	}
	return s
}
