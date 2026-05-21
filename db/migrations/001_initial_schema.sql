BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS trigger AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE conferences (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tag text NOT NULL UNIQUE,
  public_uid bigint UNIQUE,
  active boolean NOT NULL DEFAULT false,
  description text NOT NULL DEFAULT '',
  og_flavor text NOT NULL DEFAULT '',
  emoji text NOT NULL DEFAULT '',
  tagline text NOT NULL DEFAULT '',
  date_desc text NOT NULL DEFAULT '',
  start_date date,
  end_date date,
  timezone text NOT NULL DEFAULT '',
  location text NOT NULL DEFAULT '',
  venue text NOT NULL DEFAULT '',
  venue_map_url text NOT NULL DEFAULT '',
  venue_website_url text NOT NULL DEFAULT '',
  show_hackathon boolean NOT NULL DEFAULT false,
  has_satellites boolean NOT NULL DEFAULT false,
  orient_cal_notif text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (tag <> ''),
  CHECK (end_date IS NULL OR start_date IS NULL OR end_date >= start_date)
);

CREATE TRIGGER conferences_set_updated_at
BEFORE UPDATE ON conferences
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE conference_days (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  day_number integer NOT NULL,
  doors_start time,
  doors_end time,
  breakfast_start time,
  breakfast_end time,
  lunch_start time,
  lunch_end time,
  coffee_start time,
  coffee_end time,
  venues text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (conference_id, day_number),
  CHECK (day_number > 0)
);

CREATE TRIGGER conference_days_set_updated_at
BEFORE UPDATE ON conference_days
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE conference_tickets (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  ticket_key text NOT NULL,
  tier text NOT NULL,
  local_price integer NOT NULL DEFAULT 0,
  btc_price integer NOT NULL DEFAULT 0,
  usd_price integer NOT NULL DEFAULT 0,
  expires_start timestamptz,
  expires_end timestamptz,
  max_count integer NOT NULL DEFAULT 0,
  currency text NOT NULL DEFAULT '',
  symbol text NOT NULL DEFAULT '',
  post_symbol text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (conference_id, ticket_key),
  CHECK (ticket_key <> ''),
  CHECK (tier <> ''),
  CHECK (local_price >= 0),
  CHECK (btc_price >= 0),
  CHECK (usd_price >= 0),
  CHECK (max_count >= 0),
  CHECK (expires_end IS NULL OR expires_start IS NULL OR expires_end >= expires_start)
);

CREATE TRIGGER conference_tickets_set_updated_at
BEFORE UPDATE ON conference_tickets
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE speakers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  email citext,
  norm_photo_path text NOT NULL DEFAULT '',
  phone text NOT NULL DEFAULT '',
  signal text NOT NULL DEFAULT '',
  telegram text NOT NULL DEFAULT '',
  twitter_handle text NOT NULL DEFAULT '',
  nostr text NOT NULL DEFAULT '',
  github_url text NOT NULL DEFAULT '',
  instagram text NOT NULL DEFAULT '',
  linkedin text NOT NULL DEFAULT '',
  website_url text NOT NULL DEFAULT '',
  company text NOT NULL DEFAULT '',
  org_logo_path text NOT NULL DEFAULT '',
  avail_to_hire boolean NOT NULL DEFAULT false,
  looking_to_hire boolean NOT NULL DEFAULT false,
  tshirt text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (name <> '')
);

CREATE INDEX speakers_email_idx ON speakers (email);
CREATE INDEX speakers_name_idx ON speakers (lower(name));

CREATE TRIGGER speakers_set_updated_at
BEFORE UPDATE ON speakers
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE speaker_roles (
  speaker_id uuid NOT NULL REFERENCES speakers(id) ON DELETE CASCADE,
  scope text NOT NULL,
  position text NOT NULL,
  PRIMARY KEY (speaker_id, scope, position),
  CHECK (scope <> ''),
  CHECK (position <> '')
);

CREATE TABLE organizations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  tagline text NOT NULL DEFAULT '',
  logo_light_url text NOT NULL DEFAULT '',
  logo_dark_url text NOT NULL DEFAULT '',
  email citext,
  website_url text NOT NULL DEFAULT '',
  linkedin_url text NOT NULL DEFAULT '',
  instagram_url text NOT NULL DEFAULT '',
  youtube_url text NOT NULL DEFAULT '',
  github_url text NOT NULL DEFAULT '',
  twitter_handle text NOT NULL DEFAULT '',
  nostr text NOT NULL DEFAULT '',
  matrix text NOT NULL DEFAULT '',
  hiring boolean NOT NULL DEFAULT false,
  notes text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (name <> '')
);

CREATE UNIQUE INDEX organizations_name_key ON organizations (lower(name));

CREATE TRIGGER organizations_set_updated_at
BEFORE UPDATE ON organizations
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sponsorships (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
  name text NOT NULL DEFAULT '',
  level text NOT NULL DEFAULT '',
  label text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT '',
  is_vendor boolean NOT NULL DEFAULT false,
  notes text NOT NULL DEFAULT '',
  archived_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sponsorships_organization_idx ON sponsorships (organization_id);
CREATE INDEX sponsorships_status_level_idx ON sponsorships (status, level);

CREATE TRIGGER sponsorships_set_updated_at
BEFORE UPDATE ON sponsorships
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE sponsorship_conferences (
  sponsorship_id uuid NOT NULL REFERENCES sponsorships(id) ON DELETE CASCADE,
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  PRIMARY KEY (sponsorship_id, conference_id)
);

CREATE TABLE proposals (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid REFERENCES conferences(id) ON DELETE SET NULL,
  title text NOT NULL,
  description text NOT NULL DEFAULT '',
  setup text NOT NULL DEFAULT '',
  comments text NOT NULL DEFAULT '',
  talk_type text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT '',
  desired_duration_min integer NOT NULL DEFAULT 0,
  avail_duration_min integer NOT NULL DEFAULT 0,
  invite_token text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (title <> ''),
  CHECK (desired_duration_min >= 0),
  CHECK (avail_duration_min >= 0)
);

CREATE INDEX proposals_conference_status_idx ON proposals (conference_id, status);
CREATE INDEX proposals_invite_token_idx ON proposals (invite_token) WHERE invite_token <> '';

CREATE TRIGGER proposals_set_updated_at
BEFORE UPDATE ON proposals
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE speaker_confs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  speaker_id uuid NOT NULL REFERENCES speakers(id) ON DELETE CASCADE,
  organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
  coming_from text NOT NULL DEFAULT '',
  availability text[] NOT NULL DEFAULT '{}',
  record_ok text NOT NULL DEFAULT '',
  visa text NOT NULL DEFAULT '',
  first_event boolean NOT NULL DEFAULT false,
  dinner_rsvp boolean NOT NULL DEFAULT false,
  sponsor boolean NOT NULL DEFAULT false,
  company text NOT NULL DEFAULT '',
  org_photo_path text NOT NULL DEFAULT '',
  invited_at timestamptz,
  viewed_at timestamptz,
  accepted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (conference_id, speaker_id)
);

CREATE INDEX speaker_confs_speaker_idx ON speaker_confs (speaker_id);

CREATE TRIGGER speaker_confs_set_updated_at
BEFORE UPDATE ON speaker_confs
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE speaker_conf_other_events (
  speaker_conf_id uuid NOT NULL REFERENCES speaker_confs(id) ON DELETE CASCADE,
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  PRIMARY KEY (speaker_conf_id, conference_id)
);

CREATE TABLE proposal_speakers (
  proposal_id uuid NOT NULL REFERENCES proposals(id) ON DELETE CASCADE,
  speaker_conf_id uuid NOT NULL REFERENCES speaker_confs(id) ON DELETE CASCADE,
  PRIMARY KEY (proposal_id, speaker_conf_id)
);

CREATE TABLE conf_talks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  proposal_id uuid REFERENCES proposals(id) ON DELETE SET NULL,
  clipart_path text NOT NULL DEFAULT '',
  scheduled_start timestamptz,
  scheduled_end timestamptz,
  production_notes text NOT NULL DEFAULT '',
  venue text NOT NULL DEFAULT '',
  section text NOT NULL DEFAULT '',
  cal_notif text NOT NULL DEFAULT '',
  social_card_path text NOT NULL DEFAULT '',
  archived_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (proposal_id),
  CHECK (scheduled_end IS NULL OR scheduled_start IS NULL OR scheduled_end >= scheduled_start)
);

CREATE INDEX conf_talks_conference_schedule_idx ON conf_talks (conference_id, scheduled_start);

CREATE TRIGGER conf_talks_set_updated_at
BEFORE UPDATE ON conf_talks
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE recordings (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conf_talk_id uuid NOT NULL UNIQUE REFERENCES conf_talks(id) ON DELETE CASCADE,
  talk_name text NOT NULL DEFAULT '',
  youtube_url text NOT NULL DEFAULT '',
  x_url text NOT NULL DEFAULT '',
  x_reply_url text NOT NULL DEFAULT '',
  file_uri text NOT NULL DEFAULT '',
  publish_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TRIGGER recordings_set_updated_at
BEFORE UPDATE ON recordings
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE social_posts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ref text NOT NULL UNIQUE,
  text text NOT NULL DEFAULT '',
  posted_to text NOT NULL DEFAULT '',
  kind text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT '',
  recording_id uuid REFERENCES recordings(id) ON DELETE SET NULL,
  conf_talk_id uuid REFERENCES conf_talks(id) ON DELETE SET NULL,
  url text NOT NULL DEFAULT '',
  reply_url text NOT NULL DEFAULT '',
  error text NOT NULL DEFAULT '',
  error_fingerprint text NOT NULL DEFAULT '',
  scheduled_at timestamptz,
  posted_at timestamptz,
  notified_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (ref <> '')
);

CREATE INDEX social_posts_kind_status_idx ON social_posts (kind, status);

CREATE TRIGGER social_posts_set_updated_at
BEFORE UPDATE ON social_posts
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE hotels (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  name text NOT NULL,
  url text NOT NULL DEFAULT '',
  img_path text NOT NULL DEFAULT '',
  type text NOT NULL DEFAULT '',
  description text NOT NULL DEFAULT '',
  display_order integer NOT NULL DEFAULT 0,
  archived_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (name <> '')
);

CREATE INDEX hotels_conference_order_idx ON hotels (conference_id, display_order, name);

CREATE TRIGGER hotels_set_updated_at
BEFORE UPDATE ON hotels
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE discounts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  code_name citext NOT NULL UNIQUE,
  discount_expr text NOT NULL,
  uses_count integer NOT NULL DEFAULT 0,
  affiliate_email citext,
  disc_type char(1),
  amount integer,
  max_uses integer,
  extra_qty integer NOT NULL DEFAULT 0,
  valid_from timestamptz,
  valid_until timestamptz,
  archived_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (code_name <> ''),
  CHECK (uses_count >= 0),
  CHECK (disc_type IS NULL OR disc_type IN ('%', '$', '=')),
  CHECK (amount IS NULL OR amount >= 0),
  CHECK (max_uses IS NULL OR max_uses >= 0),
  CHECK (extra_qty >= 0)
);

CREATE TRIGGER discounts_set_updated_at
BEFORE UPDATE ON discounts
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE discount_conferences (
  discount_id uuid NOT NULL REFERENCES discounts(id) ON DELETE CASCADE,
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  PRIMARY KEY (discount_id, conference_id)
);

CREATE TABLE registrations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  ref_id text NOT NULL UNIQUE,
  checkout_id text NOT NULL DEFAULT '',
  conference_id uuid REFERENCES conferences(id) ON DELETE SET NULL,
  discount_id uuid REFERENCES discounts(id) ON DELETE SET NULL,
  type text NOT NULL DEFAULT '',
  email citext NOT NULL,
  item_bought text NOT NULL DEFAULT '',
  amount_paid numeric(12, 2),
  currency text NOT NULL DEFAULT '',
  platform text NOT NULL DEFAULT '',
  purchased_at timestamptz,
  checked_in_at timestamptz,
  revoked boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (ref_id <> ''),
  CHECK (email <> '')
);

CREATE INDEX registrations_conference_idx ON registrations (conference_id);
CREATE INDEX registrations_email_idx ON registrations (email);
CREATE INDEX registrations_checkout_idx ON registrations (checkout_id) WHERE checkout_id <> '';

CREATE TRIGGER registrations_set_updated_at
BEFORE UPDATE ON registrations
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE affiliate_usages (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  discount_id uuid REFERENCES discounts(id) ON DELETE SET NULL,
  conference_id uuid REFERENCES conferences(id) ON DELETE SET NULL,
  code_name_snapshot citext NOT NULL,
  affiliate_email citext NOT NULL,
  saved_sats bigint NOT NULL DEFAULT 0,
  earned_sats bigint NOT NULL DEFAULT 0,
  tickets_count integer NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK (code_name_snapshot <> ''),
  CHECK (affiliate_email <> ''),
  CHECK (tickets_count >= 0)
);

CREATE INDEX affiliate_usages_email_idx ON affiliate_usages (affiliate_email);
CREATE INDEX affiliate_usages_conference_idx ON affiliate_usages (conference_id);

CREATE TABLE job_types (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tag text NOT NULL UNIQUE,
  display_order integer NOT NULL DEFAULT 0,
  title text NOT NULL,
  tooltip text NOT NULL DEFAULT '',
  long_desc text NOT NULL DEFAULT '',
  show boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (tag <> ''),
  CHECK (title <> '')
);

CREATE TRIGGER job_types_set_updated_at
BEFORE UPDATE ON job_types
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE volunteers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name text NOT NULL,
  email citext NOT NULL,
  phone text NOT NULL DEFAULT '',
  signal text NOT NULL DEFAULT '',
  availability text[] NOT NULL DEFAULT '{}',
  contact_at text NOT NULL DEFAULT '',
  comments text NOT NULL DEFAULT '',
  discovered_via text NOT NULL DEFAULT '',
  first_event boolean NOT NULL DEFAULT false,
  hometown text NOT NULL DEFAULT '',
  twitter_handle text NOT NULL DEFAULT '',
  nostr text NOT NULL DEFAULT '',
  shirt text NOT NULL DEFAULT '',
  status text NOT NULL DEFAULT '',
  captcha integer NOT NULL DEFAULT 0,
  subscribe boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (name <> ''),
  CHECK (email <> '')
);

CREATE INDEX volunteers_email_idx ON volunteers (email);

CREATE TRIGGER volunteers_set_updated_at
BEFORE UPDATE ON volunteers
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE volunteer_conferences (
  volunteer_id uuid NOT NULL REFERENCES volunteers(id) ON DELETE CASCADE,
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  kind text NOT NULL,
  PRIMARY KEY (volunteer_id, conference_id, kind),
  CHECK (kind IN ('schedule_for', 'other_event'))
);

CREATE TABLE volunteer_job_preferences (
  volunteer_id uuid NOT NULL REFERENCES volunteers(id) ON DELETE CASCADE,
  job_type_id uuid NOT NULL REFERENCES job_types(id) ON DELETE CASCADE,
  preference text NOT NULL,
  PRIMARY KEY (volunteer_id, job_type_id, preference),
  CHECK (preference IN ('yes', 'no'))
);

CREATE TABLE work_shifts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid NOT NULL REFERENCES conferences(id) ON DELETE CASCADE,
  job_type_id uuid REFERENCES job_types(id) ON DELETE SET NULL,
  name text NOT NULL,
  max_vols integer NOT NULL DEFAULT 0,
  shift_start timestamptz,
  shift_end timestamptz,
  priority integer NOT NULL DEFAULT 0,
  cal_notif text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (name <> ''),
  CHECK (max_vols >= 0),
  CHECK (priority >= 0),
  CHECK (shift_end IS NULL OR shift_start IS NULL OR shift_end >= shift_start)
);

CREATE INDEX work_shifts_conference_time_idx ON work_shifts (conference_id, shift_start);

CREATE TRIGGER work_shifts_set_updated_at
BEFORE UPDATE ON work_shifts
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE work_shift_assignments (
  shift_id uuid NOT NULL REFERENCES work_shifts(id) ON DELETE CASCADE,
  volunteer_id uuid NOT NULL REFERENCES volunteers(id) ON DELETE CASCADE,
  role text NOT NULL DEFAULT 'assignee',
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (shift_id, volunteer_id, role),
  CHECK (role IN ('assignee', 'leader'))
);

CREATE UNIQUE INDEX work_shift_one_leader_idx
ON work_shift_assignments (shift_id)
WHERE role = 'leader';

CREATE TABLE vol_infos (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  conference_id uuid NOT NULL UNIQUE REFERENCES conferences(id) ON DELETE CASCADE,
  orient_link_url text NOT NULL DEFAULT '',
  orient_start timestamptz,
  orient_end timestamptz,
  notes text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (orient_end IS NULL OR orient_start IS NULL OR orient_end >= orient_start)
);

CREATE TRIGGER vol_infos_set_updated_at
BEFORE UPDATE ON vol_infos
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE subscribers (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email citext NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (email <> '')
);

CREATE TRIGGER subscribers_set_updated_at
BEFORE UPDATE ON subscribers
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE subscriber_subscriptions (
  subscriber_id uuid NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
  name text NOT NULL,
  PRIMARY KEY (subscriber_id, name),
  CHECK (name <> '')
);

CREATE TABLE missives (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  public_uid bigint UNIQUE,
  title text NOT NULL,
  newsletters text[] NOT NULL DEFAULT '{}',
  only_for text NOT NULL DEFAULT '',
  markdown text NOT NULL DEFAULT '',
  send_at_expr text NOT NULL DEFAULT '',
  sent_at timestamptz,
  expiry timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CHECK (title <> '')
);

CREATE INDEX missives_newsletters_idx ON missives USING gin (newsletters);
CREATE INDEX missives_only_for_idx ON missives (only_for) WHERE only_for <> '';

CREATE TRIGGER missives_set_updated_at
BEFORE UPDATE ON missives
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
