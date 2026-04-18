package handlers

import (
	"btcpp-web/internal/mtypes"
	"btcpp-web/internal/types"
)

type Day struct {
	Morning   []types.SessionTime
	Afternoon []types.SessionTime
	Evening   []types.SessionTime

	Idx int
}

func (d *Day) Venues() []string {
	venhash := make(map[string]string)

	all := make([]types.SessionTime, 0)
	all = append(all, d.Morning...)
	all = append(all, d.Afternoon...)
	all = append(all, d.Evening...)

	for _, list := range all {
		for _, sesh := range list {
			venhash[sesh.Venue] = ""
		}
	}

	venues := make([]string, len(venhash))
	i := 0
	for k, _ := range venhash {
		venues[i] = k
		i++
	}

	return venues
}


type ConfPage struct {
	Conf          *types.Conf
	Hotels        []*types.Hotel
	Tix           *types.ConfTicket
	MaxTix        *types.ConfTicket
	Sold          uint
	TixLeft       uint
	Talks         []*types.Talk
	EventSpeakers []*types.Speaker
	Buckets       map[string]types.SessionTime
	Days          []*Day
	Year          uint
}

type SuccessPage struct {
	Conf *types.Conf
	Year uint
}

type TixFormPage struct {
	Conf          *types.Conf
	Tix           *types.ConfTicket
	TixSlug       string
	Count         uint
	TixPrice      uint
	Discount      string
	DiscountPrice uint
	DiscountRef   string
	HMAC          string
	Err           string
	Year          uint
}

type SchedulePage struct {
	Talks []*types.Talk
	s     []types.TalkTime
}

type CheckInPage struct {
	NeedsPin   bool
	TicketType string
	Msg        string
	Year       uint
}


type VolunteerPage struct {
        Confs     []*types.Conf
        Conf      *types.Conf
        YesJobs   []types.CheckItem
        NoJobs    []types.CheckItem
        ConfItems []types.CheckItem
        DaysList  []types.CheckItem
        Year      uint
}

type SpeakerPage struct {
        Confs     []*types.Conf
        Conf      *types.Conf
        ConfItems []types.CheckItem
        DaysList  []types.CheckItem
        DueDate   string
        RSVPFor   string
        PresentationType []types.CheckItem
        Year      uint
}

type ApplicationStats struct {
        Applied int
        Pending int
        Accepted int
        TotalShifts int
}

type VolShiftPage struct {
        Name      string
        Hometown  string
        Email     string
        HMAC      string
        VolApps   []*types.Volunteer
        Stats     *ApplicationStats
        Confs     []*types.Conf
        VolInfos  map[string]*types.VolInfo
        Year      uint
}

type ShiftDisplay struct {
        Shift       *types.WorkShift
        IsAvailable bool   // Vol available on that day
        IsEligible  bool   // Not on WillNotWork list
        IsFull      bool   // No spots left
        IsSelected  bool   // Already assigned
        Conflicts   bool   // Overlaps with selected shift
        CanSelect   bool   // Computed eligibility
        Reason      string // Why can't select
}

type ShiftSignupPage struct {
        Vol           *types.Volunteer
        Conf          *types.Conf
        AvailShifts   map[string][]*ShiftDisplay // Grouped by day
        SelectedShifts []*types.WorkShift
        MinShifts     int
        ShiftProgress int
        CanSubmit     bool
        ConfRef       string
        Email         string
        HMAC          string
        DaysList      []types.CheckItem
        YesJobs       []types.CheckItem
        NoJobs        []types.CheckItem
        Year          uint
}

type VolAdminPage struct {
        Conf          *types.Conf
        Volunteers    []*types.Volunteer
        Shifts        []*types.WorkShift
        StatusFilter  string
        Missives      []*mtypes.Letter
        FlashMessage  string
        Year          uint
}

type ShiftDayGroup struct {
        Date     string             // "01/02/2006"
        DateDesc string             // "Mon. Jan 2"
        MinHour  int                // earliest start hour across this day's shifts
        MaxHour  int                // latest end hour
        Shifts   []*types.WorkShift // sorted by start time
}

type VolAdminShiftsPage struct {
        Conf     *types.Conf
        Days     []*ShiftDayGroup
        VolMap   map[string]*types.Volunteer // ref → volunteer for assignee resolution
        JobTypes []*types.JobType
        DaysList []types.CheckItem // for shift form day selector
        Year     uint
}

type GiftRow struct {
        Clipart     string
        SpeakerName string
}

type SpeakerRow struct {
        Name  string
        Email string
}

type SpeakerAdminPage struct {
        Conf          *types.Conf
        Rows          []*SpeakerRow
        FlashMessage  string
        Year          uint
}

type SocialSpeakerRow struct {
        ID              string
        Name            string
        TwitterHandle   string
        TalkName        string
        SpeakerPhotoURL string
        PhotoURL        string
        InstaPhotoURL   string
        PostText        string
}

type SocialTalkRow struct {
        ID           string
        Name         string
        SpeakerNames string
        PostText     string
        PhotoURL     string
}

type SocialAdminPage struct {
        Conf         *types.Conf
        SpeakerRows  []*SocialSpeakerRow
        TalkRows     []*SocialTalkRow
        FlashMessage string
        Year         uint
        BufferOK     bool
}

type TalksGiftsPage struct {
        Confs         []*types.Conf
        Conf          *types.Conf
        Rows          []*GiftRow
        FilePath      string
        Year          uint
}

type VolDetailsPage struct {
        Conf            *types.Conf
        Vol             *types.Volunteer
        AllShifts       []*types.WorkShift
        ShiftDisplays   map[string][]*ShiftDisplay
        SelectedShifts  []*types.WorkShift
        DayKeys         []string
        JobTypes        []*types.JobType
        YesJobs         []types.CheckItem
        NoJobs          []types.CheckItem
        DaysList        []types.CheckItem
        Statuses        []string
        Year            uint
}

