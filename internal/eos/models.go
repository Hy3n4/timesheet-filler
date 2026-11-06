// Package eos talks to the EOS club platform API: member login, token
// refresh and the read-only attendance data behind a coach's timesheet.
package eos

import (
	"fmt"
	"time"
)

// EOS sends naive local (Europe/Prague) times as "YYYY-MM-DD HH:MM:SS".
const timeLayout = "2006-01-02 15:04:05"

type Purpose string

const (
	PurposeNone        Purpose = "none"
	PurposeNomination  Purpose = "nomination"
	PurposeApplication Purpose = "application"
	PurposeOrganizing  Purpose = "organizing"
)

type Range struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type Event struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	EventTypeID    int64   `json:"event_type_id"`
	Purpose        Purpose `json:"purpose"`
	Range          Range   `json:"range"`
	TeamIDs        []int64 `json:"team_ids"`
	RelatedTeamIDs []int64 `json:"related_team_ids"`
	// A nil IsActive means the field was absent; EOS events are active by default.
	IsActive    *bool   `json:"is_active"`
	CancelledAt *string `json:"cancelled_at"`
}

func (e Event) active() bool { return e.IsActive == nil || *e.IsActive }

func (e Event) concerns(team int64) bool {
	for _, t := range e.TeamIDs {
		if t == team {
			return true
		}
	}
	for _, t := range e.RelatedTeamIDs {
		if t == team {
			return true
		}
	}
	return false
}

func (e Event) span() (time.Time, time.Time, error) {
	start, err := time.Parse(timeLayout, e.Range.From)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("event %d start: %w", e.ID, err)
	}
	end, err := time.Parse(timeLayout, e.Range.To)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("event %d end: %w", e.ID, err)
	}
	return start, end, nil
}

// Attendance is one member's row of one event. StatedAt is set once a
// trainer saved the event's attendance; EvaluatedAttendance is EOS's verdict
// (the saved attendance, else the member's reply, else null).
type Attendance struct {
	ID                  int64   `json:"id"`
	EventID             int64   `json:"event_id"`
	MemberID            int64   `json:"member_id"`
	EvaluatedAttendance *bool   `json:"evaluated_attendance"`
	AttendanceTypeID    *int64  `json:"attendance_type_id"`
	StatedAt            *string `json:"stated_at"`
}

func (a *Attendance) attended() bool {
	return a != nil && a.EvaluatedAttendance != nil && *a.EvaluatedAttendance
}

type EventType struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	HasAttendance bool   `json:"has_attendance"`
}

// Month is everything needed to build one member's rows for one month.
type Month struct {
	Year        int
	Month       time.Month
	Attendances []Attendance
	Events      []Event
	EventTypes  []EventType
}
