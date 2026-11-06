package eos

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"timesheet-filler/internal/models"
)

// Fixtures mirror eos-club-api tests/test_attendance_export.py so both ports
// of build_rows are checked against the same cases.

func ev(id int64, from, to string, typeID int64, purpose Purpose) Event {
	return Event{
		ID: id, Name: "U11 – hala", EventTypeID: typeID, Purpose: purpose,
		Range: Range{From: from, To: to}, TeamIDs: []int64{85}, RelatedTeamIDs: []int64{53, 85},
	}
}

func att(eventID int64, evaluated *bool, statedAt *string) Attendance {
	return Attendance{ID: 1000 + eventID, EventID: eventID, MemberID: 329, EvaluatedAttendance: evaluated, StatedAt: statedAt}
}

func ptr[T any](v T) *T { return &v }

func october() Month {
	other := ev(12, "2026-10-20 08:00:00", "2026-10-20 12:00:00", 2, PurposeNone)
	other.TeamIDs, other.RelatedTeamIDs = []int64{99}, nil
	cancelled := ev(13, "2026-10-21 08:00:00", "2026-10-21 12:00:00", 2, PurposeNone)
	cancelled.CancelledAt = ptr("2026-10-01 10:00:00")
	draft := ev(14, "2026-10-23 08:00:00", "2026-10-23 12:00:00", 2, PurposeNone)
	draft.IsActive = ptr(false)
	saved := "2026-10-20 10:00:00"
	return Month{
		Year: 2026, Month: 10,
		EventTypes: []EventType{{1, "Trénink", true}, {2, "Turnaj", false}, {3, "Jiná", false}},
		Events: []Event{
			ev(1, "2026-10-14 17:30:00", "2026-10-14 19:00:00", 1, PurposeNone), // saved, ne
			ev(2, "2026-10-15 16:00:00", "2026-10-15 17:30:00", 1, PurposeNone), // unsaved, replied ano
			ev(3, "2026-10-22 16:00:00", "2026-10-22 17:30:00", 1, PurposeNone), // saved, ano
			ev(4, "2026-10-25 15:30:00", "2026-10-25 19:00:00", 3, PurposeNone), // invited to Jiná
			ev(5, "2026-09-30 16:00:00", "2026-09-30 17:30:00", 1, PurposeNone), // other month
			ev(6, "2026-10-29 16:00:00", "2026-10-29 17:30:00", 1, PurposeNone), // not invited
			ev(7, "2026-10-30 16:00:00", "2026-10-30 17:30:00", 1, PurposeNone), // unsaved, no reply
			ev(8, "2026-10-17 08:00:00", "2026-10-17 13:00:00", 2, PurposeNomination),
			ev(9, "2026-10-18 19:00:00", "2026-10-18 21:00:00", 2, PurposeApplication),
			ev(10, "2026-10-24 08:00:00", "2026-10-24 13:00:00", 2, PurposeNomination),
			ev(11, "2026-10-19 08:00:00", "2026-10-19 12:00:00", 2, PurposeNomination),
			other, cancelled, draft,
		},
		Attendances: []Attendance{
			att(3, ptr(true), ptr("2026-10-22 18:24:07")),
			att(2, ptr(true), nil),
			att(1, ptr(false), ptr("2026-10-14 17:34:34")),
			att(4, ptr(true), &saved),
			att(5, ptr(true), ptr("2026-09-30 18:00:00")),
			att(7, nil, nil),
			att(8, ptr(true), ptr("2026-10-10 09:00:00")),
			att(9, ptr(true), ptr("2026-10-17 12:00:00")),
			att(10, ptr(false), ptr("2026-10-20 09:00:00")),
		},
	}
}

type summary struct {
	id       int64
	attended bool
	saved    bool
	possible string
}

func TestBuildRowsMatchesThePythonExport(t *testing.T) {
	rows, err := BuildRows(october())
	require.NoError(t, err)
	var got []summary
	for _, r := range rows {
		got = append(got, summary{r.Event.ID, r.Attended(), r.Saved(), r.Possible})
	}
	assert.Equal(t, []summary{
		{1, false, true, ""},
		{2, true, false, ""},
		{8, true, true, ""},
		{9, false, true, NotNominated},
		{11, false, true, NotNominated},
		{3, true, true, ""},
		{10, false, true, NotConfirmed},
		{4, false, true, NotNominated},
		{7, false, false, ""},
	}, got)
}

func TestTableRowsMatchWhatTheFillerReadsFromTheWorkbook(t *testing.T) {
	rows, err := BuildRows(october())
	require.NoError(t, err)
	assert.Equal(t, []models.TableRow{
		{Date: "2026-10-15", StartTime: "16:00", EndTime: "17:30", Note: "U11 – hala (docházka neuložena)"},
		{Date: "2026-10-17", StartTime: "08:00", EndTime: "13:00", Note: "U11 – hala"},
		{Date: "2026-10-18", StartTime: "19:00", EndTime: "21:00", Note: "U11 – hala", Optional: true, Reason: NotNominated},
		{Date: "2026-10-19", StartTime: "08:00", EndTime: "12:00", Note: "U11 – hala", Optional: true, Reason: NotNominated},
		{Date: "2026-10-22", StartTime: "16:00", EndTime: "17:30", Note: "U11 – hala"},
		{Date: "2026-10-24", StartTime: "08:00", EndTime: "13:00", Note: "U11 – hala", Optional: true, Reason: NotConfirmed},
		{Date: "2026-10-25", StartTime: "15:30", EndTime: "19:00", Note: "U11 – hala", Optional: true, Reason: NotNominated},
	}, TableRows(rows))
}

func TestBuildRowsRejectsUnparsableTimes(t *testing.T) {
	m := october()
	m.Events[0].Range.From = "2026-10-14T17:30:00+02:00"
	_, err := BuildRows(m)
	assert.Error(t, err)
}
