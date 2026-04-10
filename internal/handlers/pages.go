package handlers

import (
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
        Year          uint
}

type VolAdminPage struct {
        Conf          *types.Conf
        Volunteers    []*types.Volunteer
        Shifts        []*types.WorkShift
        StatusFilter  string
        Year          uint
}

