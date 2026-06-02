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

## Remaining Write Review

No known normal app write path from the original Notion-backed checklist remains
intentionally Notion-only. Migration/backfill commands under `cmd/` can still be
Notion-specific unless they are needed after production cutover.

## Completed Write Splits

| Domain | Completed entrypoints | Notes |
| --- | --- | --- |
| Registrations / tickets | `AddTickets`, `RevokeTicket` | Now dispatch by `AppContext`; Postgres writes insert/update `registrations`, and Notion remains the fallback. |
| Speakers / people | `CreateSpeaker`, `UpdateSpeaker`, `UpdateSpeakerRoles`, `GetSpeakersByEmail`, `FetchSpeakerByID` | Now dispatch by `AppContext`; Postgres writes `people` and `people_roles`, and Notion remains the fallback. |
| Missives / subscribers | `FindSubscriber`, `ListSubscribersFor`, `IsSubscribedTo`, `SubscribeEmailList`, `SubscribeEmail`, `UpdateSubs`, `GetLetter`, `GetLetterFor`, `GetLetters`, `ListOnlyForLetters`, `ListTemplatedLetters`, `CreateTemplatedMissive`, `UpdateTemplatedMissive`, `CreateMissive`, `MarkLetterSent` | Now dispatch by `AppContext`; Postgres reads/writes `subscribers`, `subscriber_subscriptions`, and `missives`, and Notion remains the fallback. |
| Proposal core writes | `CreateProposal`, `UpdateProposal`, `UpdateProposalStatus`, `SetProposalInviteToken` | Now dispatch by `AppContext`; Postgres writes `proposals`, and Notion remains the fallback. |
| Conf talk writes | `CreateConfTalk`, `UpdateConfTalkSchedule`, `DeleteConfTalk`, `ConfTalkSetSocialCard`, `ConfTalkSetClipart`, `TalkUpdateCalNotif` | Now dispatch by `AppContext`; Postgres writes `conf_talks`, and Notion remains the fallback. |
| Speaker-conf writes | `UpsertSpeakerConf`, `UpdateSpeakerConf`, `AddSpeakerConfToProposal`, `RemoveProposalFromSpeakerConf`, `SetSpeakerConfInvitedAt`, `SetSpeakerConfViewedAt`, `SetSpeakerConfAcceptedAt` | Now dispatch by `AppContext`; Postgres writes `speaker_confs`, `proposals_speaker_confs`, and `speaker_confs_conferences`, and Notion remains the fallback. |
| Volunteer / shift writes | `RegisterVolunteer`, `UpdateVolunteerStatus`, `UpdateVolunteerAvailability`, `UpdateVolunteerWorkPrefs`, `CreateShift`, `UpdateShift`, `UpdateShiftTimes`, `AssignVolunteerToShift`, `RemoveVolunteerFromShift`, `ShiftUpdateCalNotif` | Now dispatch by `AppContext`; Postgres writes `volunteers`, `volunteers_job_types`, `work_shifts`, `work_shifts_volunteers`, and work-shift calendar notification stamps, and Notion remains the fallback. |
| Sponsor / organization writes | `RegisterOrg`, `UpdateOrg`, `UpdateOrgDetails`, `RegisterSponsorship`, `UpdateSponsorshipStatus` | Now dispatch by `AppContext`; Postgres writes `organizations`, `sponsorships`, and `sponsorships_conferences`, and Notion remains the fallback. |
| Discount / affiliate writes | `CreateDiscount`, `UpdateDiscount`, `ArchiveDiscount`, `CreateAffiliateCode`, `UpdateAffiliateCode`, `ArchiveAffiliateCode` | Now dispatch by `AppContext`; Postgres writes `discounts` and `discounts_conferences`, and Notion remains the fallback. |
| Hotel / conference calendar writes | `CreateHotel`, `UpdateHotel`, `ArchiveHotel`, `ConfUpdateOrientCalNotif` | Now dispatch by `AppContext`; Postgres writes `hotels` and conference orientation calendar notification stamps, and Notion remains the fallback. |
| Generic file upload | `UploadFile` | Now dispatches by `AppContext`; Postgres uploads content-addressed objects to Spaces and returns the public URL, while Notion remains the fallback. |

## Paused / Separate Work

| Domain | Status |
| --- | --- |
| Migration and backfill commands | `cmd/migrate-*`, `cmd/backfill-*`, and similar tools can stay Notion-specific unless needed after production cutover. |

## Next Validation Steps

1. Run the app locally with `dataBackend = "postgres"` and exercise the major
   read/write workflows end to end.
2. Decide which `cmd/migrate-*` and `cmd/backfill-*` tools must work after
   cutover and either keep them Notion-specific or split them by backend.
3. Revisit production configuration, deployment, and security hardening once the
   functional cutover is verified.
