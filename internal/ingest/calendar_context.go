package ingest

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UpcomingEvent is one studio event (materialized in events, or expanded from a
// recurring event_series) inside the lookahead window, formatted for LLM context.
type UpcomingEvent struct {
	Date        time.Time // calendar date, UTC midnight (date-only math space)
	Start, End  string    // "21:00"
	Title       string
	EventType   string
	Location    string
	Description string
	IsCancelled bool
	FromSeries  bool
}

// StudioTZ returns the studio's local timezone (env CALENDAR_TZ, default America/Denver).
// Only used to compute "today" — all date math below happens in UTC-midnight space.
func StudioTZ() *time.Location {
	name := os.Getenv("CALENDAR_TZ")
	if name == "" {
		name = "America/Denver"
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		log.Printf("[calendar] unknown CALENDAR_TZ %q, using fixed UTC-6", name)
		return time.FixedZone("MDT", -6*3600)
	}
	return loc
}

// CalendarLookaheadDays returns how many days of events to include (env CALENDAR_LOOKAHEAD_DAYS, default 45).
func CalendarLookaheadDays() int { return calendarEnvInt("CALENDAR_LOOKAHEAD_DAYS", 45, 7, 365) }

// CalendarMaxEvents caps how many events are injected per prompt (env CALENDAR_MAX_EVENTS, default 50).
func CalendarMaxEvents() int { return calendarEnvInt("CALENDAR_MAX_EVENTS", 50, 1, 200) }

func calendarEnvInt(name string, def, min, max int) int {
	s := strings.TrimSpace(os.Getenv(name))
	if s == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// LoadUpcomingEvents fetches materialized events in the window plus synthetic
// occurrences of active weekly series (so the LLM can answer recurring-class
// questions even before the studio materializes each date). Cancelled events are
// kept and marked — the LLM must be able to say "that event was cancelled".
func LoadUpcomingEvents(ctx context.Context, pool *pgxpool.Pool, lookaheadDays int) ([]UpcomingEvent, error) {
	now := time.Now().In(StudioTZ())
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, lookaheadDays)

	var events []UpcomingEvent
	materialized := map[string]bool{} // seriesID|date -> true, to dedupe series expansion

	rows, err := pool.Query(ctx, `
		select e.event_date, e.start_time::text, e.end_time::text, e.title,
		       e.event_type, e.location, coalesce(e.description, ''), e.is_cancelled,
		       coalesce(e.series_id::text, '')
		from public.events e
		where e.event_date >= $1::date and e.event_date <= $2::date
		order by e.event_date, e.start_time nulls last, e.title
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	for rows.Next() {
		var d time.Time
		var st, et, title, etype, locS, desc, seriesID string
		var cancelled bool
		if err := rows.Scan(&d, &st, &et, &title, &etype, &locS, &desc, &cancelled, &seriesID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan event: %w", err)
		}
		if seriesID != "" {
			materialized[seriesID+"|"+d.Format("2006-01-02")] = true
		}
		events = append(events, UpcomingEvent{
			Date: d, Start: shortTime(st), End: shortTime(et),
			Title: title, EventType: etype, Location: locS, Description: desc,
			IsCancelled: cancelled,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	seriesRows, err := pool.Query(ctx, `
		select s.id::text, s.title, coalesce(s.description, ''), s.event_type, s.location,
		       s.start_time::text, s.end_time::text, s.recurrence_days,
		       s.series_start_date, s.series_end_date, s.max_occurrences
		from public.event_series s
		where lower(coalesce(s.recurrence_pattern, '')) = 'weekly'
		  and s.series_start_date <= $2::date
		  and (s.series_end_date is null or s.series_end_date >= $1::date)
		order by s.series_start_date
	`, start, end)
	if err != nil {
		return nil, fmt.Errorf("query series: %w", err)
	}
	for seriesRows.Next() {
		var id, title, desc, etype, locS, st, et string
		var days []string
		var sStart time.Time
		var sEnd *time.Time
		var maxOcc *int
		if err := seriesRows.Scan(&id, &title, &desc, &etype, &locS, &st, &et, &days, &sStart, &sEnd, &maxOcc); err != nil {
			seriesRows.Close()
			return nil, fmt.Errorf("scan series: %w", err)
		}
		weekdays := normalizeDays(days)
		if len(weekdays) == 0 {
			continue
		}
		lifeStart := sStart
		lifeEnd := end
		if sEnd != nil {
			if sEnd.Before(start) {
				continue
			}
			if sEnd.Before(end) {
				lifeEnd = *sEnd
			}
		}
		count := 0
		for d := lifeStart; !d.After(lifeEnd); d = d.AddDate(0, 0, 1) {
			if !weekdays[d.Weekday()] {
				continue
			}
			count++
			if maxOcc != nil && count > *maxOcc {
				break
			}
			if d.Before(start) || d.After(end) {
				continue
			}
			if materialized[id+"|"+d.Format("2006-01-02")] {
				continue // already a concrete event row for this series+date
			}
			events = append(events, UpcomingEvent{
				Date: d, Start: shortTime(st), End: shortTime(et),
				Title: title, EventType: etype, Location: locS, Description: desc,
				FromSeries: true,
			})
		}
	}
	seriesRows.Close()
	if err := seriesRows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(events, func(i, j int) bool {
		if !events[i].Date.Equal(events[j].Date) {
			return events[i].Date.Before(events[j].Date)
		}
		return events[i].Start < events[j].Start
	})
	return events, nil
}

// FormatUpcomingEvents renders events as a compact context block for the LLM prompt.
// Returns "" when there is nothing to show. Times are formatted AM/PM so drafts
// naturally use AM/PM, and the block declares itself authoritative over other
// details (so a conflict like a wrong time in thread history loses to the calendar).
func FormatUpcomingEvents(events []UpcomingEvent) string {
	if len(events) == 0 {
		return ""
	}
	max := CalendarMaxEvents()
	var b strings.Builder
	b.WriteString("## Upcoming studio events (schedule context)\n")
	b.WriteString("Use this to answer questions about classes, lessons, performances, and socials. Do not invent events not listed here.\n")
	b.WriteString("When an event below matches the question, its date and time are authoritative and override the 'Regular schedule' in the studio context and any times mentioned in the conversation. If the calendar has no event for what is asked, use the studio's 'Regular schedule' from the context.\n")
	shown := 0
	for _, e := range events {
		if shown >= max {
			fmt.Fprintf(&b, "\n(+%d more events in this window — not listed)\n", len(events)-shown)
			break
		}
		line := e.Date.Format("Mon 2006-01-02")
		if e.Start != "" {
			start := formatAmPm(e.Start)
			if start != "" {
				line += " " + start
				if end := formatAmPm(e.End); end != "" && end != start {
					line += " - " + end
				}
			}
		}
		line += " · " + e.Title
		if e.Location != "" && e.Location != "TBD" {
			line += " — " + e.Location
		}
		if e.EventType != "" {
			line += " [" + e.EventType + "]"
		}
		if e.IsCancelled {
			line += " (CANCELLED)"
		} else if e.FromSeries {
			line += " (recurring)"
		}
		b.WriteString(line + "\n")
		shown++
		if desc := strings.TrimSpace(e.Description); desc != "" && !strings.EqualFold(desc, e.Title) {
			if len(desc) > 140 {
				desc = desc[:140] + "…"
			}
			b.WriteString("    " + strings.ReplaceAll(desc, "\n", " ") + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// CalendarContext builds the schedule context block for the LLM prompt. It ALWAYS
// includes today's date (so the LLM can reason about "this week", "next week",
// "this month"), plus the upcoming-events block when available. On calendar load
// failure it still returns the date line, so a DB issue never leaves the LLM
// without temporal grounding.
func CalendarContext(ctx context.Context, pool *pgxpool.Pool) string {
	now := time.Now().In(StudioTZ())

	events, err := LoadUpcomingEvents(ctx, pool, CalendarLookaheadDays())
	eventsBlock := ""
	if err != nil {
		log.Printf("[calendar] load failed (continuing with date only): %v", err)
	} else if eventsBlock = FormatUpcomingEvents(events); eventsBlock != "" {
		log.Printf("[calendar] injecting %d upcoming event(s) (%d days lookahead)", len(events), CalendarLookaheadDays())
	}

	return FormatScheduleContext(now, eventsBlock)
}

// FormatScheduleContext renders the final block: today's date line (always) + events.
func FormatScheduleContext(now time.Time, eventsBlock string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Today is %s (%s timezone).\n",
		now.Format("Monday, January 2, 2006"), now.Location().String())
	if eventsBlock != "" {
		b.WriteString("\n" + eventsBlock)
	}
	return strings.TrimRight(b.String(), "\n")
}

func shortTime(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 5 {
		return s[:5]
	}
	return s
}

// formatAmPm converts a 24h "HH:MM" (or "HH:MM:SS") string to "h:mm AM/PM".
// Returns "" for empty input, or the original string if it cannot be parsed.
func formatAmPm(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		if t, err = time.Parse("15:04:05", s); err != nil {
			return s
		}
	}
	return t.Format("3:04 PM")
}

var weekdayIndex = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday, "friday": time.Friday,
	"saturday": time.Saturday,
}

func normalizeDays(days []string) map[time.Weekday]bool {
	out := map[time.Weekday]bool{}
	for _, d := range days {
		if wd, ok := weekdayIndex[strings.ToLower(strings.TrimSpace(d))]; ok {
			out[wd] = true
		}
	}
	return out
}
