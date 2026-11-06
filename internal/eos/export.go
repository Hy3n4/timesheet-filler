package eos

import (
	"sort"
	"time"

	"timesheet-filler/internal/models"
)

// Port of eos-club-api attendance_export.build_rows, kept to the same rules
// so the "Load from EOS" rows match the exported workbook.
const (
	Unsaved      = "docházka neuložena"
	NotConfirmed = "nominace nepotvrzena"
	NotNominated = "bez nominace"
)

// Row is one event of the member in the month.
type Row struct {
	Event      Event
	Attendance *Attendance
	Start, End time.Time
	// Tracked events (e.g. trainings) have attendance a trainer saves;
	// nominations and other actions do not.
	Tracked bool
	// Possible holds why an event may count although EOS cannot confirm it.
	Possible string
}

func (r Row) Saved() bool {
	return !r.Tracked || (r.Attendance != nil && r.Attendance.StatedAt != nil)
}

func (r Row) Attended() bool { return r.Possible == "" && r.Attendance.attended() }

// BuildRows joins the member's attendances with the month's events: tracked
// events the member has a row for (saved or not), confirmed nominations, and
// possible events (unconfirmed nominations, applications, actions of teams
// the member coaches that month).
func BuildRows(m Month) ([]Row, error) {
	mine := make(map[int64]*Attendance, len(m.Attendances))
	for i := range m.Attendances {
		mine[m.Attendances[i].EventID] = &m.Attendances[i]
	}
	tracked := make(map[int64]bool, len(m.EventTypes))
	for _, t := range m.EventTypes {
		tracked[t.ID] = t.HasAttendance
	}

	type dated struct {
		event      Event
		start, end time.Time
	}
	var month []dated
	for _, e := range m.Events {
		start, end, err := e.span()
		if err != nil {
			return nil, err
		}
		if start.Year() == m.Year && start.Month() == m.Month {
			month = append(month, dated{e, start, end})
		}
	}

	coached := map[int64]bool{}
	for _, d := range month {
		a := mine[d.event.ID]
		if a != nil && (tracked[d.event.EventTypeID] || confirmedNomination(d.event, a)) {
			for _, t := range d.event.TeamIDs {
				coached[t] = true
			}
		}
	}

	var rows []Row
	for _, d := range month {
		a := mine[d.event.ID]
		row := Row{Event: d.event, Attendance: a, Start: d.start, End: d.end}
		switch {
		case tracked[d.event.EventTypeID]:
			if a != nil {
				row.Tracked = true
				rows = append(rows, row)
			}
		case confirmedNomination(d.event, a):
			rows = append(rows, row)
		case possible(d.event, a, coached):
			row.Possible = NotNominated
			if a != nil && d.event.Purpose == PurposeNomination {
				row.Possible = NotConfirmed
			}
			rows = append(rows, row)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Start.Equal(rows[j].Start) {
			return rows[i].Start.Before(rows[j].Start)
		}
		return rows[i].Event.ID < rows[j].Event.ID
	})
	return rows, nil
}

func confirmedNomination(e Event, a *Attendance) bool {
	return e.Purpose == PurposeNomination && a.attended()
}

func possible(e Event, a *Attendance, coached map[int64]bool) bool {
	if !e.active() || e.CancelledAt != nil {
		return false
	}
	if a != nil {
		return true
	}
	for team := range coached {
		if e.concerns(team) {
			return true
		}
	}
	return false
}

// TableRows turns rows into the edit page's rows the same way the filler reads
// the exported workbook: attended rows are entries (unsaved ones marked in the
// note), possible rows are optional, everything else is left out.
func TableRows(rows []Row) []models.TableRow {
	var out []models.TableRow
	for _, r := range rows {
		t := models.TableRow{
			Date:      r.Start.Format("2006-01-02"),
			StartTime: r.Start.Format("15:04"),
			EndTime:   r.End.Format("15:04"),
			Note:      r.Event.Name,
		}
		switch {
		case r.Possible != "":
			t.Optional = true
			t.Reason = r.Possible
		case r.Attended():
			if !r.Saved() {
				t.Note += " (" + Unsaved + ")"
			}
		default:
			continue
		}
		out = append(out, t)
	}
	return out
}
