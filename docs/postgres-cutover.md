# Postgres Cutover Checklist

This tracks what remains before the app can run production traffic with
`dataBackend = "postgres"` without relying on Notion for normal app workflows.

## Current State

- Read getters for migrated domains dispatch through `AppContext` and can use
  Postgres.
- `external/getters/notion.go` has been removed; Notion implementations now
  live in domain-specific files.
- Migration and one-off maintenance commands under `cmd/` can remain
  Notion-specific unless they are needed after cutover.

## Highest-Priority Write Paths

These are user-facing production writes and should get Postgres implementations
before cutover.

| Domain | Current Notion entrypoints | Why it matters |
| --- | --- | --- |
| Proposals / speaker confs / conf talks | `CreateProposal`, `UpdateProposal`, `UpdateProposalStatus`, `UpsertSpeakerConf`, `UpdateSpeakerConf`, `CreateConfTalk`, `UpdateConfTalkSchedule`, `DeleteConfTalk`, `AddSpeakerConfToProposal`, `RemoveProposalFromSpeakerConf`, speaker-conf date stamps | CFP/admin scheduling/dashboard workflows. |
| Volunteers / shifts | `RegisterVolunteer`, `UpdateVolunteerStatus`, `UpdateVolunteerAvailability`, `UpdateVolunteerWorkPrefs`, `CreateShift`, `UpdateShift`, `UpdateShiftTimes`, `AssignVolunteerToShift`, `RemoveVolunteerFromShift` | Volunteer signup, coordinator/admin shift management. |
| Sponsors / organizations | `RegisterOrg`, `UpdateOrg`, `UpdateOrgDetails`, `RegisterSponsorship`, `UpdateSponsorshipStatus`, `FindOrg`, `SearchOrgsByName` | Sponsor dashboard and talk-submit organization lookup/create. |
| Discounts / affiliates | `CreateDiscount`, `UpdateDiscount`, `ArchiveDiscount`, `CreateAffiliateCode`, `UpdateAffiliateCode`, `ArchiveAffiliateCode` | Admin discount management and affiliate code lifecycle. |

## Medium-Priority Write Paths

These are important admin/media workflows, but less central than ticketing,
speaker/proposal, and volunteer writes.

| Domain | Current Notion entrypoints | Notes |
| --- | --- | --- |
| Hotels | `CreateHotel`, `UpdateHotel`, `ArchiveHotel` | Admin hotel management. |
| Calendar notification stamps | `TalkUpdateCalNotif`, `ShiftUpdateCalNotif`, `ConfUpdateOrientCalNotif` | Prevents duplicate calendar sends; should be durable in Postgres. |
| Conf talk media fields | `ConfTalkSetSocialCard`, `ConfTalkSetClipart` | Used by media/card/clipart flows. |
| File upload | `UploadFile` | This is Notion file-upload specific; likely needs a separate storage-backed replacement rather than Postgres. |

## Completed Write Splits

| Domain | Completed entrypoints | Notes |
| --- | --- | --- |
| Registrations / tickets | `AddTickets`, `RevokeTicket` | Now dispatch by `AppContext`; Postgres writes insert/update `registrations`, and Notion remains the fallback. |
| Speakers / people | `CreateSpeaker`, `UpdateSpeaker`, `UpdateSpeakerRoles`, `GetSpeakersByEmail`, `FetchSpeakerByID` | Now dispatch by `AppContext`; Postgres writes `people` and `people_roles`, and Notion remains the fallback. |
| Missives / subscribers | `FindSubscriber`, `ListSubscribersFor`, `IsSubscribedTo`, `SubscribeEmailList`, `SubscribeEmail`, `UpdateSubs`, `GetLetter`, `GetLetterFor`, `GetLetters`, `ListOnlyForLetters`, `ListTemplatedLetters`, `CreateTemplatedMissive`, `UpdateTemplatedMissive`, `CreateMissive`, `MarkLetterSent` | Now dispatch by `AppContext`; Postgres reads/writes `subscribers`, `subscriber_subscriptions`, and `missives`, and Notion remains the fallback. |
| Proposal core writes | `CreateProposal`, `UpdateProposal`, `UpdateProposalStatus`, `SetProposalInviteToken` | Now dispatch by `AppContext`; Postgres writes `proposals`, and Notion remains the fallback. |
| Conf talk writes | `CreateConfTalk`, `UpdateConfTalkSchedule`, `DeleteConfTalk`, `ConfTalkSetSocialCard`, `ConfTalkSetClipart`, `TalkUpdateCalNotif` | Now dispatch by `AppContext`; Postgres writes `conf_talks`, and Notion remains the fallback. |

## Paused / Separate Work

| Domain | Status |
| --- | --- |
| Migration and backfill commands | `cmd/migrate-*`, `cmd/backfill-*`, and similar tools can stay Notion-specific unless needed after production cutover. |

## Suggested Implementation Order

1. Registrations/tickets writes, because checkout and check-in are core production flows.
2. Speaker/person writes plus speaker dashboard lookups, because many proposal flows still pass `ctx.Notion` directly.
3. Proposal/speaker-conf/conf-talk writes, because they are tightly coupled to speaker flows.
4. Volunteer and work-shift writes.
5. Sponsor/org writes.
6. Discount/affiliate and hotel admin writes.
7. Calendar/media/file-upload replacements.

For each domain, prefer changing exported app-facing functions to accept
`*config.AppContext` and dispatch internally, then keep a `Notion` suffixed
implementation beside the Postgres implementation for the transition.
