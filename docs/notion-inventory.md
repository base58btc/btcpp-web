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
| `SpeakersDb` / `NOTION_SPEAKERS_DB` | Speaker/contact profile and admin roles. | `Name`, `Email`, `NormPhoto`, `Phone`, `Signal`, `Telegram`, `Twitter`, `npub`, `Github`, `Instagram`, `LinkedIn`, `Website`, `Company`, `OrgPhoto`, `AvailToHire`, `LookingToHire`, `TShirt`, `Roles`. | Linked by `SpeakerConfDb`; role tags grant dashboard/admin access. Role tags split into `scope` and `position`, e.g. `global-admin` -> `global` / `admin`, `vienna-staff` -> `vienna` / `staff`. | `speakers`, `speaker_roles` |
| `ProposalDb` / `NOTION_PROPOSAL_DB` | Talk proposal/application content. | `Title`, `Desc`, `Setup`, `Comments`, `TalkType`, `Status`, `DesiredDuration`, `AvailDuration`, `ScheduleFor`, `speakers`, `InviteToken`. | `ScheduleFor` is a conference tag. `speakers` relates to `SpeakerConfDb`. One accepted proposal usually has one `ConfTalkDb` row. | `proposals`, `proposals_speaker_confs` |
| `SpeakerConfDb` / `NOTION_SPEAKER_CONF_DB` | A speaker's attendance/application state for one conference. | `ComingFrom`, `speaker`, `talk`, `org`, `Company`, `OrgPhoto`, `Avails`, `RecordOK`, `Visa`, `FirstEvent`, `OtherEvents`, `DinnerRSVP`, `Sponsor`, `InvitedAt`, `ViewedAt`, `AcceptedAt`. | `speaker` to `SpeakersDb`, `talk` to `ProposalDb`, `org` to `OrgDb`, `OtherEvents` by conference tag. Runtime upsert key is speaker plus conference. | `speaker_confs`, `speaker_confs_conferences`, `proposals_speaker_confs` |
| `ConfTalkDb` / `NOTION_CONFTALK_DB` | Scheduled talk row used by agenda/media/social. | `Event`, `proposal`, `Clipart`, `TalkTime`, `ProductionNotes`, `Venue`, `Section`, `CalNotif`, `SocialCard`. | `Event` is conference tag. `proposal` relates to `ProposalDb`. | `conf_talks` |
| `RecordingsDb` / `NOTION_RECORDINGS_DB` | Publishing metadata for recorded talks. | `talk`, `TalkName`, `YTLink`, `XLink`, `XReplyLink`, `FileURI`, `PublishAt`. | `talk` relation to `ConfTalkDb`. | `recordings` |
| `SocialPostsDb` / `NOTION_SOCIAL_POSTS_DB` | Social-post state for recordings and other refs. | `Ref`, `Text`, `PostedTo`, `Kind`, `Status`, `Recording`, `ConfTalk`, `URL`, `ReplyURL`, `Error`, `ErrorFingerprint`, `ScheduledAt`, `PostedAt`, `NotifiedAt`. | Optional relations to `RecordingsDb` and `ConfTalkDb`. | `social_posts` |
| `PurchasesDb` / `NOTION_PURCHASES_DB` | One ticket/registration row per purchased item. | `RefID`, `Timestamp`, `Platform`, `conf`, `Type`, `Currency`, `Email`, `Item Bought`, `Lookup ID`, `Amount Paid`, `discount`, `Revoked`, `Checked In`. | `conf` to `ConfsDb`; optional `discount` to `DiscountsDb`. | `registrations` |
| `DiscountsDb` / `NOTION_DISCOUNT_DB` | Discount and affiliate codes. | `CodeName`, `Discount`, `UsesCount`, `AffiliateEmail`, `Conference`. | `Conference` relation to `ConfsDb`; empty means global/wildcard. Used by registrations and affiliate usage. | `discounts`, `discounts_conferences` |
| `AffiliateUsageDb` / `NOTION_AFFILIATE_USE_DB` | Redemption ledger for self-service affiliate codes. | `DiscountCode`, `AffiliateEmail`, `Conference`, `SavedSats`, `EarnedSats`, `TicketsCount`, created time. | Conference is stored as tag/select; code name is stored as text snapshot. | `affiliate_usages` |
| `HotelsDb` / `NOTION_HOTEL_DB` | Hotels listed for conference pages/admin. | `Name`, `URL`, `Img`, `Type`, `Desc`, `Order`, `conf`. | `conf` relation to `ConfsDb`. | `hotels` |
| `OrgDb` / `NOTION_ORG_DB` | Sponsor/organization profile. | `Name`, `Tagline`, `LogoLight`, `LogoDark`, `Email`, `Website`, `LinkedIn`, `Instagram`, `Youtube`, `Github`, `Twitter`, `Nostr`, `Matrix`, `Hiring`, `Notes`. | Linked by sponsorships and speaker-conference org affiliation. | `organizations` |
| `SponsorshipsDb` / `NOTION_SPONSORSHIPS_DB` | Sponsor commitments per conference. | `Name`, `org`, `event`, `Level`, `Label`, `Status`, `IsVendor`, `Notes`. | `org` to `OrgDb`; `event` multi-relation to `ConfsDb`. | `sponsorships`, `sponsorships_conferences` |
| `VolunteerDb` / `NOTION_VOLUNTEER_DB` | Volunteer applications and status. | `Name`, `Email`, `Phone`, `Signal`, `Availability`, `ContactAt`, `Comments`, `DiscoveredVia`, `ScheduleFor`, `OtherEvents`, `WorkYes`, `WorkNo`, `FirstEvent`, `Hometown`, `Twitter`, `npub`, `Shirt`, `Status`, created date. | `ScheduleFor`/`OtherEvents` to conferences; `WorkYes`/`WorkNo` to job types; shifts relate back through assignees/leaders. | `volunteers`, `volunteers_conferences`, `volunteers_job_types` |
| `JobTypeDb` / `NOTION_JOBTYPE_DB` | Volunteer work type catalog. | `Tag`, `DisplayOrder`, `Title`, `Tooltip`, `LongDesc`, `Show`. | Used by volunteers and work shifts. | `job_types` |
| `ShiftDb` / `NOTION_SHIFTS_DB` | Volunteer work shifts. | `Name`, `MaxVols`, `TypeRef`, `ConfRef`, `ShiftTime`, `Assignees`, `ShiftLeader`, `Priority`, `CalNotif`. | `TypeRef` to `JobTypeDb`, `ConfRef` to `ConfsDb`, assignees/leader to `VolunteerDb`. | `work_shifts`, `work_shifts_volunteers` |
| `VolInfoDb` / `NOTION_VOLINFO_DB` | Volunteer orientation metadata per conference. | `conf`, `OrientLink`, `OrientTimes`, `Notes`. | `conf` relation to `ConfsDb`. | `vol_infos` |
| `NewsletterDb` / `NOTION_NEWSLETTER_DB` | Email subscriber list. | `Email`, `Subs`. | Standalone; some forms check subscription state. | `subscribers`, `subscriber_subscriptions` |
| `MissivesDb` / `NOTION_MISSIVES_DB` | Newsletter/email message catalog. | Notion unique `ID`, `Title`, `Newsletter`, `OnlyFor`, `Markdown`, `SendAt`, `SentAt`, `Expiry`. | Newsletter names are tags, not relations. | `missives` |

## Column Mappings

These mappings list the Notion columns used by runtime code and the Postgres
columns they should populate. Notion page IDs are not persisted; they may be
used only in-memory during an import to resolve relations.

### `ConfsDb` -> `conferences`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Name` | `conferences.tag` | Natural key for conferences. |
| `ID` | `conferences.public_uid` | Notion unique ID property, not page ID. |
| `Active` | `conferences.active` | Checkbox. |
| `Desc` | `conferences.description` | Rich text. |
| `OG_Flavor` | `conferences.og_flavor` | Rich text. |
| `Emoji` | `conferences.emoji` | Rich text. |
| `Tagline` | `conferences.tagline` | Rich text. |
| `DateDesc` | `conferences.date_desc` | Rich text. |
| `StartDate` | `conferences.start_date` | Date. |
| `EndDate` | `conferences.end_date` | Date. |
| `Timezone` | `conferences.timezone` | Select or rich text IANA timezone. |
| `Location` | `conferences.location` | Rich text. |
| `Venue` | `conferences.venue` | Rich text. |
| `VenueMap` | `conferences.venue_map_url` | URL. |
| `VenueWebsite` | `conferences.venue_website_url` | URL. |
| `Show Hacks` | `conferences.show_hackathon` | Checkbox. |
| `Has Satellites` | `conferences.has_satellites` | Checkbox. |
| `OrientCalNotif` | `conferences.orient_cal_notif` | Rich text calendar state. |

### `ConfsTixDb` -> `conference_tickets`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Conf` | `conference_tickets.conference_id` | Resolve through related conference tag. |
| `Tier` | `conference_tickets.tier` | Display tier label. |
| `Tier`, `Expires`, prices, `Max`, currency fields | `conference_tickets.ticket_key` | Deterministic non-Notion key because tier names are not unique. |
| `Local` | `conference_tickets.local_price` | Number. |
| `BTC` | `conference_tickets.btc_price` | Number. |
| `USD` | `conference_tickets.usd_price` | Number. |
| `Expires.start` | `conference_tickets.expires_start` | Date range start. |
| `Expires.end` | `conference_tickets.expires_end` | Date range end. |
| `Max` | `conference_tickets.max_count` | Number. |
| `Currency` | `conference_tickets.currency` | Rich text. |
| `Symbol` | `conference_tickets.symbol` | Rich text. |
| `PostSymbol` | `conference_tickets.post_symbol` | Rich text. |

### `ConfInfoDb` -> `conference_days`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Conf` | `conference_days.conference_id` | Resolve from conference tag text/select. |
| `Day` | `conference_days.day_number` | 1-indexed conference day. |
| `Doors` | `conference_days.doors_start`, `conference_days.doors_end` | Rich text `HH:MM,HH:MM`. |
| `Breakfast` | `conference_days.breakfast_start`, `conference_days.breakfast_end` | Rich text `HH:MM,HH:MM`. |
| `Lunch` | `conference_days.lunch_start`, `conference_days.lunch_end` | Rich text `HH:MM,HH:MM`. |
| `Coffee` | `conference_days.coffee_start`, `conference_days.coffee_end` | Rich text `HH:MM,HH:MM`. |
| `Venues` | `conference_days.venues` | Multi-select array. |

### `SpeakersDb` -> `speakers`, `speaker_roles`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Name` | `speakers.name` | Required. |
| `Email` | `speakers.email` | Case-insensitive email. |
| `NormPhoto` | `speakers.norm_photo_path` | Rich text media path. |
| `Phone` | `speakers.phone` | Rich text. |
| `Signal` | `speakers.signal` | Rich text. |
| `Telegram` | `speakers.telegram` | Rich text. |
| `Twitter` | `speakers.twitter_handle` | Normalized handle. |
| `npub` | `speakers.nostr` | Rich text. |
| `Github` | `speakers.github_url` | URL. |
| `Instagram` | `speakers.instagram` | Rich text. |
| `LinkedIn` | `speakers.linkedin` | Rich text. |
| `Website` | `speakers.website_url` | URL. |
| `Company` | `speakers.company` | Rich text. |
| `OrgPhoto` | `speakers.org_logo_path` | Rich text media path. |
| `AvailToHire` | `speakers.avail_to_hire` | Checkbox. |
| `LookingToHire` | `speakers.looking_to_hire` | Checkbox. |
| `TShirt` | `speakers.tshirt` | Select. |
| `Roles` | `speaker_roles.scope`, `speaker_roles.position` | Split each tag at the last hyphen, e.g. `global-admin`, `vienna-staff`, `seoul-admin`. |

### `ProposalDb` -> `proposals`, `proposals_speaker_confs`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `ScheduleFor` | `proposals.conference_id` | Resolve from conference tag select. |
| `Title` | `proposals.title` | Required. |
| `Desc` | `proposals.description` | Rich text. |
| `Setup` | `proposals.setup` | Rich text. |
| `Comments` | `proposals.comments` | Rich text. |
| `TalkType` | `proposals.talk_type` | Select. |
| `Status` | `proposals.status` | Select. |
| `DesiredDuration` | `proposals.desired_duration_min` | Number. |
| `AvailDuration` | `proposals.avail_duration_min` | Number. |
| `InviteToken` | `proposals.invite_token` | Rich text. |
| `speakers` | `proposals_speaker_confs.proposal_id`, `proposals_speaker_confs.speaker_conf_id` | Relation to SpeakerConf rows. |

### `SpeakerConfDb` -> `speaker_confs`, `speaker_confs_conferences`, `proposals_speaker_confs`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| inferred conference | `speaker_confs.conference_id` | Infer from linked proposal `ScheduleFor` or importer context. |
| `speaker` | `speaker_confs.speaker_id` | Relation to `SpeakersDb`. |
| `org` | `speaker_confs.organization_id` | Relation to `OrgDb`. |
| `ComingFrom` | `speaker_confs.coming_from` | Title/rich text. |
| `Avails` | `speaker_confs.availability` | Multi-select array. |
| `RecordOK` | `speaker_confs.record_ok` | Select. |
| `Visa` | `speaker_confs.visa` | Select. |
| `FirstEvent` | `speaker_confs.first_event` | Checkbox. |
| `DinnerRSVP` | `speaker_confs.dinner_rsvp` | Checkbox. |
| `Sponsor` | `speaker_confs.sponsor` | Checkbox. |
| `Company` | `speaker_confs.company` | Rich text. |
| `OrgPhoto` | `speaker_confs.org_photo_path` | Rich text media path. |
| `InvitedAt` | `speaker_confs.invited_at` | Date. |
| `ViewedAt` | `speaker_confs.viewed_at` | Date. |
| `AcceptedAt` | `speaker_confs.accepted_at` | Date. |
| `OtherEvents` | `speaker_confs_conferences.speaker_conf_id`, `speaker_confs_conferences.conference_id` | Multi-select conference tags. |
| `talk` | `proposals_speaker_confs.proposal_id`, `proposals_speaker_confs.speaker_conf_id` | Relation to proposals. |

### `ConfTalkDb` -> `conf_talks`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Event` | `conf_talks.conference_id` | Resolve from conference tag select. |
| `proposal` | `conf_talks.proposal_id` | Relation to Proposal. |
| `Clipart` | `conf_talks.clipart_path` | Rich text/title media path. |
| `TalkTime.start` | `conf_talks.scheduled_start` | Date range start. |
| `TalkTime.end` | `conf_talks.scheduled_end` | Date range end. |
| `ProductionNotes` | `conf_talks.production_notes` | Rich text. |
| `Venue` | `conf_talks.venue` | Select. |
| `Section` | `conf_talks.section` | Select/rich text if present. |
| `CalNotif` | `conf_talks.cal_notif` | Rich text calendar state. |
| `SocialCard` | `conf_talks.social_card_path` | Rich text media path. |
| archived page state | `conf_talks.archived_at` | Set when a Notion row was archived. |

### `RecordingsDb` -> `recordings`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `talk` | `recordings.conf_talk_id` | Relation to ConfTalk. |
| `TalkName` | `recordings.talk_name` | Rich text/title snapshot. |
| `YTLink` | `recordings.youtube_url` | URL. |
| `XLink` | `recordings.x_url` | URL. |
| `XReplyLink` | `recordings.x_reply_url` | URL. |
| `FileURI` | `recordings.file_uri` | Rich text Spaces object key. |
| `PublishAt` | `recordings.publish_at` | Date. |

### `SocialPostsDb` -> `social_posts`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Ref` | `social_posts.ref` | Natural key. |
| `Text` | `social_posts.text` | Rich text. |
| `PostedTo` | `social_posts.posted_to` | Select or rich text. |
| `Kind` | `social_posts.kind` | Select or rich text. |
| `Status` | `social_posts.status` | Select or rich text. |
| `Recording` | `social_posts.recording_id` | Optional relation to Recording. |
| `ConfTalk` | `social_posts.conf_talk_id` | Optional relation to ConfTalk. |
| `URL` | `social_posts.url` | URL. |
| `ReplyURL` | `social_posts.reply_url` | URL. |
| `Error` | `social_posts.error` | Rich text. |
| `ErrorFingerprint` | `social_posts.error_fingerprint` | Rich text. |
| `ScheduledAt` | `social_posts.scheduled_at` | Date. |
| `PostedAt` | `social_posts.posted_at` | Date. |
| `NotifiedAt` | `social_posts.notified_at` | Date. |

### `PurchasesDb` -> `registrations`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `RefID` | `registrations.ref_id` | Public stable ticket ID. |
| `Lookup ID` | `registrations.checkout_id` | Checkout/payment identifier. |
| `conf` | `registrations.conference_id` | Relation to conference. |
| `discount` | `registrations.discount_id` | Optional relation to discount. |
| `Type` | `registrations.type` | Select. |
| `Email` | `registrations.email` | Case-insensitive email. |
| `Item Bought` | `registrations.item_bought` | Rich text. |
| `Amount Paid` | `registrations.amount_paid` | Number, already in main currency units. |
| `Currency` | `registrations.currency` | Select. |
| `Platform` | `registrations.platform` | Select, e.g. Stripe/OpenNode/admin. |
| `Timestamp` | `registrations.purchased_at` | Rich text RFC3339 in current writer. |
| `Checked In` | `registrations.checked_in_at` | Rich text RFC3339 in current writer. |
| `Revoked` | `registrations.revoked` | Checkbox. |

### `DiscountsDb` -> `discounts`, `discounts_conferences`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `CodeName` | `discounts.code_name` | Case-insensitive natural key. |
| `Discount` | `discounts.discount_expr` | Raw discount expression. |
| parsed `Discount` | `discounts.disc_type`, `discounts.amount`, `discounts.max_uses`, `discounts.extra_qty`, `discounts.valid_from`, `discounts.valid_until` | Derived by the existing discount parser. |
| `UsesCount` | `discounts.uses_count` | Number. |
| `AffiliateEmail` | `discounts.affiliate_email` | Email or rich text. |
| `Conference` | `discounts_conferences.discount_id`, `discounts_conferences.conference_id` | Empty relation means global/wildcard. |
| archived page state | `discounts.archived_at` | Set when a Notion row was archived. |

### `AffiliateUsageDb` -> `affiliate_usages`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `DiscountCode` | `affiliate_usages.code_name_snapshot` | Text snapshot; can also resolve `discount_id`. |
| `DiscountCode` lookup | `affiliate_usages.discount_id` | Optional FK by discount code. |
| `AffiliateEmail` | `affiliate_usages.affiliate_email` | Email/rich text. |
| `Conference` | `affiliate_usages.conference_id` | Select conference tag. |
| `SavedSats` | `affiliate_usages.saved_sats` | Number. |
| `EarnedSats` | `affiliate_usages.earned_sats` | Number. |
| `TicketsCount` | `affiliate_usages.tickets_count` | Number. |
| Notion created time | `affiliate_usages.created_at` | Use page created time when importing. |

### `HotelsDb` -> `hotels`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `conf` | `hotels.conference_id` | Relation to conference. |
| `Name` | `hotels.name` | Required. |
| `URL` | `hotels.url` | URL. |
| `Img` | `hotels.img_path` | Rich text Spaces object path. |
| `Type` | `hotels.type` | Rich text. |
| `Desc` | `hotels.description` | Rich text. |
| `Order` | `hotels.display_order` | Number. |
| archived page state | `hotels.archived_at` | Set when a Notion row was archived. |

### `OrgDb` -> `organizations`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Name` | `organizations.name` | Required. Importer upserts by case-insensitive name. |
| `Tagline` | `organizations.tagline` | Rich text. |
| `LogoLight` | `organizations.logo_light_url` | URL. |
| `LogoDark` | `organizations.logo_dark_url` | URL. |
| `Email` | `organizations.email` | Email. |
| `Website` | `organizations.website_url` | URL. Not unique in current Notion data. |
| `LinkedIn` | `organizations.linkedin_url` | URL. |
| `Instagram` | `organizations.instagram_url` | URL. |
| `Youtube` | `organizations.youtube_url` | URL. |
| `Github` | `organizations.github_url` | URL. |
| `Twitter` | `organizations.twitter_handle` | Normalized handle. |
| `Nostr` | `organizations.nostr` | Rich text. |
| `Matrix` | `organizations.matrix` | Rich text. |
| `Hiring` | `organizations.hiring` | Checkbox. |
| `Notes` | `organizations.notes` | Rich text. |

### `SponsorshipsDb` -> `sponsorships`, `sponsorships_conferences`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Name` | `sponsorships.name` | Display-ish label. Importer appends related event tag(s) only for readability; duplicates are allowed and rows use generated UUID primary keys. Blank names fall back to org name plus event tag(s). |
| `org` | `sponsorships.organization_id` | Relation to `OrgDb`; importer resolves through in-memory org ref to `organizations.id`. |
| `event` | `sponsorships_conferences.sponsorship_id`, `sponsorships_conferences.conference_id` | Multi-relation to conferences; importer resolves each related conference ref to `conferences.tag`, then `conferences.id`. |
| `Level` | `sponsorships.level` | Select. |
| `Label` | `sponsorships.label` | Rich text. |
| `Status` | `sponsorships.status` | Select. |
| `IsVendor` | `sponsorships.is_vendor` | Checkbox. |
| `Notes` | `sponsorships.notes` | Rich text. |
| archived page state | `sponsorships.archived_at` | Set when a Notion row was archived. |

Importer note: since Notion page IDs are intentionally not retained,
`SponsorshipsDb` is duplicate-friendly and import rows are inserted with
generated UUID primary keys. During migration, rerun this table with `-reset`
to avoid duplicate rows from repeated imports.

### `VolunteerDb` -> `volunteers`, `volunteers_conferences`, `volunteers_job_types`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Name` | `volunteers.name` | Required. |
| `Email` | `volunteers.email` | Case-insensitive email. |
| `Phone` | `volunteers.phone` | Phone. |
| `Signal` | `volunteers.signal` | Rich text. |
| `Availability` | `volunteers.availability` | Multi-select array. |
| `ContactAt` | `volunteers.contact_at` | Rich text. |
| `Comments` | `volunteers.comments` | Rich text. |
| `DiscoveredVia` | `volunteers.discovered_via` | Rich text. |
| `FirstEvent` | `volunteers.first_event` | Checkbox. |
| `Hometown` | `volunteers.hometown` | Rich text. |
| `Twitter` | `volunteers.twitter_handle` | Normalized handle. |
| `npub` | `volunteers.nostr` | Rich text. |
| `Shirt` | `volunteers.shirt` | Select. |
| `Status` | `volunteers.status` | Select. |
| form `Captcha` | `volunteers.captcha` | Captured on struct; not always written to Notion. |
| form `Subscribe` | `volunteers.subscribe` | Captured on struct; not always written to Notion. |
| `created` | `volunteers.created_at` | Current parser reads a `created` date property. |
| `ScheduleFor` | `volunteers_conferences.volunteer_id`, `volunteers_conferences.conference_id`, `kind='schedule_for'` | Relation to conferences. |
| `OtherEvents` | `volunteers_conferences.volunteer_id`, `volunteers_conferences.conference_id`, `kind='other_event'` | Relation to conferences. |
| `WorkYes` | `volunteers_job_types.volunteer_id`, `volunteers_job_types.job_type_id`, `preference='yes'` | Relation to JobType. |
| `WorkNo` | `volunteers_job_types.volunteer_id`, `volunteers_job_types.job_type_id`, `preference='no'` | Relation to JobType. |

### `JobTypeDb` -> `job_types`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Tag` | `job_types.tag` | Natural key. |
| `DisplayOrder` | `job_types.display_order` | Number. |
| `Title` | `job_types.title` | Required. |
| `Tooltip` | `job_types.tooltip` | Rich text. |
| `LongDesc` | `job_types.long_desc` | Rich text. |
| `Show` | `job_types.show` | Checkbox. |

### `ShiftDb` -> `work_shifts`, `work_shifts_volunteers`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `ConfRef` | `work_shifts.conference_id` | Relation to conference. |
| `TypeRef` | `work_shifts.job_type_id` | Relation to JobType. |
| `Name` | `work_shifts.name` | Required. |
| `MaxVols` | `work_shifts.max_vols` | Number. |
| `ShiftTime.start` | `work_shifts.shift_start` | Date range start. |
| `ShiftTime.end` | `work_shifts.shift_end` | Date range end. |
| `Priority` | `work_shifts.priority` | Number. |
| `CalNotif` | `work_shifts.cal_notif` | Rich text calendar state. |
| `Assignees` | `work_shifts_volunteers.shift_id`, `work_shifts_volunteers.volunteer_id`, `role='assignee'` | Multi-relation to volunteers. |
| `ShiftLeader` | `work_shifts_volunteers.shift_id`, `work_shifts_volunteers.volunteer_id`, `role='leader'` | Relation to volunteer. |

### `VolInfoDb` -> `vol_infos`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `conf` | `vol_infos.conference_id` | Relation to conference. |
| `OrientLink` | `vol_infos.orient_link_url` | URL. |
| `OrientTimes.start` | `vol_infos.orient_start` | Date range start. |
| `OrientTimes.end` | `vol_infos.orient_end` | Date range end. |
| `Notes` | `vol_infos.notes` | Rich text. |

### `NewsletterDb` -> `subscribers`, `subscriber_subscriptions`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `Email` | `subscribers.email` | Case-insensitive natural key. |
| `Subs` | `subscriber_subscriptions.subscriber_id`, `subscriber_subscriptions.name` | Multi-select subscription names. |

### `MissivesDb` -> `missives`

| Notion column | Postgres column | Notes |
| --- | --- | --- |
| `ID` | `missives.public_uid` | Notion unique ID property, not page ID. |
| `Title` | `missives.title` | Required. |
| `Newsletter` | `missives.newsletters` | Multi-select array. |
| `OnlyFor` | `missives.only_for` | Select. |
| `Markdown` | `missives.markdown` | Rich text. |
| `SendAt` | `missives.send_at_expr` | Scheduling expression. |
| `SentAt` | `missives.sent_at` | Date. |
| `Expiry` | `missives.expiry` | Date. |

## Legacy Or Unused Notion Config

| Config | Current status |
| --- | --- |
| `NOTION_TALKS_DB` | Present in `.do/app.yaml`, but not loaded by `cmd/web/main.go`. Talk rendering now comes from `ConfTalkDb -> ProposalDb -> SpeakerConfDb -> SpeakersDb`. |
| `TalkAppDb` | Used by old migration commands, not runtime config. It was the older talk application source that fed the newer proposal/speaker-conf model. |
| `EmailDb` | Present on `types.NotionConfig`, but not loaded in production config and no runtime references were found. |

## Index And Uniqueness Rationale

These notes describe the initial Postgres schema indexes and uniqueness rules.
UUID `id` columns remain the primary key unless a table is a pure join table.

| Table | Index / unique rule | Unique? | Rationale |
| --- | --- | --- | --- |
| `conferences` | `tag` | yes | Stable public conference slug and importer upsert key. |
| `conferences` | `public_uid` | yes | Notion unique ID property when present; not the Notion page ID. |
| `conference_days` | `(conference_id, day_number)` | yes | One schedule metadata row per conference day. |
| `conference_tickets` | `(conference_id, ticket_key)` | yes | Prevent duplicate ticket tiers on reruns without relying on Notion page IDs. |
| `speakers` | `email` | no | Lookup aid; nullable and not all speakers have email. |
| `speakers` | `lower(name)` | no | Admin/search lookup aid; names can duplicate. |
| `speaker_roles` | `(speaker_id, scope, position)` primary key | yes | A speaker should not have the same scoped role twice. |
| `organizations` | `lower(name)` | yes | Current importer key. Website is not unique in real Notion data. |
| `sponsorships` | `organization_id` | no | Lookup sponsorships by org. |
| `sponsorships` | `(status, level)` | no | Filtering/grouping sponsorships by workflow status and tier. Names intentionally allow duplicates. |
| `sponsorships_conferences` | `(sponsorship_id, conference_id)` primary key | yes | Prevent duplicate conference links for the same sponsorship row. |
| `proposals` | `(conference_id, status)` | no | Admin review/status filtering by conference. |
| `proposals` | `invite_token` where non-empty | no | Lookup invite token when present; blank tokens are ignored. |
| `speaker_confs` | `(conference_id, speaker_id)` | yes | One speaker attendance/application row per conference. |
| `speaker_confs` | `speaker_id` | no | Reverse lookup all conference rows for a speaker. |
| `speaker_confs_conferences` | `(speaker_conf_id, conference_id)` primary key | yes | Prevent duplicate related-event links. |
| `proposals_speaker_confs` | `(proposal_id, speaker_conf_id)` primary key | yes | Prevent duplicate speaker links on a proposal. |
| `conf_talks` | `proposal_id` | yes | At most one scheduled talk row per accepted proposal. |
| `conf_talks` | `(conference_id, scheduled_start)` | no | Schedule rendering/query order by conference. |
| `recordings` | `conf_talk_id` | yes | One recording metadata row per scheduled talk. |
| `social_posts` | `ref` | yes | Stable app-generated social post identity. |
| `social_posts` | `(kind, status)` | no | Dashboard/background job filtering. |
| `hotels` | `(conference_id, display_order, name)` | no | Conference hotel rendering order. |
| `discounts` | `code_name` | yes | Discount codes are case-insensitive public identifiers. |
| `discounts_conferences` | `(discount_id, conference_id)` primary key | yes | Prevent duplicate discount/conference links. |
| `registrations` | `ref_id` | yes | Ticket/public registration reference identity. |
| `registrations` | `conference_id` | no | Conference attendee/report lookups. |
| `registrations` | `email` | no | Attendee/email lookups; emails can repeat. |
| `registrations` | `checkout_id` where non-empty | no | Payment lookup aid; blank checkout IDs are ignored. |
| `affiliate_usages` | `affiliate_email` | no | Affiliate reporting by email. |
| `affiliate_usages` | `conference_id` | no | Affiliate reporting by conference. |
| `job_types` | `tag` | yes | Stable job type identifier. |
| `volunteers` | `email` | no | Volunteer lookup aid; repeat applications may share email. |
| `volunteers_conferences` | `(volunteer_id, conference_id, kind)` primary key | yes | A volunteer should not have the same conference relation kind twice. |
| `volunteers_job_types` | `(volunteer_id, job_type_id, preference)` primary key | yes | Prevent duplicate yes/no preference rows. |
| `work_shifts` | `(conference_id, shift_start)` | no | Schedule/admin lookup by conference and time. |
| `work_shifts_volunteers` | `(shift_id, volunteer_id, role)` primary key | yes | Prevent duplicate shift assignment rows. |
| `work_shifts_volunteers` | one leader per `shift_id` where `role='leader'` | yes | Enforces one shift leader while allowing many assignees. |
| `vol_infos` | `conference_id` | yes | One volunteer info/orientation row per conference. |
| `subscribers` | `email` | yes | Subscriber email is the account/subscription identity. |
| `subscriber_subscriptions` | `(subscriber_id, name)` primary key | yes | Prevent duplicate subscription names per subscriber. |
| `missives` | `public_uid` | yes | Notion unique ID property when present; not the Notion page ID. |
| `missives` | `newsletters` GIN | no | Query messages by newsletter membership. |
| `missives` | `only_for` where non-empty | no | Filter targeted messages; blank targets are ignored. |

## Migration Key Assumptions

Because Notion page IDs are not retained in Postgres, the importer needs
temporary in-memory maps while it runs:

- Conferences map by `Name`/tag.
- Conference tickets, hotels, shifts, volunteer info, registrations, and
  sponsorships resolve conferences through the related row's conference tag.
- Organizations currently map by case-insensitive `Name`.
- Sponsorships use generated UUID primary keys. The importer allows duplicate
  names and should be rerun with `-reset` during migration to avoid repeated
  insert duplicates.
- Speakers map primarily by email. Rows without email need a fallback
  disambiguator such as normalized name plus social/contact fields. Current
  migration imports speakers with generated UUID primary keys and should be
  rerun with `-reset` to avoid duplicate inserts.
- Speaker-conference rows map by `(speaker, conference)`. The conference is
  inferred from linked proposal `ScheduleFor` values or explicit context.
- Proposals map by generated UUID after import; while importing, resolve them
  by the current Notion row in memory, then discard the Notion key.
- Organizations map by normalized name; website is not unique in current
  Notion data.
- Discounts map by case-insensitive code name.
- Registrations map by `RefID`, which is already the ticket's public stable ID.
- Social posts map by `Ref`.

Any migration command should fail loudly on ambiguous natural keys rather than
silently picking a row.
