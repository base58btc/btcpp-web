package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"btcpp-web/external/getters"
	"btcpp-web/internal/db"
	"btcpp-web/internal/types"
	"github.com/BurntSushi/toml"
	"github.com/jackc/pgx/v5/pgxpool"
)

type options struct {
	configPath        string
	databaseURL       string
	dryRun            bool
	reset             bool
	validate          bool
	listConfTalkDups  bool
	skipConfDays      bool
	skipTickets       bool
	skipDiscounts     bool
	skipPurchases     bool
	skipAffiliateUse  bool
	skipHotels        bool
	skipJobTypes      bool
	skipVolunteers    bool
	skipVolunteerInfo bool
	skipWorkShifts    bool
	skipSponsors      bool
	skipSpeakers      bool
	skipProposals     bool
	skipSpeakerConfs  bool
	skipConfTalks     bool
	skipRecordings    bool
	skipSocialPosts   bool
}

func main() {
	log.SetFlags(0)

	opts := parseFlags()
	ctx := context.Background()

	env, err := loadConfig(opts.configPath)
	if err != nil {
		log.Fatal(err)
	}
	if opts.databaseURL != "" {
		env.DatabaseURL = opts.databaseURL
	}
	if opts.listConfTalkDups {
		if err := validateConfTalkDuplicateConfig(env); err != nil {
			log.Fatal(err)
		}
		notion := &types.Notion{Config: &env.Notion}
		notion.Setup(env.Notion.Token)
		proposals, err := getters.ListProposalsOnly(notion)
		if err != nil {
			log.Fatalf("fetch proposals from Notion: %s", err)
		}
		confTalks, err := listConfTalkImportRows(notion)
		if err != nil {
			log.Fatalf("fetch conf talks from Notion: %s", err)
		}
		printConfTalkProposalDuplicates(confTalks, proposalByRef(proposals))
		return
	}
	needDB := !opts.dryRun || opts.validate
	importConfDays := !opts.skipConfDays
	importTickets := !opts.skipTickets
	importDiscounts := !opts.skipDiscounts
	importPurchases := !opts.skipPurchases
	importAffiliateUse := !opts.skipAffiliateUse
	importHotels := !opts.skipHotels
	importJobTypes := !opts.skipJobTypes
	importVolunteers := !opts.skipVolunteers
	importVolunteerInfo := !opts.skipVolunteerInfo
	importWorkShifts := !opts.skipWorkShifts
	importSponsors := !opts.skipSponsors
	importSpeakers := !opts.skipSpeakers
	importProposals := !opts.skipProposals
	importSpeakerConfs := !opts.skipSpeakerConfs
	importConfTalks := !opts.skipConfTalks
	importRecordings := !opts.skipRecordings
	importSocialPosts := !opts.skipSocialPosts
	if importSpeakerConfs && (!importSponsors || !importSpeakers || !importProposals) {
		log.Fatal("speaker conf import requires sponsors, speakers, and proposals; use -skip-speaker-confs when skipping any of those imports")
	}
	if importConfTalks && !importProposals {
		log.Fatal("conf talk import requires proposals; use -skip-conf-talks when skipping proposals")
	}
	if importRecordings && !importConfTalks {
		log.Fatal("recording import requires conf talks; use -skip-recordings when skipping conf talks")
	}
	if importSocialPosts && (!importConfTalks || !importRecordings) {
		log.Fatal("social post import requires conf talks and recordings; use -skip-social-posts when skipping either import")
	}
	if importPurchases && !importDiscounts {
		log.Fatal("purchase import requires discounts; use -skip-purchases when skipping discounts")
	}
	if importVolunteers && !importJobTypes {
		log.Fatal("volunteer import requires job types; use -skip-volunteers when skipping job types")
	}
	if importWorkShifts && (!importJobTypes || !importVolunteers) {
		log.Fatal("work shift import requires job types and volunteers; use -skip-work-shifts when skipping either import")
	}
	if err := validateConfig(env, needDB, importConfDays, importTickets, importDiscounts, importPurchases, importAffiliateUse, importHotels, importJobTypes, importVolunteers, importVolunteerInfo, importWorkShifts, importSponsors, importSpeakers, importProposals, importSpeakerConfs, importConfTalks, importRecordings, importSocialPosts); err != nil {
		log.Fatal(err)
	}

	notion := &types.Notion{Config: &env.Notion}
	notion.Setup(env.Notion.Token)

	confs, err := getters.ListConferencesOnly(notion)
	if err != nil {
		log.Fatalf("fetch conferences from Notion: %s", err)
	}
	log.Printf("fetched %d conferences from Notion", len(confs))
	confTagByRef := conferenceTagByRef(confs)

	var conferenceDays []*conferenceDayImportRow
	if importConfDays {
		conferenceDays, err = listConferenceDayImportRows(notion)
		if err != nil {
			log.Fatalf("fetch conference days from Notion: %s", err)
		}
		if err := validateConferenceDayRows(conferenceDays, confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d conference days from Notion", len(conferenceDays))
	}

	var tickets []*types.ConfTicket
	if importTickets {
		tickets, err = getters.ListConfTickets(notion)
		if err != nil {
			log.Fatalf("fetch conference tickets from Notion: %s", err)
		}
		if err := validateTicketKeys(tickets, confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d conference tickets from Notion", len(tickets))
	}

	var discounts []*types.DiscountCode
	if importDiscounts {
		discounts, err = getters.ListDiscounts(notion)
		if err != nil {
			log.Fatalf("fetch discounts from Notion: %s", err)
		}
		if err := validateDiscountRows(discounts, confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d discounts from Notion", len(discounts))
	}

	var purchases []*purchaseImportRow
	if importPurchases {
		purchases, err = listPurchaseImportRows(notion)
		if err != nil {
			log.Fatalf("fetch purchases from Notion: %s", err)
		}
		if err := validatePurchaseRows(purchases, confTagByRef, discountRefsByRef(discounts)); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d purchases from Notion", len(purchases))
	}

	var affiliateUsages []*affiliateUsageImportRow
	if importAffiliateUse {
		affiliateUsages, err = listAffiliateUsageImportRows(notion)
		if err != nil {
			log.Fatalf("fetch affiliate usages from Notion: %s", err)
		}
		if err := validateAffiliateUsageRows(affiliateUsages, confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d affiliate usages from Notion", len(affiliateUsages))
	}

	var hotels []*types.Hotel
	if importHotels {
		hotels, err = getters.ListHotels(notion)
		if err != nil {
			log.Fatalf("fetch hotels from Notion: %s", err)
		}
		if err := validateHotelRows(hotels, confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d hotels from Notion", len(hotels))
	}

	var jobTypes []*types.JobType
	if importJobTypes {
		jobTypes, err = getters.ListJobs(notion)
		if err != nil {
			log.Fatalf("fetch job types from Notion: %s", err)
		}
		if err := validateJobTypeRows(jobTypes); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d job types from Notion", len(jobTypes))
	}

	var volunteers []*volunteerImportRow
	if importVolunteers {
		volunteers, err = listVolunteerImportRows(notion)
		if err != nil {
			log.Fatalf("fetch volunteers from Notion: %s", err)
		}
		if err := validateVolunteerRows(volunteers, confTagByRef, jobTypeTagByRef(jobTypes)); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d volunteers from Notion", len(volunteers))
	}

	var volunteerInfo []*volunteerInfoImportRow
	if importVolunteerInfo {
		volunteerInfo, err = listVolunteerInfoImportRows(notion)
		if err != nil {
			log.Fatalf("fetch volunteer info from Notion: %s", err)
		}
		if err := validateVolunteerInfoRows(volunteerInfo, confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d volunteer info rows from Notion", len(volunteerInfo))
	}

	var workShifts []*workShiftImportRow
	if importWorkShifts {
		workShifts, err = listWorkShiftImportRows(notion)
		if err != nil {
			log.Fatalf("fetch work shifts from Notion: %s", err)
		}
		if err := validateWorkShiftRows(workShifts, confTagByRef, jobTypeTagByRef(jobTypes), volunteerRefsByRef(volunteers)); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d work shifts from Notion", len(workShifts))
	}

	var orgs []*types.Org
	var sponsorships []*types.Sponsorship
	if importSponsors {
		orgs, err = getters.ListOrgs(notion)
		if err != nil {
			log.Fatalf("fetch organizations from Notion: %s", err)
		}
		if err := validateOrganizationKeys(orgs); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d organizations from Notion", len(orgs))

		sponsorships, err = getters.ListSponsorshipsOnly(notion)
		if err != nil {
			log.Fatalf("fetch sponsorships from Notion: %s", err)
		}
		if err := validateSponsorshipKeys(sponsorships, orgRefByRef(orgs), confTagByRef); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d sponsorships from Notion", len(sponsorships))
	}

	var speakers []*types.Speaker
	if importSpeakers {
		speakers, err = getters.ListSpeakers(notion)
		if err != nil {
			log.Fatalf("fetch speakers from Notion: %s", err)
		}
		if err := validateSpeakerRows(speakers); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d speakers from Notion", len(speakers))
	}

	var proposals []*types.Proposal
	if importProposals {
		proposals, err = getters.ListProposalsOnly(notion)
		if err != nil {
			log.Fatalf("fetch proposals from Notion: %s", err)
		}
		if err := validateProposalRows(proposals); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d proposals from Notion", len(proposals))
	}

	var speakerConfs []*speakerConfImportRow
	if importSpeakerConfs {
		speakerConfs, err = listSpeakerConfImportRows(notion)
		if err != nil {
			log.Fatalf("fetch speaker confs from Notion: %s", err)
		}
		if err := validateSpeakerConfRows(speakerConfs, speakerRefByRef(speakers), proposalByRef(proposals), orgRefByRef(orgs)); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d speaker confs from Notion", len(speakerConfs))
	}

	var confTalks []*confTalkImportRow
	if importConfTalks {
		confTalks, err = listConfTalkImportRows(notion)
		if err != nil {
			log.Fatalf("fetch conf talks from Notion: %s", err)
		}
		if err := validateConfTalkRows(confTalks, confTagByRef, proposalByRef(proposals)); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d conf talks from Notion", len(confTalks))
	}

	var recordings []*recordingImportRow
	if importRecordings {
		recordings, err = listRecordingImportRows(notion)
		if err != nil {
			log.Fatalf("fetch recordings from Notion: %s", err)
		}
		if err := validateRecordingRows(recordings, confTalkRefsByRef(confTalks)); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d recordings from Notion", len(recordings))
	}

	var socialPosts []*socialPostImportRow
	if importSocialPosts {
		socialPosts, err = listSocialPostImportRows(notion)
		if err != nil {
			log.Fatalf("fetch social posts from Notion: %s", err)
		}
		if err := validateSocialPostRows(socialPosts, recordingRefsByRef(recordings), confTalkRefsByRef(confTalks)); err != nil {
			log.Fatal(err)
		}
		log.Printf("fetched %d social posts from Notion", len(socialPosts))
	}

	var pool *pgxpool.Pool
	if !opts.dryRun || opts.validate {
		pool, err = db.Open(ctx, env.DatabaseURL)
		if err != nil {
			log.Fatal(err)
		}
		defer pool.Close()
	}

	if opts.reset && !opts.dryRun {
		if err := resetDatabase(ctx, pool); err != nil {
			log.Fatalf("reset postgres tables: %s", err)
		}
		log.Printf("reset postgres tables")
	}

	if opts.dryRun {
		for _, conf := range confs {
			log.Printf("dry-run conference tag=%q uid=%d active=%t start=%s end=%s", conf.Tag, conf.UID, conf.Active, dateString(conf.StartDate), dateString(conf.EndDate))
		}
		for _, conferenceDay := range conferenceDays {
			log.Printf("dry-run conference-day conf=%q day=%d venues=%d", conferenceDay.confTag, conferenceDay.dayNumber, len(conferenceDay.venues))
		}
		for _, ticket := range tickets {
			confTag := confTagByRef[ticket.ConfRef]
			log.Printf("dry-run conference-ticket conf=%q key=%q tier=%q local=%d btc=%d usd=%d max=%d", confTag, ticketKey(ticket), ticket.Tier, ticket.Local, ticket.BTC, ticket.USD, ticket.Max)
		}
		for _, discount := range discounts {
			log.Printf("dry-run discount code=%q expr=%q confs=%d uses=%d", discount.CodeName, discount.Discount, len(discount.ConfRef), discount.UsesCount)
		}
		for _, purchase := range purchases {
			confTag := confTagByRef[purchase.confRef]
			log.Printf("dry-run purchase ref=%q conf=%q email=%q item=%q amount=%.2f", purchase.refID, confTag, purchase.email, purchase.itemBought, purchase.amountPaid)
		}
		for _, affiliateUsage := range affiliateUsages {
			log.Printf("dry-run affiliate-usage code=%q conf=%q email=%q tickets=%d saved_sats=%d earned_sats=%d", affiliateUsage.codeName, affiliateUsage.confTag, affiliateUsage.affiliateEmail, affiliateUsage.ticketsCount, affiliateUsage.savedSats, affiliateUsage.earnedSats)
		}
		for _, hotel := range hotels {
			confTag := confTagByRef[hotel.ConfRef]
			log.Printf("dry-run hotel conf=%q name=%q order=%d type=%q", confTag, hotel.Name, hotel.Order, hotel.Type)
		}
		for _, jobType := range jobTypes {
			log.Printf("dry-run job-type tag=%q title=%q order=%d show=%t", jobType.Tag, jobType.Title, jobType.DisplayOrder, jobType.Show)
		}
		for _, volunteer := range volunteers {
			log.Printf("dry-run volunteer name=%q email=%q schedule_for=%d work_yes=%d work_no=%d", volunteer.name, volunteer.email, len(volunteer.scheduleForRefs), len(volunteer.workYesRefs), len(volunteer.workNoRefs))
		}
		for _, info := range volunteerInfo {
			confTag := confTagByRef[info.confRef]
			log.Printf("dry-run volunteer-info conf=%q orient_link=%q", confTag, info.orientLink)
		}
		for _, shift := range workShifts {
			confTag := confTagByRef[shift.confRef]
			log.Printf("dry-run work-shift conf=%q name=%q assignees=%d leader=%t", confTag, shift.name, len(shift.assigneeRefs), shift.shiftLeaderRef != "")
		}
		for _, org := range orgs {
			log.Printf("dry-run organization name=%q website=%q", org.Name, org.Website)
		}
		for _, sponsorship := range sponsorships {
			confTags := sponsorshipConfTags(sponsorship, confTagByRef)
			orgName := sponsorshipOrgName(sponsorship, orgRefByRef(orgs))
			log.Printf("dry-run sponsorship name=%q org=%q confs=%q level=%q status=%q vendor=%t", sponsorship.Name, orgName, strings.Join(confTags, ","), sponsorship.Level, sponsorship.Status, sponsorship.IsVendor)
		}
		for _, speaker := range speakers {
			log.Printf("dry-run speaker name=%q email=%q roles=%q", speaker.Name, speaker.Email, strings.Join(speaker.Roles, ","))
		}
		for _, proposal := range proposals {
			confTag := ""
			if proposal.ScheduleFor != nil {
				confTag = proposal.ScheduleFor.Tag
			}
			log.Printf("dry-run proposal title=%q conf=%q status=%q speakers=%d", proposal.Title, confTag, proposal.Status, len(proposal.SpeakerConfRefs))
		}
		for _, speakerConf := range speakerConfs {
			log.Printf("dry-run speaker-conf speaker_ref=%q proposal_refs=%d other_events=%d", speakerConf.speakerRef, len(speakerConf.proposalRefs), len(speakerConf.otherEventTags))
		}
		for _, confTalk := range confTalks {
			log.Printf("dry-run conf-talk conf=%q proposal_ref=%q venue=%q", confTalk.confTag, confTalk.proposalRef, confTalk.venue)
		}
		for _, recording := range recordings {
			log.Printf("dry-run recording conf_talk_ref=%q talk_name=%q youtube=%q", recording.confTalkRef, recording.talkName, recording.youtubeURL)
		}
		for _, socialPost := range socialPosts {
			log.Printf("dry-run social-post ref=%q kind=%q status=%q recording_ref=%q conf_talk_ref=%q", socialPost.socialRef, socialPost.kind, socialPost.status, socialPost.recordingRef, socialPost.confTalkRef)
		}
	} else {
		if err := importConferences(ctx, pool, confs); err != nil {
			log.Fatal(err)
		}
		log.Printf("upserted %d conferences into Postgres", len(confs))
		if importConfDays {
			if err := importConferenceDayRows(ctx, pool, conferenceDays); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d conference days into Postgres", len(conferenceDays))
		}
		if importTickets {
			if err := importConferenceTickets(ctx, pool, tickets, confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d conference tickets into Postgres", len(tickets))
		}
		var discountIDsByRef map[string]string
		if importDiscounts {
			discountIDsByRef, err = importDiscountRows(ctx, pool, discounts, confTagByRef)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d discounts into Postgres", len(discounts))
		}
		if importPurchases {
			if err := importPurchaseRows(ctx, pool, purchases, confTagByRef, discountIDsByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d purchases into Postgres", len(purchases))
		}
		if importAffiliateUse {
			if err := importAffiliateUsageRows(ctx, pool, affiliateUsages); err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d affiliate usages into Postgres", len(affiliateUsages))
		}
		if importHotels {
			if err := importHotelRows(ctx, pool, hotels, confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d hotels into Postgres", len(hotels))
		}
		if importJobTypes {
			if err := importJobTypeRows(ctx, pool, jobTypes); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d job types into Postgres", len(jobTypes))
		}
		var volunteerIDsByRef map[string]string
		if importVolunteers {
			volunteerIDsByRef, err = importVolunteerRows(ctx, pool, volunteers, confTagByRef, jobTypeTagByRef(jobTypes))
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d volunteers into Postgres", len(volunteerIDsByRef))
		}
		if importVolunteerInfo {
			if err := importVolunteerInfoRows(ctx, pool, volunteerInfo, confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d volunteer info rows into Postgres", len(volunteerInfo))
		}
		if importWorkShifts {
			if err := importWorkShiftRows(ctx, pool, workShifts, confTagByRef, jobTypeTagByRef(jobTypes), volunteerIDsByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d work shifts into Postgres", len(workShifts))
		}
		var orgIDsByRef map[string]string
		if importSponsors {
			orgIDsByRef, err = importOrganizations(ctx, pool, orgs)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d organizations into Postgres", len(orgs))
			if err := importSponsorships(ctx, pool, sponsorships, orgIDsByRef, orgRefByRef(orgs), confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d sponsorships into Postgres", len(sponsorships))
		}
		var speakerIDsByRef map[string]string
		if importSpeakers {
			speakerIDsByRef, err = importSpeakersRows(ctx, pool, speakers)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d speakers into Postgres", len(speakers))
		}
		var proposalIDsByRef map[string]string
		if importProposals {
			proposalIDsByRef, err = importProposalsRows(ctx, pool, proposals)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d proposals into Postgres", len(proposals))
		}
		if importSpeakerConfs {
			if err := importSpeakerConfsRows(ctx, pool, speakerConfs, speakerIDsByRef, orgIDsByRef, proposalIDsByRef, proposalByRef(proposals)); err != nil {
				log.Fatal(err)
			}
			log.Printf("inserted %d speaker confs into Postgres", len(speakerConfs))
		}
		var confTalkIDsByRef map[string]string
		if importConfTalks {
			confTalkIDsByRef, err = importConfTalkRows(ctx, pool, confTalks, proposalIDsByRef)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d conf talks into Postgres", len(confTalks))
		}
		var recordingIDsByRef map[string]string
		if importRecordings {
			recordingIDsByRef, err = importRecordingRows(ctx, pool, recordings, confTalkIDsByRef)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d recordings into Postgres", len(recordings))
		}
		if importSocialPosts {
			if err := importSocialPostRows(ctx, pool, socialPosts, recordingIDsByRef, confTalkIDsByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("upserted %d social posts into Postgres", len(socialPosts))
		}
	}

	if opts.validate {
		if err := validateConferences(ctx, pool, confs); err != nil {
			log.Fatal(err)
		}
		log.Printf("validated conferences count and required tags")
		if importConfDays {
			if err := validateConferenceDays(ctx, pool, conferenceDays); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated conference day count")
		}
		if importTickets {
			if err := validateConferenceTickets(ctx, pool, tickets, confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated conference ticket count and required tiers")
		}
		if importDiscounts {
			if err := validateDiscounts(ctx, pool, discounts); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated discount count and conference links")
		}
		if importPurchases {
			if err := validatePurchases(ctx, pool, purchases); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated purchase count")
		}
		if importAffiliateUse {
			if err := validateAffiliateUsages(ctx, pool, affiliateUsages); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated affiliate usage count")
		}
		if importHotels {
			if err := validateHotels(ctx, pool, hotels); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated hotel count")
		}
		if importJobTypes {
			if err := validateJobTypes(ctx, pool, jobTypes); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated job type count")
		}
		if importVolunteers {
			if err := validateVolunteers(ctx, pool, volunteers); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated volunteer count and links")
		}
		if importVolunteerInfo {
			if err := validateVolunteerInfo(ctx, pool, volunteerInfo); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated volunteer info count")
		}
		if importWorkShifts {
			if err := validateWorkShifts(ctx, pool, workShifts); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated work shift count and volunteer links")
		}
		if importSponsors {
			if err := validateOrganizations(ctx, pool, orgs); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated organization count and required names")
			if err := validateSponsorships(ctx, pool, sponsorships, orgRefByRef(orgs), confTagByRef); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated sponsorship count and conference links")
		}
		if importSpeakers {
			if err := validateSpeakers(ctx, pool, speakers); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated speaker count and roles")
		}
		if importProposals {
			if err := validateProposals(ctx, pool, proposals); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated proposal count and required titles")
		}
		if importSpeakerConfs {
			if err := validateSpeakerConfs(ctx, pool, speakerConfs); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated speaker conf count and proposal links")
		}
		if importConfTalks {
			if err := validateConfTalks(ctx, pool, confTalks); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated conf talk count")
		}
		if importRecordings {
			if err := validateRecordings(ctx, pool, recordings); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated recording count")
		}
		if importSocialPosts {
			if err := validateSocialPosts(ctx, pool, socialPosts); err != nil {
				log.Fatal(err)
			}
			log.Printf("validated social post count")
		}
	}
}

func parseFlags() options {
	var opts options
	flag.StringVar(&opts.configPath, "config", "config.toml", "config.toml path; env-only when the default file is absent")
	flag.StringVar(&opts.databaseURL, "database-url", "", "Postgres connection string; defaults to config DatabaseURL or DATABASE_URL")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "fetch and print planned imports without writing Postgres")
	flag.BoolVar(&opts.reset, "reset", false, "truncate imported tables before writing")
	flag.BoolVar(&opts.validate, "validate", false, "compare imported conference rows against Notion")
	flag.BoolVar(&opts.listConfTalkDups, "list-conf-talk-duplicates", false, "print ConfTalkDb rows that share the same proposal relation and exit")
	flag.BoolVar(&opts.skipConfDays, "skip-conference-days", false, "skip importing conference day schedule metadata")
	flag.BoolVar(&opts.skipTickets, "skip-tickets", false, "skip importing conference ticket tiers")
	flag.BoolVar(&opts.skipDiscounts, "skip-discounts", false, "skip importing discount codes")
	flag.BoolVar(&opts.skipPurchases, "skip-purchases", false, "skip importing purchases")
	flag.BoolVar(&opts.skipAffiliateUse, "skip-affiliate-usages", false, "skip importing affiliate usage ledger rows")
	flag.BoolVar(&opts.skipHotels, "skip-hotels", false, "skip importing hotels")
	flag.BoolVar(&opts.skipJobTypes, "skip-job-types", false, "skip importing volunteer job type catalog")
	flag.BoolVar(&opts.skipVolunteers, "skip-volunteers", false, "skip importing volunteer applications")
	flag.BoolVar(&opts.skipVolunteerInfo, "skip-volunteer-info", false, "skip importing volunteer orientation metadata")
	flag.BoolVar(&opts.skipWorkShifts, "skip-work-shifts", false, "skip importing volunteer work shifts")
	flag.BoolVar(&opts.skipSponsors, "skip-sponsors", false, "skip importing organizations and sponsorships")
	flag.BoolVar(&opts.skipSpeakers, "skip-speakers", false, "skip importing speakers and speaker roles")
	flag.BoolVar(&opts.skipProposals, "skip-proposals", false, "skip importing proposals")
	flag.BoolVar(&opts.skipSpeakerConfs, "skip-speaker-confs", false, "skip importing speaker conference rows and proposal links")
	flag.BoolVar(&opts.skipConfTalks, "skip-conf-talks", false, "skip importing scheduled conference talks")
	flag.BoolVar(&opts.skipRecordings, "skip-recordings", false, "skip importing recording metadata")
	flag.BoolVar(&opts.skipSocialPosts, "skip-social-posts", false, "skip importing social post state")
	flag.Parse()
	return opts
}

func loadConfig(path string) (*types.EnvConfig, error) {
	var env types.EnvConfig
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, &env); err != nil {
				return nil, fmt.Errorf("decode %s: %w", path, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) || path != "config.toml" {
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
	}

	if v := os.Getenv("DATABASE_URL"); v != "" {
		env.DatabaseURL = v
	}
	if v := os.Getenv("NOTION_TOKEN"); v != "" {
		env.Notion.Token = v
	}
	if v := os.Getenv("NOTION_CONFS_DB"); v != "" {
		env.Notion.ConfsDb = v
	}
	if v := os.Getenv("NOTION_CONFSTIX_DB"); v != "" {
		env.Notion.ConfsTixDb = v
	}
	if v := os.Getenv("NOTION_DISCOUNT_DB"); v != "" {
		env.Notion.DiscountsDb = v
	}
	if v := os.Getenv("NOTION_PURCHASES_DB"); v != "" {
		env.Notion.PurchasesDb = v
	}
	if v := os.Getenv("NOTION_ORGS_DB"); v != "" {
		env.Notion.OrgDb = v
	}
	if v := os.Getenv("NOTION_SPONSORSHIPS_DB"); v != "" {
		env.Notion.SponsorshipsDb = v
	}
	if v := os.Getenv("NOTION_SPEAKERS_DB"); v != "" {
		env.Notion.SpeakersDb = v
	}
	if v := os.Getenv("NOTION_PROPOSAL_DB"); v != "" {
		env.Notion.ProposalDb = v
	}
	if v := os.Getenv("NOTION_SPEAKER_CONF_DB"); v != "" {
		env.Notion.SpeakerConfDb = v
	}
	if v := os.Getenv("NOTION_CONFTALK_DB"); v != "" {
		env.Notion.ConfTalkDb = v
	}
	if v := os.Getenv("NOTION_CONFINFO_DB"); v != "" {
		env.Notion.ConfInfoDb = v
	}
	if v := os.Getenv("NOTION_RECORDINGS_DB"); v != "" {
		env.Notion.RecordingsDb = v
	}
	if v := os.Getenv("NOTION_SOCIAL_POSTS_DB"); v != "" {
		env.Notion.SocialPostsDb = v
	}
	if v := os.Getenv("NOTION_AFFILIATE_USE_DB"); v != "" {
		env.Notion.AffiliateUsageDb = v
	}
	if v := os.Getenv("NOTION_HOTEL_DB"); v != "" {
		env.Notion.HotelsDb = v
	}
	if v := os.Getenv("NOTION_JOBTYPE_DB"); v != "" {
		env.Notion.JobTypeDb = v
	}
	if v := os.Getenv("NOTION_VOLUNTEER_DB"); v != "" {
		env.Notion.VolunteerDb = v
	}
	if v := os.Getenv("NOTION_VOLINFO_DB"); v != "" {
		env.Notion.VolInfoDb = v
	}
	if v := os.Getenv("NOTION_SHIFTS_DB"); v != "" {
		env.Notion.ShiftDb = v
	}
	return &env, nil
}

func validateConfig(env *types.EnvConfig, needDB, importConfDays, importTickets, importDiscounts, importPurchases, importAffiliateUse, importHotels, importJobTypes, importVolunteers, importVolunteerInfo, importWorkShifts, importSponsors, importSpeakers, importProposals, importSpeakerConfs, importConfTalks, importRecordings, importSocialPosts bool) error {
	var missing []string
	if strings.TrimSpace(env.Notion.Token) == "" {
		missing = append(missing, "NOTION_TOKEN")
	}
	if strings.TrimSpace(env.Notion.ConfsDb) == "" {
		missing = append(missing, "NOTION_CONFS_DB")
	}
	if importConfDays && strings.TrimSpace(env.Notion.ConfInfoDb) == "" {
		missing = append(missing, "NOTION_CONFINFO_DB")
	}
	if importTickets && strings.TrimSpace(env.Notion.ConfsTixDb) == "" {
		missing = append(missing, "NOTION_CONFSTIX_DB")
	}
	if importDiscounts && strings.TrimSpace(env.Notion.DiscountsDb) == "" {
		missing = append(missing, "NOTION_DISCOUNT_DB")
	}
	if importPurchases && strings.TrimSpace(env.Notion.PurchasesDb) == "" {
		missing = append(missing, "NOTION_PURCHASES_DB")
	}
	if importAffiliateUse && strings.TrimSpace(env.Notion.AffiliateUsageDb) == "" {
		missing = append(missing, "NOTION_AFFILIATE_USE_DB")
	}
	if importHotels && strings.TrimSpace(env.Notion.HotelsDb) == "" {
		missing = append(missing, "NOTION_HOTEL_DB")
	}
	if importJobTypes && strings.TrimSpace(env.Notion.JobTypeDb) == "" {
		missing = append(missing, "NOTION_JOBTYPE_DB")
	}
	if importVolunteers && strings.TrimSpace(env.Notion.VolunteerDb) == "" {
		missing = append(missing, "NOTION_VOLUNTEER_DB")
	}
	if importVolunteerInfo && strings.TrimSpace(env.Notion.VolInfoDb) == "" {
		missing = append(missing, "NOTION_VOLINFO_DB")
	}
	if importWorkShifts && strings.TrimSpace(env.Notion.ShiftDb) == "" {
		missing = append(missing, "NOTION_SHIFTS_DB")
	}
	if importSponsors && strings.TrimSpace(env.Notion.OrgDb) == "" {
		missing = append(missing, "NOTION_ORGS_DB")
	}
	if importSponsors && strings.TrimSpace(env.Notion.SponsorshipsDb) == "" {
		missing = append(missing, "NOTION_SPONSORSHIPS_DB")
	}
	if importSpeakers && strings.TrimSpace(env.Notion.SpeakersDb) == "" {
		missing = append(missing, "NOTION_SPEAKERS_DB")
	}
	if importProposals && strings.TrimSpace(env.Notion.ProposalDb) == "" {
		missing = append(missing, "NOTION_PROPOSAL_DB")
	}
	if importSpeakerConfs && strings.TrimSpace(env.Notion.SpeakerConfDb) == "" {
		missing = append(missing, "NOTION_SPEAKER_CONF_DB")
	}
	if importConfTalks && strings.TrimSpace(env.Notion.ConfTalkDb) == "" {
		missing = append(missing, "NOTION_CONFTALK_DB")
	}
	if importRecordings && strings.TrimSpace(env.Notion.RecordingsDb) == "" {
		missing = append(missing, "NOTION_RECORDINGS_DB")
	}
	if importSocialPosts && strings.TrimSpace(env.Notion.SocialPostsDb) == "" {
		missing = append(missing, "NOTION_SOCIAL_POSTS_DB")
	}
	if needDB && strings.TrimSpace(env.DatabaseURL) == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func conferenceTagByRef(confs []*types.Conf) map[string]string {
	out := make(map[string]string, len(confs))
	for _, conf := range confs {
		if conf == nil || conf.Ref == "" || conf.Tag == "" {
			continue
		}
		out[conf.Ref] = conf.Tag
	}
	return out
}

func resetDatabase(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `TRUNCATE conferences, discounts, purchases, organizations, sponsorships, people, proposals, volunteers, job_types CASCADE`)
	return err
}
