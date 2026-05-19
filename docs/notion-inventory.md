# Notion Data Inventory

This inventory captures the Notion databases the app currently reads or
writes. The Postgres schema intentionally does not preserve Notion page IDs;
imports need to resolve relationships through natural keys and generated
Postgres IDs.

## Runtime Databases

| Config key | Current role | Important fields | Relationships | Postgres target |
| --- | --- | --- | --- | --- |
| `ConfsDb` / `NOTION_CONFS_DB` | Conference/event catalog. | `Name` tag, Notion unique `ID`, `Active`, `Desc`, `OG_Flavor`, `Emoji`, `Tagline`, `DateDesc`, `StartDate`, `EndDate`, `Location`, `Venue`, `VenueMap`, `VenueWebsite`, `Show Hacks`, `Has Satellites`, `Timezone`, `OrientCalNotif`. | Parent of tickets, hotels, registrations, volunteers, shifts, sponsorships, conf talks, vol info. Some newer rows reference conferences by tag instead of relation. | `conferences` |
| `ConfsTixDb` / `NOTION_CONFSTIX_DB` | Ticket tiers per conference. | `Tier`, `Local`, `BTC`, `USD`, `Expires`, `Max`, `Currency`, `Symbol`, `PostSymbol`. | `Conf` relation to `ConfsDb`. | `conference_tickets` |
| `ConfInfoDb` / `NOTION_CONFINFO_DB` | Per-day public schedule metadata. | `Conf` tag/select or rich text, `Day`, `Doors`, `Breakfast`, `Lunch`, `Coffee`, `Venues`. | Uses conference tag, not a Notion relation. | `conference_days` |
| `SpeakersDb` / `NOTION_SPEAKERS_DB` | Speaker/contact profile and admin roles. | `Name`, `Email`, `NormPhoto`, `Phone`, `Signal`, `Telegram`, `Twitter`, `npub`, `Github`, `Instagram`, `LinkedIn`, `Website`, `Company`, `OrgPhoto`, `AvailToHire`, `LookingToHire`, `TShirt`, `Roles`. | Linked by `SpeakerConfDb`; role tags grant dashboard/admin access. | `speakers`, `speaker_roles` |
| `ProposalDb` / `NOTION_PROPOSAL_DB` | Talk proposal/application content. | `Title`, `Desc`, `Setup`, `Comments`, `TalkType`, `Status`, `DesiredDuration`, `AvailDuration`, `ScheduleFor`, `speakers`, `InviteToken`. | `ScheduleFor` is a conference tag. `speakers` relates to `SpeakerConfDb`. One accepted proposal usually has one `ConfTalkDb` row. | `proposals`, `proposal_speakers` |
| `SpeakerConfDb` / `NOTION_SPEAKER_CONF_DB` | A speaker's attendance/application state for one conference. | `ComingFrom`, `speaker`, `talk`, `org`, `Company`, `OrgPhoto`, `Avails`, `RecordOK`, `Visa`, `FirstEvent`, `OtherEvents`, `DinnerRSVP`, `Sponsor`, `InvitedAt`, `ViewedAt`, `AcceptedAt`. | `speaker` to `SpeakersDb`, `talk` to `ProposalDb`, `org` to `OrgDb`, `OtherEvents` by conference tag. Runtime upsert key is speaker plus conference. | `speaker_confs`, `speaker_conf_other_events`, `proposal_speakers` |
| `ConfTalkDb` / `NOTION_CONFTALK_DB` | Scheduled talk row used by agenda/media/social. | `Event`, `proposal`, `Clipart`, `TalkTime`, `ProductionNotes`, `Venue`, `Section`, `CalNotif`, `SocialCard`. | `Event` is conference tag. `proposal` relates to `ProposalDb`. | `conf_talks` |
| `RecordingsDb` / `NOTION_RECORDINGS_DB` | Publishing metadata for recorded talks. | `talk`, `TalkName`, `YTLink`, `XLink`, `XReplyLink`, `FileURI`, `PublishAt`. | `talk` relation to `ConfTalkDb`. | `recordings` |
| `SocialPostsDb` / `NOTION_SOCIAL_POSTS_DB` | Social-post state for recordings and other refs. | `Ref`, `Text`, `PostedTo`, `Kind`, `Status`, `Recording`, `ConfTalk`, `URL`, `ReplyURL`, `Error`, `ErrorFingerprint`, `ScheduledAt`, `PostedAt`, `NotifiedAt`. | Optional relations to `RecordingsDb` and `ConfTalkDb`. | `social_posts` |
| `PurchasesDb` / `NOTION_PURCHASES_DB` | One ticket/registration row per purchased item. | `RefID`, `Timestamp`, `Platform`, `conf`, `Type`, `Currency`, `Email`, `Item Bought`, `Lookup ID`, `Amount Paid`, `discount`, `Revoked`, `Checked In`. | `conf` to `ConfsDb`; optional `discount` to `DiscountsDb`. | `registrations` |
| `DiscountsDb` / `NOTION_DISCOUNT_DB` | Discount and affiliate codes. | `CodeName`, `Discount`, `UsesCount`, `AffiliateEmail`, `Conference`. | `Conference` relation to `ConfsDb`; empty means global/wildcard. Used by registrations and affiliate usage. | `discounts`, `discount_conferences` |
| `AffiliateUsageDb` / `NOTION_AFFILIATE_USE_DB` | Redemption ledger for self-service affiliate codes. | `DiscountCode`, `AffiliateEmail`, `Conference`, `SavedSats`, `EarnedSats`, `TicketsCount`, created time. | Conference is stored as tag/select; code name is stored as text snapshot. | `affiliate_usages` |
| `HotelsDb` / `NOTION_HOTEL_DB` | Hotels listed for conference pages/admin. | `Name`, `URL`, `Img`, `Type`, `Desc`, `Order`, `conf`. | `conf` relation to `ConfsDb`. | `hotels` |
| `OrgDb` / `NOTION_ORG_DB` | Sponsor/organization profile. | `Name`, `Tagline`, `LogoLight`, `LogoDark`, `Email`, `Website`, `LinkedIn`, `Instagram`, `Youtube`, `Github`, `Twitter`, `Nostr`, `Matrix`, `Hiring`, `Notes`. | Linked by sponsorships and speaker-conference org affiliation. | `organizations` |
| `SponsorshipsDb` / `NOTION_SPONSORSHIPS_DB` | Sponsor commitments per conference. | `Name`, `org`, `event`, `Level`, `Label`, `Status`, `IsVendor`, `Notes`. | `org` to `OrgDb`; `event` multi-relation to `ConfsDb`. | `sponsorships`, `sponsorship_conferences` |
| `VolunteerDb` / `NOTION_VOLUNTEER_DB` | Volunteer applications and status. | `Name`, `Email`, `Phone`, `Signal`, `Availability`, `ContactAt`, `Comments`, `DiscoveredVia`, `ScheduleFor`, `OtherEvents`, `WorkYes`, `WorkNo`, `FirstEvent`, `Hometown`, `Twitter`, `npub`, `Shirt`, `Status`, created date. | `ScheduleFor`/`OtherEvents` to conferences; `WorkYes`/`WorkNo` to job types; shifts relate back through assignees/leaders. | `volunteers`, `volunteer_conferences`, `volunteer_job_preferences` |
| `JobTypeDb` / `NOTION_JOBTYPE_DB` | Volunteer work type catalog. | `Tag`, `DisplayOrder`, `Title`, `Tooltip`, `LongDesc`, `Show`. | Used by volunteers and work shifts. | `job_types` |
| `ShiftDb` / `NOTION_SHIFTS_DB` | Volunteer work shifts. | `Name`, `MaxVols`, `TypeRef`, `ConfRef`, `ShiftTime`, `Assignees`, `ShiftLeader`, `Priority`, `CalNotif`. | `TypeRef` to `JobTypeDb`, `ConfRef` to `ConfsDb`, assignees/leader to `VolunteerDb`. | `work_shifts`, `work_shift_assignments` |
| `VolInfoDb` / `NOTION_VOLINFO_DB` | Volunteer orientation metadata per conference. | `conf`, `OrientLink`, `OrientTimes`, `Notes`. | `conf` relation to `ConfsDb`. | `vol_infos` |
| `NewsletterDb` / `NOTION_NEWSLETTER_DB` | Email subscriber list. | `Email`, `Subs`. | Standalone; some forms check subscription state. | `subscribers`, `subscriber_subscriptions` |
| `MissivesDb` / `NOTION_MISSIVES_DB` | Newsletter/email message catalog. | Notion unique `ID`, `Title`, `Newsletter`, `OnlyFor`, `Markdown`, `SendAt`, `SentAt`, `Expiry`. | Newsletter names are tags, not relations. | `missives` |

## Legacy Or Unused Notion Config

| Config | Current status |
| --- | --- |
| `NOTION_TALKS_DB` | Present in `.do/app.yaml`, but not loaded by `cmd/web/main.go`. Talk rendering now comes from `ConfTalkDb -> ProposalDb -> SpeakerConfDb -> SpeakersDb`. |
| `TalkAppDb` | Used by old migration commands, not runtime config. It was the older talk application source that fed the newer proposal/speaker-conf model. |
| `EmailDb` | Present on `types.NotionConfig`, but not loaded in production config and no runtime references were found. |

## Migration Key Assumptions

Because Notion page IDs are not retained in Postgres, the importer needs
temporary in-memory maps while it runs:

- Conferences map by `Name`/tag.
- Conference tickets, hotels, shifts, volunteer info, registrations, and
  sponsorships resolve conferences through the related row's conference tag.
- Speakers map primarily by email. Rows without email need a fallback
  disambiguator such as normalized name plus social/contact fields.
- Speaker-conference rows map by `(speaker, conference)`. The conference is
  inferred from linked proposal `ScheduleFor` values or explicit context.
- Proposals map by generated UUID after import; while importing, resolve them
  by the current Notion row in memory, then discard the Notion key.
- Organizations map by normalized website first, normalized name second.
- Discounts map by case-insensitive code name.
- Registrations map by `RefID`, which is already the ticket's public stable ID.
- Social posts map by `Ref`.

Any migration command should fail loudly on ambiguous natural keys rather than
silently picking a row.
