package handlers

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/abhinandanwadwa/overbookr/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AnalyticsHandler holds DB deps
type AnalyticsHandler struct {
	db *db.Queries
}

// NewAnalyticsHandler creates handler
func NewAnalyticsHandler(dbconn *pgxpool.Pool) *AnalyticsHandler {
	return &AnalyticsHandler{
		db: db.New(dbconn),
	}
}

// Response structs
type AnalyticsResponse struct {
	Range     TimeRange               `json:"range"`
	Totals    Totals                  `json:"totals"`
	ByDay     []BookingsPerDayPoint   `json:"by_day"`
	TopEvents []TopEvent              `json:"top_events"`
	ByStatus  []StatusCount           `json:"by_status"`
	EventUtil []EventUtilizationPoint `json:"event_utilization"`
}

type TimeRange struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

type Totals struct {
	TotalBookings      int64 `json:"total_bookings"`
	TotalSeatsBooked   int64 `json:"total_seats_booked"`
	TotalCancellations int64 `json:"total_cancellations"`
	TotalActive        int64 `json:"total_active"`
}

type BookingsPerDayPoint struct {
	Day         time.Time `json:"day"`
	Bookings    int64     `json:"bookings"`
	SeatsBooked int64     `json:"seats_booked"`
}

type TopEvent struct {
	EventID     string `json:"event_id"`
	Name        string `json:"name"`
	Bookings    int64  `json:"bookings_count"`
	SeatsBooked int64  `json:"seats_booked"`
	Capacity    int32  `json:"capacity"`
	BookedCount int32  `json:"booked_count"`
}

type StatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

type EventUtilizationPoint struct {
	EventID              string `json:"event_id"`
	Name                 string `json:"name"`
	Capacity             int32  `json:"capacity"`
	BookedCount          int32  `json:"booked_count"`
	BookingsSeatsInRange int64  `json:"bookings_seats_in_range"`
}

// GET /admin/analytics/total_bookings?from=&to=&top_n=
func (h *AnalyticsHandler) GetTotalBookingsAnalytics(c *gin.Context) {
	ctx := context.Background()

	// Parse from/to; support ISO8601 datetime or date-only (YYYY-MM-DD)
	now := time.Now().UTC()
	var from, to time.Time
	fromStr := c.Query("from")
	toStr := c.Query("to")
	if fromStr == "" && toStr == "" {
		// default: last 30 days
		to = now
		from = now.AddDate(0, 0, -30)
	} else {
		var err error
		from, err = parseDateOrDatetime(fromStr, now.AddDate(0, 0, -30))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from param", "details": err.Error()})
			return
		}
		to, err = parseDateOrDatetime(toStr, now)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to param", "details": err.Error()})
			return
		}
	}

	// optional top N (default 10)
	topN := 10
	if v := c.Query("top_n"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			topN = n
		}
	}

	// prepare pgtype parameters (your sqlc likely expects pgtype.Timestamptz)
	fromParam := pgtype.Timestamptz{Time: from, Valid: true}
	toParam := pgtype.Timestamptz{Time: to, Valid: true}

	// Totals
	totalsRow, err := h.db.GetBookingsTotalsBetween(ctx, db.GetBookingsTotalsBetweenParams{CreatedAt: fromParam, CreatedAt_2: toParam})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch totals", "details": err.Error()})
		return
	}
	totals := Totals{
		TotalBookings:      totalsRow.TotalBookings,
		TotalSeatsBooked:   totalsRow.TotalSeatsBooked,
		TotalCancellations: totalsRow.TotalCancellations,
		TotalActive:        totalsRow.TotalActive,
	}

	// By day
	byDayRows, err := h.db.GetBookingsPerDayBetween(ctx, db.GetBookingsPerDayBetweenParams{CreatedAt: fromParam, CreatedAt_2: toParam})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch by-day", "details": err.Error()})
		return
	}
	byDay := make([]BookingsPerDayPoint, 0, len(byDayRows))
	for _, r := range byDayRows {
		byDay = append(byDay, BookingsPerDayPoint{
			Day:         r.Day.Time,
			Bookings:    r.BookingsCount,
			SeatsBooked: r.SeatsBooked,
		})
	}

	// Top events
	topRows, err := h.db.GetTopEventsBySeatsBetween(ctx, db.GetTopEventsBySeatsBetweenParams{CreatedAt: fromParam, CreatedAt_2: toParam, Limit: int32(topN)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch top events", "details": err.Error()})
		return
	}
	topEvents := make([]TopEvent, 0, len(topRows))
	for _, r := range topRows {
		topEvents = append(topEvents, TopEvent{
			EventID:     r.EventID.String(),
			Name:        r.Name,
			Bookings:    r.BookingsCount,
			SeatsBooked: r.SeatsBooked,
			Capacity:    r.Capacity,
			BookedCount: r.BookedCount,
		})
	}

	// By status
	statusRows, err := h.db.GetBookingsByStatusBetween(ctx, db.GetBookingsByStatusBetweenParams{CreatedAt: fromParam, CreatedAt_2: toParam})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch status breakdown", "details": err.Error()})
		return
	}
	statusCounts := make([]StatusCount, 0, len(statusRows))
	for _, r := range statusRows {
		statusCounts = append(statusCounts, StatusCount{
			Status: r.Status,
			Count:  r.Cnt,
		})
	}

	// Event utilization (limit topN)
	utilRows, err := h.db.GetEventUtilizationBetween(ctx, db.GetEventUtilizationBetweenParams{CreatedAt: fromParam, CreatedAt_2: toParam, Limit: int32(topN)})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch event utilization", "details": err.Error()})
		return
	}
	util := make([]EventUtilizationPoint, 0, len(utilRows))
	for _, r := range utilRows {
		util = append(util, EventUtilizationPoint{
			EventID:              r.EventID.String(),
			Name:                 r.Name,
			Capacity:             r.Capacity,
			BookedCount:          r.BookedCount,
			BookingsSeatsInRange: r.BookingsSeatsInRange,
		})
	}

	resp := AnalyticsResponse{
		Range:     TimeRange{From: from, To: to},
		Totals:    totals,
		ByDay:     byDay,
		TopEvents: topEvents,
		ByStatus:  statusCounts,
		EventUtil: util,
	}

	c.JSON(http.StatusOK, resp)
}

// parseDateOrDatetime accepts ISO datetime or date-only (YYYY-MM-DD). If empty, returns defaultVal.
func parseDateOrDatetime(s string, defaultVal time.Time) (time.Time, error) {
	if s == "" {
		return defaultVal, nil
	}
	// try RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// try date only
	if t, err := time.Parse("2006-01-02", s); err == nil {
		// treat as start of day in UTC
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	return time.Time{}, &time.ParseError{Layout: "RFC3339 or 2006-01-02", Value: s, LayoutElem: "", ValueElem: ""}
}
