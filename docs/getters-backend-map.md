# Getters Backend Map

This document tracks the planned split from Notion-only getters to runtime
dispatchers with Notion and Postgres implementations.

Rules:

- `external/getters/notion.go` should remain Notion-only while it exists.
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
| `external/getters/runtime.go` | Worker pool, cache bootstrapping, generic runtime cache coordination. |
| `external/getters/conferences.go` | Conference runtime/cache entrypoints and dispatchers. |
| `external/getters/conferences_postgres.go` | Postgres conference reads/writes. |
| `external/getters/speakers.go` | Speaker/person runtime/cache entrypoints and dispatchers. |
| `external/getters/speakers_postgres.go` | Postgres people reads/writes. |
| `external/getters/talks.go` | Talk, proposal, speaker-conf, conf-talk runtime entrypoints and dispatchers. |
| `external/getters/talks_postgres.go` | Postgres talk/proposal/speaker-conf/conf-talk reads/writes. |
| `external/getters/discounts_postgres.go` | Postgres discount reads/writes. |
| `external/getters/hotels_postgres.go` | Postgres hotel reads/writes. |
| `external/getters/volunteers_postgres.go` | Postgres volunteer, volunteer info, job type, shift reads/writes. |
| `external/getters/purchases_postgres.go` | Postgres purchase/ticket/check-in reads/writes. |
| `external/getters/sponsors_postgres.go` | Postgres organization/sponsorship reads/writes. |
| `external/getters/affiliate_postgres.go` | Postgres affiliate usage reads/writes. |
| `external/getters/socialposts_postgres.go` | Postgres social post reads/writes. |
| `external/getters/missives_postgres.go` | Future Postgres missive/subscriber reads/writes; paused for now. |

## `notion.go` Function Map

### Runtime / Cache Infrastructure

These are not Notion implementations. They should move out of `notion.go`
without gaining a `Notion` suffix.

| Current function | Future location | Notes |
| --- | --- | --- |
| `queueRefresh` | `runtime.go` | Shared cache queue helper. |
| `OnTalksRefresh` | `runtime.go` or `talks.go` | Runtime callback registration. |
| `OnSpeakersRefresh` | `runtime.go` or `speakers.go` | Runtime callback registration. |
| `StartWorkPool` | `runtime.go` | Runtime worker pool. |
| `CloseWorkPool` | `runtime.go` | Runtime worker pool. |
| `loadFromCache` | `runtime.go` | Disk cache bootstrap for legacy caches. |
| `WaitFetch` | `runtime.go` | Runtime cache warmup. |
| `runJob` | `runtime.go` | Runtime cache job dispatch. |
| `workers` | `runtime.go` | Runtime cache worker loop. |
| `CacheStats` | `runtime.go` | Runtime debug stats. |
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
| `CheckIn` | `CheckInNotion` | `purchases.go` | `checkInPostgres` |
| `SoldTixCount` | `SoldTixCountNotion` | `purchases.go` | `soldTixCountPostgres` |
| `LookupTicketPages` | `LookupTicketPagesNotion` | `purchases.go` | N/A |
| `RefTicketPages` | `RefTicketPagesNotion` | `purchases.go` | N/A |
| `TicketPages` | `TicketPagesNotion` | `purchases.go` | N/A |
| `ToggleTicketBlock` | `ToggleTicketBlockNotion` | `purchases.go` | `toggleTicketBlockPostgres` |
| `RevokeTicket` | `RevokeTicketNotion` | `purchases.go` | `revokeTicketPostgres` |
| `AddTickets` | `AddTicketsNotion` | `purchases.go` | `addTicketsPostgres` |
| `RegisterVolunteer` | `RegisterVolunteerNotion` | `volunteers.go` | `registerVolunteerPostgres` |
| `normalizeVolunteerInput` | `normalizeVolunteerInput` | Shared helper | Shared helper |
| `ListConfInfos` | `ListConfInfosNotion` | `conferences.go` | `listConfInfosPostgres` |
| `GetVolInfos` | `GetVolInfosNotion` | `volunteers.go` | `getVolInfosPostgres` |
| `ListVolunteerApps` | `ListVolunteerAppsNotion` | `volunteers.go` | `listVolunteerAppsPostgres` |
| `FetchVolunteer` | `FetchVolunteerNotion` | `volunteers.go` | `fetchVolunteerPostgres` |
| `ListVolunteersForConf` | `ListVolunteersForConfNotion` | `volunteers.go` | `listVolunteersForConfPostgres` |
| `UploadFile` | `UploadFileNotion` | `files.go` | No direct Postgres equivalent; probably storage-backed. |

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
| `SoldTixCached` | Purchases | Cache/derived count. |
| `UpdateSoldTix` | Purchases/conferences | Runtime cache patch plus count refresh. |
| `FetchRegistrations` | Purchases | Runtime read. |
| `ListRegistrationsByEmail` | Purchases | Runtime read. |
| `EmailHasRegistration` | Purchases | Runtime read. |
| `ticketMatch` | Purchases | Shared helper. |
| `checkActive` | Conferences/purchases | Shared helper over conference cache. |
| `FetchRegistrationsConf` | Purchases | Runtime read wrapper. |
| `FetchBtcppRegistrations` | Purchases | Runtime read wrapper. |
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
- `ListWorkShiftsNotion` is the renamed Notion implementation.
- `listWorkShiftsPostgres` is implemented in `external/getters/work_shifts_postgres.go`.
- Shift write paths (`CreateShift`, `UpdateShift`, assignment helpers, etc.) still need a
  dedicated Postgres write split.

## Current Progress: Volunteers

- `GetVolInfo`, `GetVolInfoMap`, `GetVolInfos`, `ListVolunteerApps`,
  `FetchVolunteer`, and `ListVolunteersForConf` moved to
  `external/getters/volunteers.go`.
- `GetVolInfosNotion`, `ListVolunteerAppsNotion`, `FetchVolunteerNotion`, and
  `ListVolunteersForConfNotion` are the renamed Notion implementations.
- `getVolInfosPostgres`, `listVolunteerAppsPostgres`, `fetchVolunteerPostgres`,
  and `listVolunteersForConfPostgres` are implemented in
  `external/getters/volunteers_postgres.go`.
- Volunteer write paths (`RegisterVolunteer`, status/availability/work preference
  updates, etc.) still need a dedicated Postgres write split.

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
