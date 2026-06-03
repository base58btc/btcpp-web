# Postgres Cutover Status

This tracks the current state of running normal app workflows with
`dataBackend = "postgres"`.

## Current State

- Read getters for migrated domains dispatch through `AppContext` and can use
  Postgres.
- `external/getters/notion.go` has been removed; Notion implementations now
  live in domain-specific files.
- Normal app read and write paths that were part of the cutover checklist now
  dispatch by backend.
- Local validation has run with `DATA_BACKEND=postgres`; the app starts and the
  homepage plus an event page returned HTTP 200.
- Migration and one-off maintenance commands under `cmd/` can remain
  Notion-specific unless they are needed after cutover.

## Remaining Write Review

No known normal app write path from the original Notion-backed checklist remains
intentionally Notion-only. Migration/backfill commands under `cmd/` can still be
Notion-specific unless they are needed after production cutover.

## Completed Write Splits

| Domain | Completed entrypoints |
| --- | --- |
| Registrations / tickets | `AddTickets`, `RevokeTicket` |
| Speakers / people | `CreateSpeaker`, `UpdateSpeaker`, `UpdateSpeakerRoles`, `GetSpeakersByEmail`, `FetchSpeakerByID` |
| Missives / subscribers | `FindSubscriber`, `ListSubscribersFor`, `IsSubscribedTo`, `SubscribeEmailList`, `SubscribeEmail`, `UpdateSubs`, `GetLetter`, `GetLetterFor`, `GetLetters`, `ListOnlyForLetters`, `ListTemplatedLetters`, `CreateTemplatedMissive`, `UpdateTemplatedMissive`, `CreateMissive`, `MarkLetterSent` |
| Proposal core writes | `CreateProposal`, `UpdateProposal`, `UpdateProposalStatus`, `SetProposalInviteToken` |
| Conf talk writes | `CreateConfTalk`, `UpdateConfTalkSchedule`, `DeleteConfTalk`, `ConfTalkSetSocialCard`, `ConfTalkSetClipart`, `TalkUpdateCalNotif` |
| Speaker-conf writes | `UpsertSpeakerConf`, `UpdateSpeakerConf`, `AddSpeakerConfToProposal`, `RemoveProposalFromSpeakerConf`, `SetSpeakerConfInvitedAt`, `SetSpeakerConfViewedAt`, `SetSpeakerConfAcceptedAt` |
| Volunteer / shift writes | `RegisterVolunteer`, `UpdateVolunteerStatus`, `UpdateVolunteerAvailability`, `UpdateVolunteerWorkPrefs`, `CreateShift`, `UpdateShift`, `UpdateShiftTimes`, `AssignVolunteerToShift`, `RemoveVolunteerFromShift`, `ShiftUpdateCalNotif` |
| Sponsor / organization writes | `RegisterOrg`, `UpdateOrg`, `UpdateOrgDetails`, `RegisterSponsorship`, `UpdateSponsorshipStatus` |
| Discount / affiliate writes | `CreateDiscount`, `UpdateDiscount`, `ArchiveDiscount`, `CreateAffiliateCode`, `UpdateAffiliateCode`, `ArchiveAffiliateCode` |
| Hotel / conference calendar writes | `CreateHotel`, `UpdateHotel`, `ArchiveHotel`, `ConfUpdateOrientCalNotif` |
| Generic file upload | `UploadFile` |

## Paused / Separate Work

| Domain | Status |
| --- | --- |
| Migration and backfill commands | `cmd/migrate-*`, `cmd/backfill-*`, and similar tools can stay Notion-specific unless needed after production cutover. |

## Remaining Validation Steps

1. Exercise the major read/write workflows from a browser against local
   Postgres: registration/check-in, proposal submission/admin review,
   speaker-conf acceptance, sponsorship/admin edits, volunteer signup/shifts,
   discounts/affiliate usage, recordings/social posts, hotels, subscribers,
   and missives.
2. Decide which `cmd/migrate-*` and `cmd/backfill-*` tools must work after
   cutover and either keep them Notion-specific or split them by backend.
3. Revisit production configuration, deployment, and security hardening once the
   functional cutover is verified.
