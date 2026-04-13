package volunteers

import (
	"testing"
	"time"

	"btcpp-web/internal/types"
)

// helper to build a time on a given date at hour:minute
func tm(year, month, day, hour, min int) time.Time {
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, time.UTC)
}

func tmPtr(year, month, day, hour, min int) *time.Time {
	t := tm(year, month, day, hour, min)
	return &t
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func mkShift(ref, name string, maxVols, priority uint, start time.Time, end *time.Time, jobType *types.JobType) *types.WorkShift {
	return &types.WorkShift{
		Ref:          ref,
		Name:         name,
		MaxVols:      maxVols,
		Priority:     priority,
		ShiftTime:    &types.Times{Start: start, End: end},
		Type:         jobType,
		AssigneesRef: []string{},
	}
}

func mkVol(ref string, availability []string, workNo []*types.JobType, created time.Time) *types.Volunteer {
	ct := created
	return &types.Volunteer{
		Ref:          ref,
		Name:         ref,
		Availability: availability,
		WorkNo:       workNo,
		WorkShifts:   []*types.WorkShift{},
		CreatedAt:    &ct,
	}
}

// noopAssign is a test AssignFunc that always succeeds (in-memory only).
func noopAssign(volRef, shiftRef string) error {
	return nil
}

// recordingAssign captures every assignment for assertions.
type assignment struct {
	VolRef, ShiftRef string
}

func recordingAssign(log *[]assignment) AssignFunc {
	return func(volRef, shiftRef string) error {
		*log = append(*log, assignment{volRef, shiftRef})
		return nil
	}
}

// --- Tests ---

func TestFindEligibleVols_BasicFiltering(t *testing.T) {
	jobA := &types.JobType{Ref: "jobA", Tag: "door"}
	jobB := &types.JobType{Ref: "jobB", Tag: "snacks"}

	shift := mkShift("s1", "Morning Door", 2, 1,
		tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), jobA)

	// Available on 06/10/2026, no exclusions
	volOK := mkVol("v1", []string{"06/10/2026"}, nil, tm(2026, 1, 1, 0, 0))
	// Not available on that day
	volWrongDay := mkVol("v2", []string{"06/11/2026"}, nil, tm(2026, 1, 1, 0, 0))
	// Available but excludes jobA
	volExcluded := mkVol("v3", []string{"06/10/2026"}, []*types.JobType{jobA}, tm(2026, 1, 1, 0, 0))
	// Available but already has 3 shifts
	volFull := mkVol("v4", []string{"06/10/2026"}, nil, tm(2026, 1, 1, 0, 0))
	volFull.WorkShifts = []*types.WorkShift{
		mkShift("x1", "x", 1, 0, tm(2026, 6, 10, 14, 0), tmPtr(2026, 6, 10, 18, 0), nil),
		mkShift("x2", "x", 1, 0, tm(2026, 6, 11, 9, 0), tmPtr(2026, 6, 11, 13, 0), nil),
		mkShift("x3", "x", 1, 0, tm(2026, 6, 11, 14, 0), tmPtr(2026, 6, 11, 18, 0), nil),
	}
	// Available but has an overlapping shift
	volConflict := mkVol("v5", []string{"06/10/2026"}, nil, tm(2026, 1, 1, 0, 0))
	volConflict.WorkShifts = []*types.WorkShift{
		mkShift("x4", "overlap", 1, 0, tm(2026, 6, 10, 10, 0), tmPtr(2026, 6, 10, 14, 0), nil),
	}

	vols := []*types.Volunteer{volOK, volWrongDay, volExcluded, volFull, volConflict}

	eligible := findEligibleVols(shift, vols, false)
	if len(eligible) != 1 || eligible[0].Ref != "v1" {
		t.Errorf("expected only v1 eligible, got %d: %v", len(eligible), refsOf(eligible))
	}

	// With relaxType=true, v3 (excluded job type) should also be eligible
	eligibleRelaxed := findEligibleVols(shift, vols, true)
	if len(eligibleRelaxed) != 2 {
		t.Errorf("expected v1 and v3 eligible with relaxType, got %d: %v", len(eligibleRelaxed), refsOf(eligibleRelaxed))
	}

	// jobB shift should make v3 eligible even without relaxing
	shiftB := mkShift("s2", "Snacks", 2, 1,
		tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), jobB)
	eligibleB := findEligibleVols(shiftB, []*types.Volunteer{volExcluded}, false)
	if len(eligibleB) != 1 {
		t.Errorf("v3 should be eligible for jobB shift, got %d", len(eligibleB))
	}
}

func TestAssignShifts_BasicDistribution(t *testing.T) {
	// 3 shifts on 2 days, 2 volunteers available on both days.
	// The sort prioritizes vols with the most shifts (fills one to target
	// before starting the next), so with only 3 slots the first vol gets
	// all 3 and the second gets 0.
	s1 := mkShift("s1", "Day1 AM", 1, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)
	s2 := mkShift("s2", "Day1 PM", 1, 1, tm(2026, 6, 10, 14, 0), tmPtr(2026, 6, 10, 18, 0), nil)
	s3 := mkShift("s3", "Day2 AM", 1, 1, tm(2026, 6, 11, 9, 0), tmPtr(2026, 6, 11, 13, 0), nil)

	v1 := mkVol("v1", []string{"06/10/2026", "06/11/2026"}, nil, tm(2026, 1, 1, 0, 0))
	v2 := mkVol("v2", []string{"06/10/2026", "06/11/2026"}, nil, tm(2026, 1, 2, 0, 0))

	var log []assignment
	shifts := []*types.WorkShift{s1, s2, s3}
	vols := []*types.Volunteer{v1, v2}

	err := assignShiftsCore(vols, shifts, recordingAssign(&log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// v1 should get all 3 shifts (earliest created, fills to target first)
	if len(v1.WorkShifts) != 3 {
		t.Errorf("v1 should have 3 shifts, got %d", len(v1.WorkShifts))
	}

	// Total assignments should be 3 (one per shift slot)
	if len(log) != 3 {
		t.Errorf("expected 3 total assignments, got %d", len(log))
	}
}

func TestAssignShifts_RespectsMaxVols(t *testing.T) {
	// 1 shift with MaxVols=2, 3 available volunteers
	s := mkShift("s1", "Only Two", 2, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)

	v1 := mkVol("v1", []string{"06/10/2026"}, nil, tm(2026, 1, 1, 0, 0))
	v2 := mkVol("v2", []string{"06/10/2026"}, nil, tm(2026, 1, 2, 0, 0))
	v3 := mkVol("v3", []string{"06/10/2026"}, nil, tm(2026, 1, 3, 0, 0))

	var log []assignment
	err := assignShiftsCore([]*types.Volunteer{v1, v2, v3}, []*types.WorkShift{s}, recordingAssign(&log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log) != 2 {
		t.Errorf("expected 2 assignments (MaxVols=2), got %d", len(log))
	}
}

func TestAssignShifts_RespectsTargetShiftsPerVol(t *testing.T) {
	// 4 shifts, 1 volunteer — should only be assigned 3
	s1 := mkShift("s1", "AM1", 1, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)
	s2 := mkShift("s2", "PM1", 1, 1, tm(2026, 6, 10, 14, 0), tmPtr(2026, 6, 10, 18, 0), nil)
	s3 := mkShift("s3", "AM2", 1, 1, tm(2026, 6, 11, 9, 0), tmPtr(2026, 6, 11, 13, 0), nil)
	s4 := mkShift("s4", "PM2", 1, 1, tm(2026, 6, 11, 14, 0), tmPtr(2026, 6, 11, 18, 0), nil)

	v := mkVol("v1", []string{"06/10/2026", "06/11/2026"}, nil, tm(2026, 1, 1, 0, 0))

	var log []assignment
	err := assignShiftsCore([]*types.Volunteer{v}, []*types.WorkShift{s1, s2, s3, s4}, recordingAssign(&log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(v.WorkShifts) != 3 {
		t.Errorf("expected vol to have 3 shifts (target), got %d", len(v.WorkShifts))
	}
	if len(log) != 3 {
		t.Errorf("expected 3 assignments, got %d", len(log))
	}
}

func TestAssignShifts_PriorityOrder(t *testing.T) {
	// High-priority shift should be filled first.
	// 1 volunteer, 2 shifts on same time slot (can only do 1). High-pri wins.
	sLow := mkShift("lo", "LowPri", 1, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)
	sHigh := mkShift("hi", "HighPri", 1, 10, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)

	v := mkVol("v1", []string{"06/10/2026"}, nil, tm(2026, 1, 1, 0, 0))

	var log []assignment
	// Pass low-pri first; algorithm should sort by priority and assign high-pri
	err := assignShiftsCore([]*types.Volunteer{v}, []*types.WorkShift{sLow, sHigh}, recordingAssign(&log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log) != 1 {
		t.Fatalf("expected 1 assignment, got %d", len(log))
	}
	if log[0].ShiftRef != "hi" {
		t.Errorf("expected high-priority shift 'hi' to be assigned, got %q", log[0].ShiftRef)
	}
}

func TestAssignShifts_SkipsFullShifts(t *testing.T) {
	// Shift already at capacity should be skipped
	s := mkShift("s1", "Full", 1, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)
	s.AssigneesRef = []string{"someone-else"}

	v := mkVol("v1", []string{"06/10/2026"}, nil, tm(2026, 1, 1, 0, 0))

	var log []assignment
	err := assignShiftsCore([]*types.Volunteer{v}, []*types.WorkShift{s}, recordingAssign(&log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log) != 0 {
		t.Errorf("expected 0 assignments for full shift, got %d", len(log))
	}
}

func TestAssignShifts_NoOverlappingShifts(t *testing.T) {
	// Two overlapping shifts — vol should only get one
	s1 := mkShift("s1", "AM", 1, 2, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)
	s2 := mkShift("s2", "AM-overlap", 1, 1, tm(2026, 6, 10, 10, 0), tmPtr(2026, 6, 10, 14, 0), nil)

	v := mkVol("v1", []string{"06/10/2026"}, nil, tm(2026, 1, 1, 0, 0))

	var log []assignment
	err := assignShiftsCore([]*types.Volunteer{v}, []*types.WorkShift{s1, s2}, recordingAssign(&log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log) != 1 {
		t.Errorf("expected 1 assignment (overlap prevents 2nd), got %d", len(log))
	}
	if log[0].ShiftRef != "s1" {
		t.Errorf("higher priority s1 should be assigned, got %q", log[0].ShiftRef)
	}
}

func TestAssignShifts_RelaxedTypeFallback(t *testing.T) {
	// Vol excludes jobA. Shift with jobA and MaxVols=1.
	// Strict pass finds nobody. Relaxed pass should assign the vol anyway.
	jobA := &types.JobType{Ref: "jobA", Tag: "door"}
	s := mkShift("s1", "Door", 1, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), jobA)
	v := mkVol("v1", []string{"06/10/2026"}, []*types.JobType{jobA}, tm(2026, 1, 1, 0, 0))

	var log []assignment
	err := assignShiftsCore([]*types.Volunteer{v}, []*types.WorkShift{s}, recordingAssign(&log))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log) != 1 {
		t.Errorf("expected 1 assignment via relaxed fallback, got %d", len(log))
	}
}

func TestAssignShifts_FairDistribution(t *testing.T) {
	// 6 shifts (3 days x 2 slots), 2 volunteers both available all days.
	// Each vol should end up with 3 shifts (the target).
	shifts := []*types.WorkShift{
		mkShift("d1am", "D1 AM", 1, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil),
		mkShift("d1pm", "D1 PM", 1, 1, tm(2026, 6, 10, 14, 0), tmPtr(2026, 6, 10, 18, 0), nil),
		mkShift("d2am", "D2 AM", 1, 1, tm(2026, 6, 11, 9, 0), tmPtr(2026, 6, 11, 13, 0), nil),
		mkShift("d2pm", "D2 PM", 1, 1, tm(2026, 6, 11, 14, 0), tmPtr(2026, 6, 11, 18, 0), nil),
		mkShift("d3am", "D3 AM", 1, 1, tm(2026, 6, 12, 9, 0), tmPtr(2026, 6, 12, 13, 0), nil),
		mkShift("d3pm", "D3 PM", 1, 1, tm(2026, 6, 12, 14, 0), tmPtr(2026, 6, 12, 18, 0), nil),
	}

	v1 := mkVol("v1", []string{"06/10/2026", "06/11/2026", "06/12/2026"}, nil, tm(2026, 1, 1, 0, 0))
	v2 := mkVol("v2", []string{"06/10/2026", "06/11/2026", "06/12/2026"}, nil, tm(2026, 1, 2, 0, 0))

	err := assignShiftsCore([]*types.Volunteer{v1, v2}, shifts, noopAssign)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(v1.WorkShifts) != 3 {
		t.Errorf("v1 should have 3 shifts, got %d", len(v1.WorkShifts))
	}
	if len(v2.WorkShifts) != 3 {
		t.Errorf("v2 should have 3 shifts, got %d", len(v2.WorkShifts))
	}
}

func TestSortingShiftsByPriority(t *testing.T) {
	s1 := mkShift("lo", "Lo", 1, 1, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)
	s2 := mkShift("hi", "Hi", 1, 10, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)
	s3 := mkShift("mid", "Mid", 1, 5, tm(2026, 6, 10, 9, 0), tmPtr(2026, 6, 10, 13, 0), nil)

	shifts := shiftsByPriority{s1, s2, s3}
	if shifts.Less(0, 1) {
		t.Error("lo should not be less than hi")
	}
	if !shifts.Less(1, 0) {
		t.Error("hi should be less than lo (sorted first)")
	}
}

// helper
func refsOf(vols []*types.Volunteer) []string {
	refs := make([]string, len(vols))
	for i, v := range vols {
		refs[i] = v.Ref
	}
	return refs
}
