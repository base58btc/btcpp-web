# Getters Backend Map

This document tracks the planned split from Notion-only getters to runtime
dispatchers with Notion and Postgres implementations.

Rules:

- `external/getters/notion.go` has been removed; Notion implementations now
  live in domain-specific `*_notion.go` files.
- Notion API implementations should get a `Notion` suffix when renamed.
- Runtime/cache dispatchers should live outside `notion.go`.
- Postgres implementations should live in domain files named like
  `conferences_postgres.go`.
- During the transition, exported compatibility wrappers may remain when
  command-line migration tools still call the old Notion-shaped signatures.

## Proposed Files

| File | Purpose |
| --- | --- |
| `external/getters/backend.go` | Backend constants, backend selection helper, unsupported-backend error helper. |
| `external/getters/cache.go` | Worker pool, cache bootstrapping, generic cache coordination. |
| `external/getters/conferences.go` | Conference runtime/cache entrypoints and dispatchers. |
| `external/getters/conferences_postgres.go` | Postgres conference reads/writes. |
| `external/getters/speakers.go` | Speaker/person runtime/cache entrypoints and dispatchers. |
| `external/getters/speakers_postgres.go` | Postgres people reads/writes. |
| `external/getters/speaker_confs.go` | Speaker-conf runtime/cache entrypoints and dispatchers. |
| `external/getters/speaker_confs_postgres.go` | Postgres speaker-conf reads. |
| `external/getters/conf_talks.go` | Conf-talk runtime/cache entrypoints, dispatchers, and Talk derivation. |
| `external/getters/conf_talks_postgres.go` | Postgres conf-talk reads. |
| `external/getters/talks.go` | Derived Talk runtime/cache entrypoints and helpers. |
| `external/getters/notion_helpers.go` | Shared direct Notion HTTP helpers for page PATCH/POST workarounds. |
| `external/getters/discounts_postgres.go` | Postgres discount reads/writes. |
| `external/getters/hotels_postgres.go` | Postgres hotel reads/writes. |
| `external/getters/volunteers_postgres.go` | Postgres volunteer, volunteer info, job type, shift reads/writes. |
| `external/getters/registrations_postgres.go` | Postgres registration/ticket/check-in reads/writes. |
| `external/getters/sponsors_postgres.go` | Postgres organization/sponsorship reads/writes. |
| `external/getters/affiliate_postgres.go` | Postgres affiliate usage reads/writes. |
| `external/getters/socialposts_postgres.go` | Postgres social post reads/writes. |
| `external/getters/missives_postgres.go` | Postgres missive/subscriber reads/writes. |

## Current Progress: Cache

- Shared cache state, `JobType`, work-pool startup, cache queueing,
  `WaitFetch`, job dispatch, disk-cache bootstrap, refresh callbacks, and
  `CacheStats` moved to `external/getters/cache.go`.
- `external/getters/notion.go` has been removed.

## Current Progress: Notion Splits

- The remaining `notion.go` functions were split into:
  `conferences_notion.go`, `speakers_notion.go`, `hotels_notion.go`,
  `job_types_notion.go`, `discounts_notion.go`, `registrations_notion.go`,
  and `uploads_notion.go`.
- Calendar notification writes now live with their domain:
  `TalkUpdateCalNotif` in `conf_talks_notion.go`,
  `ShiftUpdateCalNotif` in `work_shifts_notion.go`, and
  `ConfUpdateOrientCalNotif` in `conferences_notion.go`.

## `notion.go` Function Map

### Runtime / Cache Infrastructure

These are not Notion implementations. They should move out of `notion.go`
without gaining a `Notion` suffix.

| Current function | Future location | Notes |
| --- | --- | --- |
| `queueRefresh` | `cache.go` | Shared cache queue helper. |
| `OnTalksRefresh` | `cache.go` or `talks.go` | Runtime callback registration. |
| `OnSpeakersRefresh` | `cache.go` or `speakers.go` | Runtime callback registration. |
| `StartWorkPool` | `cache.go` | Runtime worker pool. |
| `CloseWorkPool` | `cache.go` | Runtime worker pool. |
| `loadFromCache` | `cache.go` | Disk cache bootstrap for legacy caches. |
| `WaitFetch` | `cache.go` | Runtime cache warmup. |
| `runJob` | `cache.go` | Runtime cache job dispatch. |
| `workers` | `cache.go` | Runtime cache worker loop. |
| `CacheStats` | `cache.go` | Runtime debug stats. |
| `InvalidateProposalsCache` | `talks.go` | Runtime cache invalidation. |
| `InvalidateSpeakerConfsCache` | `talks.go` | Runtime cache invalidation. |
| `CacheSpeakerInsert` | `speakers.go` | Runtime cache mutation after write. |
| `CacheSpeakerConfInsert` | `talks.go` | Runtime cache mutation after write. |
| `CacheSpeakerByID` | `speakers.go` | Runtime cache lookup. |
| `InvalidateConfTalksCache` | `talks.go` | Runtime cache invalidation. |
| `FetchRecordingByConfTalk` | `recordings.go` | Runtime cache lookup. |
| `FetchYTLinkForTalk` | `recordings.go` or `talks.go` | Runtime derived lookup. |
| `cacheRecordingsWarm` | `recordings.go` | Runtime cache state helper. |
| `cacheConfTalksWarm` | `talks.go` | Runtime cache state helper. |
| `InvalidateRecordingsCache` | `recordings.go` | Runtime cache invalidation. |
| `FetchSiteStats` | `runtime.go` or `site_stats.go` | Runtime cached aggregate. |
| `InvalidateTalksCache` | `talks.go` | Runtime cache invalidation. |
| `patchTalksStatusForProposal` | `talks.go` | Runtime cache patch helper. |
| `FetchConfsCached` | `conferences.go` | App-facing runtime getter. Dispatches by backend. |
| `FetchSpeakersCached` | `speakers.go` | App-facing runtime getter. Dispatches by backend. |
| `FetchTalksCached` | `talks.go` | App-facing runtime getter. Dispatches by backend. |
| `FetchDiscountsCached` | `discounts.go` | App-facing runtime getter. Dispatches by backend. |
| `FetchHotelsCached` | `hotels.go` | App-facing runtime getter. Dispatches by backend. |
| `FetchJobsCached` | `volunteers.go` | App-facing runtime getter. Dispatches by backend. |
| `FetchShiftsCached` | `volunteers.go` | App-facing runtime getter. Dispatches by backend. |
| `FetchOrgsCached` | `sponsors.go` | App-facing runtime getter. Dispatches by backend. |

### Runtime Job Loaders

These should move out of `notion.go`. Each should dispatch to a backend-specific
implementation and update the existing runtime cache.

| Current function | Future location | Notion call target | Postgres call target |
| --- | --- | --- | --- |
| `getProposals` | `talks.go` | `ListProposalsNotion` | `listProposalsPostgres` |
| `getSpeakerConfs` | `talks.go` | `ListSpeakerConfsNotion` | `listSpeakerConfsPostgres` |
| `getConfTalks` | `talks.go` | `ListConfTalksNotion` | `listConfTalksPostgres` |
| `getRecordings` | `recordings.go` | `ListRecordingsNotion` | `listRecordingsPostgres` |
| `getSiteStats` | `site_stats.go` | `getSiteStatsNotion` | `getSiteStatsPostgres` |
| `getConfs` | `conferences.go` | `ListConferencesNotion` | `listConferencesPostgres` |
| `getSpeakers` | `speakers.go` | `ListSpeakersNotion` | `listSpeakersPostgres` |
| `getTalks` | `talks.go` | `listTalksNotion` | `listTalksPostgres` |
| `getDiscounts` | `discounts.go` | `ListDiscountsNotion` | `listDiscountsPostgres` |
| `getHotels` | `hotels.go` | `ListHotelsNotion` | `listHotelsPostgres` |
| `getJobs` | `volunteers.go` | `ListJobsNotion` | `listJobsPostgres` |
| `getShifts` | `volunteers.go` | `ListWorkShiftsNotion` | `listWorkShiftsPostgres` |
| `getOrgs` | `sponsors.go` | `ListOrgsNotion` | `listOrgsPostgres` |

### Notion Implementations

These functions directly read from or write to Notion. Their Notion
implementation names should gain a `Notion` suffix. App-facing wrappers can
remain under the current names until callers are moved to runtime dispatchers.

| Current function | Future Notion name | Future runtime/domain file | Future Postgres name |
| --- | --- | --- | --- |
| `ListConfTickets` | `ListConfTicketsNotion` | `conferences.go` | `listConfTicketsPostgres` |
| `ListConferences` | `ListConferencesNotion` | `conferences.go` | `listConferencesPostgres` |
| `ListConferencesOnly` | `ListConferencesOnlyNotion` | `conferences.go` | `listConferencesOnlyPostgres` |
| `TalkUpdateCalNotif` | `TalkUpdateCalNotifNotion` | `talks.go` | `talkUpdateCalNotifPostgres` |
| `ShiftUpdateCalNotif` | `ShiftUpdateCalNotifNotion` | `volunteers.go` | `shiftUpdateCalNotifPostgres` |
| `ConfUpdateOrientCalNotif` | `ConfUpdateOrientCalNotifNotion` | `conferences.go` | `confUpdateOrientCalNotifPostgres` |
| `ListSpeakers` | `ListSpeakersNotion` | `speakers.go` | `listSpeakersPostgres` |
| `listTalks` | `listTalksNotion` | `talks.go` | `listTalksPostgres` |
| `ListHotels` | `ListHotelsNotion` | `hotels.go` | `listHotelsPostgres` |
| `ListJobs` | `ListJobsNotion` | `volunteers.go` | `listJobsPostgres` |
| `ListWorkShifts` | `ListWorkShiftsNotion` | `volunteers.go` | `listWorkShiftsPostgres` |
| `buildShiftPropertiesJSON` | `buildShiftPropertiesJSONNotion` | `volunteers.go` | N/A |
| `notionPagePost` | `notionPagePost` | Stays Notion helper | N/A |
| `CreateShift` | `CreateShiftNotion` | `volunteers.go` | `createShiftPostgres` |
| `UpdateShiftTimes` | `UpdateShiftTimesNotion` | `volunteers.go` | `updateShiftTimesPostgres` |
| `UpdateShift` | `UpdateShiftNotion` | `volunteers.go` | `updateShiftPostgres` |
| `AssignVolunteerToShift` | `AssignVolunteerToShiftNotion` | `volunteers.go` | `assignVolunteerToShiftPostgres` |
| `RemoveVolunteerFromShift` | `RemoveVolunteerFromShiftNotion` | `volunteers.go` | `removeVolunteerFromShiftPostgres` |
| `clearRelationProperty` | `clearRelationPropertyNotion` | `volunteers.go` | N/A |
| `UpdateVolunteerStatus` | `UpdateVolunteerStatusNotion` | `volunteers.go` | `updateVolunteerStatusPostgres` |
| `UpdateVolunteerAvailability` | `UpdateVolunteerAvailabilityNotion` | `volunteers.go` | `updateVolunteerAvailabilityPostgres` |
| `UpdateVolunteerWorkPrefs` | `UpdateVolunteerWorkPrefsNotion` | `volunteers.go` | `updateVolunteerWorkPrefsPostgres` |
| `ListDiscounts` | `ListDiscountsNotion` | `discounts.go` | `listDiscountsPostgres` |
| `IncrementDiscountUses` | `IncrementDiscountUsesNotion` | `discounts.go` | `incrementDiscountUsesPostgres` |
| `CheckIn` | `CheckInNotion` | `registrations.go` | `checkInPostgres` |
| `SoldTixCount` | `SoldTixCountNotion` | `registrations.go` | `soldTixCountPostgres` |
| `LookupTicketPages` | `LookupTicketPagesNotion` | `registrations.go` | N/A |
| `RefTicketPages` | `RefTicketPagesNotion` | `registrations.go` | N/A |
| `TicketPages` | `TicketPagesNotion` | `registrations.go` | N/A |
| `ToggleTicketBlock` | `ToggleTicketBlockNotion` | `registrations.go` | `toggleTicketBlockPostgres` |
| `RevokeTicket` | `RevokeTicketNotion` | `registrations.go` | `revokeTicketPostgres` |
| `AddTickets` | `AddTicketsNotion` | `registrations.go` | `addTicketsPostgres` |
| `RegisterVolunteer` | `RegisterVolunteerNotion` | `volunteers.go` | `registerVolunteerPostgres` |
| `normalizeVolunteerInput` | `normalizeVolunteerInput` | Shared helper | Shared helper |
| `ListConfInfos` | `ListConfInfosNotion` | `conferences.go` | `listConfInfosPostgres` |
| `GetVolInfos` | `GetVolInfosNotion` | `volunteers.go` | `getVolInfosPostgres` |
| `ListVolunteerApps` | `ListVolunteerAppsNotion` | `volunteers.go` | `listVolunteerAppsPostgres` |
| `FetchVolunteer` | `FetchVolunteerNotion` | `volunteers.go` | `fetchVolunteerPostgres` |
| `ListVolunteersForConf` | `ListVolunteersForConfNotion` | `volunteers.go` | `listVolunteersForConfPostgres` |
| `UploadFile` | `UploadFileNotion` | `uploads.go` | `uploadFilePostgres` |

### App-Facing Functions That Need Case-By-Case Review

These currently accept `*config.AppContext`, but may mix cache, Notion reads,
and app logic. They should become runtime dispatchers or move to a clearer
domain file after the backing Notion function is identified.

| Current function | Likely domain | Notes |
| --- | --- | --- |
| `FetchProposalByID` | Talks/proposals | Cache lookup with Notion page fallback today. |
| `FetchSpeakerConfsForSpeaker` | Talks/speaker confs | Cache-backed. |
| `FetchSpeakerConfByID` | Talks/speaker confs | Cache-backed. |
| `FetchConfTalkByProposal` | Talks/conf talks | Cache-backed. |
| `FetchConfTalkByID` | Talks/conf talks | Cache-backed. |
| `GetTalksFor` | Talks | Currently derived from talks cache. |
| `GetTalk` | Talks | Currently derived from talks cache. |
| `GetShiftsForConf` | Volunteers/shifts | Currently derived from shifts cache. |
| `FindDiscount` | Discounts | Currently cache-backed lookup. |
| `CalcDiscount` | Discounts | Business logic over cached discounts. |
| `SoldTixCached` | Registrations | Cache/derived count. |
| `UpdateSoldTix` | Registrations/conferences | Runtime cache patch plus count refresh. |
| `FetchRegistrations` | Registrations | Runtime read. |
| `ListRegistrationsByEmail` | Registrations | Runtime read. |
| `EmailHasRegistration` | Registrations | Runtime read. |
| `ticketMatch` | Registrations | Shared helper. |
| `checkActive` | Conferences/registrations | Shared helper over conference cache. |
| `FetchRegistrationsConf` | Registrations | Runtime read wrapper. |
| `FetchBtcppRegistrations` | Registrations | Runtime read wrapper. |
| `GetVolInfo` | Volunteers | Runtime read. |
| `GetVolInfoMap` | Volunteers | Runtime read. |
| `GetConfInfoMap` | Conferences | Runtime read. |

## First Slice: Conferences

The first backend split should cover conferences only.

Target files:

- `external/getters/conferences.go`
- `external/getters/conferences_postgres.go`

Current Notion methods in `notion.go` to rename when that step is done:

- `ListConfTickets` -> `ListConfTicketsNotion`
- `ListConferences` -> `ListConferencesNotion`
- `ListConferencesOnly` -> `ListConferencesOnlyNotion`
- `ConfUpdateOrientCalNotif` -> `ConfUpdateOrientCalNotifNotion`
- `ListConfInfos` -> `ListConfInfosNotion`

Runtime/cache methods to move out of `notion.go` for the conference slice:

- `getConfs`
- `FetchConfsCached`
- `GetConfInfoMap`

Initial Postgres methods:

- `listConfTicketsPostgres`
- `listConferencesPostgres`
- `listConferencesOnlyPostgres`
- `listConfInfosPostgres`

The exported app-facing names should stay stable for handlers while the
backend-specific implementations split underneath them.

Current progress:

- `getConfs` moved to `external/getters/conferences.go`.
- `FetchConfsCached` moved to `external/getters/conferences.go`.
- `ListConfTickets` remains as a compatibility wrapper for Notion-shaped callers.
- `ListConferences` remains as a compatibility wrapper for Notion-shaped callers.
- `ListConferencesOnly` remains as a compatibility wrapper for Notion-shaped callers.
- `ListConfTicketsNotion`, `ListConferencesNotion`, and
  `ListConferencesOnlyNotion` are the renamed Notion implementations.
- `listConfTicketsPostgres`, `listConferencesPostgres`, and
  `listConferencesOnlyPostgres` are implemented.
- `ConfUpdateOrientCalNotif` and `ListConfInfos` are not split yet.

## Current Progress: Hotels

- `getHotels` moved to `external/getters/hotels.go`.
- `FetchHotelsCached` moved to `external/getters/hotels.go`.
- `ListHotels` remains as a compatibility wrapper for Notion-shaped callers.
- `ListHotelsNotion` is the renamed Notion implementation.
- `listHotelsPostgres` is implemented in `external/getters/hotels_postgres.go`.

## Current Progress: Discounts

- `getDiscounts` moved to `external/getters/discounts.go`.
- `FetchDiscountsCached` moved to `external/getters/discounts.go`.
- `FindDiscount` moved to `external/getters/discounts.go`.
- `CalcDiscount` moved to `external/getters/discounts.go`.
- `IncrementDiscountUses` moved to `external/getters/discounts.go` and dispatches by backend.
- `ListDiscounts` remains as a compatibility wrapper for Notion-shaped callers.
- `ListDiscountsNotion` is the renamed Notion implementation.
- `listDiscountsPostgres` and `incrementDiscountUsesPostgres` are implemented in
  `external/getters/discounts_postgres.go`.

## Current Progress: Job Types

- `getJobs` moved to `external/getters/job_types.go`.
- `FetchJobsCached` moved to `external/getters/job_types.go`.
- `ListJobs` remains as a compatibility wrapper for Notion-shaped callers.
- `ListJobsNotion` is the renamed Notion implementation.
- `listJobsPostgres` is implemented in `external/getters/job_types_postgres.go`.

## Current Progress: Work Shifts

- `getShifts` moved to `external/getters/work_shifts.go`.
- `FetchShiftsCached` moved to `external/getters/work_shifts.go`.
- `GetShiftsForConf` moved to `external/getters/work_shifts.go`.
- `ListWorkShifts` remains as a context-based app-facing wrapper.
- `CreateShift`, `UpdateShift`, `UpdateShiftTimes`, `AssignVolunteerToShift`,
  and `RemoveVolunteerFromShift` now dispatch by backend.
- `ListWorkShiftsNotion` and shift write Notion implementations moved to
  `external/getters/work_shifts_notion.go`.
- Shared direct Notion page helpers moved to
  `external/getters/notion_helpers.go`.
- `listWorkShiftsPostgres` and shift write implementations are in
  `external/getters/work_shifts_postgres.go`.

## Current Progress: Volunteers

- `GetVolInfo`, `GetVolInfoMap`, `GetVolInfos`, `ListVolunteerApps`,
  `FetchVolunteer`, and `ListVolunteersForConf` moved to
  `external/getters/volunteers.go`.
- `GetVolInfosNotion`, `ListVolunteerAppsNotion`, `FetchVolunteerNotion`,
  `ListVolunteersForConfNotion`, `RegisterVolunteer`, and volunteer update
  helpers moved to `external/getters/volunteers_notion.go`.
- `getVolInfosPostgres`, `listVolunteerAppsPostgres`, `fetchVolunteerPostgres`,
  and `listVolunteersForConfPostgres` are implemented in
  `external/getters/volunteers_postgres.go`.
- `RegisterVolunteer`, `UpdateVolunteerStatus`, `UpdateVolunteerAvailability`,
  and `UpdateVolunteerWorkPrefs` now dispatch by backend.

## Current Progress: Sponsors And Organizations

- `getOrgs` moved to `external/getters/sponsors.go`.
- `FetchOrgsCached` moved to `external/getters/sponsors.go`.
- `ListOrgs`, `GetOrg`, and `ListSponsorshipsOnly` remain as compatibility
  wrappers for Notion-shaped callers.
- `ListOrgsNotion`, `GetOrgNotion`, `ListSponsorshipsNotion`, and
  `ListSponsorshipsOnlyNotion` are the renamed Notion implementations.
- `ListSponsorships` now dispatches by backend.
- `listOrgsPostgres` and `listSponsorshipsPostgres` are implemented in
  `external/getters/sponsors_postgres.go`.
- Organization/sponsorship write paths still need a dedicated Postgres write split.

## Current Progress: Proposals

- `getProposals`, `FetchProposalsCached`, and `FetchProposalByID` moved to
  `external/getters/proposals.go`.
- `ListProposals` now dispatches by backend.
- `ListProposalsNotion` and `ListProposalsOnlyNotion` are the renamed Notion
  implementations.
- `ListProposalsOnly` remains as a compatibility wrapper for Notion-shaped callers.
- `listProposalsPostgres` and `getProposalPostgres` are implemented in
  `external/getters/proposals_postgres.go`.
- Proposal write paths (`CreateProposal`, `UpdateProposal`,
  `UpdateProposalStatus`, invite-token updates, etc.) still need a dedicated
  Postgres write split.

## Current Progress: Speakers / People

- `getSpeakers` and `FetchSpeakersCached` moved to
  `external/getters/speakers.go`.
- `ListSpeakers` remains as a compatibility wrapper for Notion-shaped callers.
- `ListSpeakersNotion` is the renamed Notion implementation.
- `listSpeakersPostgres` is implemented in `external/getters/speakers_postgres.go`.
- Postgres reads the `people` table and hydrates `Speaker.Roles` from
  `people_roles` as the existing raw role tag format, e.g. `global-admin`.
- Speaker write paths (`CreateSpeaker`, `UpdateSpeaker`, `UpdateSpeakerRoles`,
  etc.) still need a dedicated Postgres write split.

## Current Progress: Speaker Confs

- `getSpeakerConfs`, `FetchSpeakerConfsForSpeaker`, `FetchSpeakerConfByID`,
  `FetchSpeakerConfWithSpeaker`, `GetSpeakerConfsByEmail`,
  `InvalidateSpeakerConfsCache`, and `CacheSpeakerConfInsert` moved to
  `external/getters/speaker_confs.go`.
- `ListSpeakerConfs` now dispatches by backend.
- `ListSpeakerConfsNotion` is the renamed Notion implementation.
- `listSpeakerConfsPostgres` is implemented in
  `external/getters/speaker_confs_postgres.go` and hydrates speakers,
  proposals, and `OtherEvents` from `proposals_speaker_confs` and
  `speaker_confs_conferences`.
- Speaker-conf write paths (`UpsertSpeakerConf`, `UpdateSpeakerConf`, date
  stamps, proposal add/remove, etc.) still need a dedicated Postgres write
  split.

## Current Progress: Conf Talks

- `getConfTalks`, `FetchConfTalkByProposal`, `FetchConfTalkByID`,
  `InvalidateConfTalksCache`, `cacheConfTalksWarm`, `GetConfTalkByProposal`,
  `LoadTalkFromConfTalk`, `LoadTalksFromConfTalks`, `resolveProposalSpeakers`,
  and `talkFromConfTalk` moved to `external/getters/conf_talks.go`.
- `ListConfTalks` now dispatches by backend.
- The Notion implementations live in `external/getters/conf_talks_notion.go`.
- `listConfTalksPostgres`, `getConfTalkByProposalPostgres`, and
  `loadTalkFromConfTalkPostgres` are implemented in
  `external/getters/conf_talks_postgres.go` and read `conf_talks`.
- Conf-talk write paths (`CreateConfTalk`, schedule updates, archive/delete,
  clipart/social-card updates) still need a dedicated Postgres write split.

## Current Progress: Talks

- `getTalks`, `FetchTalksCached`, `InvalidateTalksCache`,
  `patchTalksStatusForProposal`, `listTalks`, `GetTalksFor`, and `GetTalk`
  moved to `external/getters/talks.go`.
- Talk reads are derived from `conf_talks`, `proposals`, and
  `speaker_confs`; they use the backend-specific readers behind those
  dispatchers instead of a separate Postgres `talks` table.

## Current Progress: Affiliate Usage

- `RecordAffiliateUsage`, `ListAffiliateUsage`, `UpdateAffiliateUsageSats`,
  `QueryAffiliateUsageByEmail`, and `QueryAffiliateUsageByConf` now dispatch by
  backend.
- The renamed Notion implementations are `RecordAffiliateUsageNotion`,
  `ListAffiliateUsageNotion`, `UpdateAffiliateUsageSatsNotion`,
  `QueryAffiliateUsageByEmailNotion`, and `QueryAffiliateUsageByConfNotion`.
- The Postgres implementation lives in `external/getters/affiliate_postgres.go`
  and reads/writes `affiliate_usages`.
- Affiliate discount-code management (`CreateAffiliateCode`, `UpdateAffiliateCode`,
  `ArchiveAffiliateCode`) still writes Notion via the existing discounts flow.

## Current Progress: Registrations

- `CheckIn`, `SoldTixCount`, `FetchRegistrations`, and
  `ListRegistrationsByEmail` now dispatch by backend.
- `SoldTixCached`, `UpdateSoldTix`, `EmailHasRegistration`,
  `FetchRegistrationsConf`, and `FetchBtcppRegistrations` moved to
  `external/getters/registrations.go`.
- The renamed Notion implementations plus Notion-only ticket write helpers
  live in `external/getters/registrations_notion.go`.
- The Postgres implementation lives in
  `external/getters/registrations_postgres.go` and reads/writes
  `registrations`.
- Ticket creation/revoke paths (`AddTickets`, `RevokeTicket`,
  `ToggleTicketBlock`, etc.) still need a dedicated Postgres write split.

## Current Progress: Conference Info

- `ListConfInfos` and `GetConfInfoMap` moved to
  `external/getters/conf_infos.go`.
- `ListConfInfosNotion` is the renamed Notion implementation.
- `listConfInfosPostgres` is implemented in
  `external/getters/conf_infos_postgres.go` and reads `conference_days`.

## Current Progress: Recordings

- `getRecordings`, `FetchRecordingByConfTalk`, `FetchYTLinkForTalk`,
  `InvalidateRecordingsCache`, and recording cache helpers moved to
  `external/getters/recordings.go`.
- `ListRecordings`, `GetRecordingByConfTalk`, `UpdateRecordingYTLink`,
  `UpdateRecordingXLink`, `UpdateRecordingPublishAt`,
  `UpdateRecordingFileURI`, and `UpdateRecordingPublishing` now dispatch by
  backend.
- The renamed Notion implementations live in
  `external/getters/recordings_notion.go` with `Notion`/`notion` suffixes.
- The Postgres implementation lives in
  `external/getters/recordings_postgres.go` and reads/writes `recordings`.

## Current Progress: Site Stats

- `getSiteStats`, `FetchSiteStats`, and `SiteStatsValues` moved to
  `external/getters/site_stats.go`.
- Notion attendee counting lives in `external/getters/site_stats_notion.go`.
- Postgres attendee counting lives in `external/getters/site_stats_postgres.go`
  and counts rows from `registrations`.

## Current Progress: Social Posts

- `ListPostedRefs`, `RecordSocialPost`, `ListSocialPosts`, and
  `UpsertSocialPost` now dispatch by backend.
- Shared cache and update helpers remain in `external/getters/socialposts.go`.
- Notion implementations live in `external/getters/socialposts_notion.go`.
- Postgres implementations live in `external/getters/socialposts_postgres.go`
  and read/write `social_posts`.
