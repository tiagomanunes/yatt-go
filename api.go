package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"time"
)

type server struct {
	db           *sql.DB
	cfg          Config
	allowedHosts map[string]bool
	static       http.Handler
}

func newServer(db *sql.DB, cfg Config, addr string, static http.Handler) *server {
	return &server{db: db, cfg: cfg, allowedHosts: hostsFor(addr), static: static}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.config)
	mux.HandleFunc("GET /api/categories", s.listCategories)
	mux.HandleFunc("POST /api/categories", s.createCategory)
	mux.HandleFunc("GET /api/activities", s.listActivities)
	mux.HandleFunc("POST /api/activities", s.createActivity)
	mux.HandleFunc("PUT /api/activities/{id}", s.updateActivity)
	mux.HandleFunc("GET /api/earliest", s.earliest)
	mux.HandleFunc("GET /api/report", s.report)
	mux.Handle("GET /", s.static)
	return s.guard(mux)
}

// guard protects the unauthenticated local API from other websites: it
// rejects unexpected Host headers (DNS rebinding) and requires a JSON
// content type on writes, which browsers only send cross-origin after a
// CORS preflight that this server never grants.
func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHosts[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if ct != "application/json" {
				http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// ---- types ----------------------------------------------------------------

type Category struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Activity struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Date       string `json:"date"`
	StartTime  string `json:"startTime"`
	Duration   int    `json:"duration"` // minutes
	Name       string `json:"name"`
	CategoryID int64  `json:"categoryId"`
	Category   string `json:"category"`
	Tags       string `json:"tags"`
}

// ---- helpers --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encoding response: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverErr(w http.ResponseWriter, err error) {
	log.Printf("error: %v", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func validTime(s string) bool {
	_, err := time.Parse("15:04", s)
	return err == nil
}

func minutesOf(hhmm string) int {
	t, _ := time.Parse("15:04", hhmm)
	return t.Hour()*60 + t.Minute()
}

func formatMinutes(m int) string {
	m = ((m % 1440) + 1440) % 1440
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func (s *server) categoryExists(id int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM category WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ---- config ---------------------------------------------------------------

func (s *server) config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

// ---- categories -----------------------------------------------------------

func (s *server) listCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT id, name FROM category ORDER BY id`)
	if err != nil {
		serverErr(w, err)
		return
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name); err != nil {
			serverErr(w, err)
			return
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createCategory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	res, err := s.db.Exec(`INSERT INTO category (name) VALUES (?)`, in.Name)
	if err != nil {
		serverErr(w, err)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusCreated, Category{ID: id, Name: in.Name})
}

// ---- activities -----------------------------------------------------------

const activitySelect = `
SELECT a.id, a.kind, a.date, a.start_time, a.duration, a.name, a.category_id, c.name, a.tags
FROM activity a JOIN category c ON c.id = a.category_id`

type scanner interface{ Scan(dest ...any) error }

func scanActivity(sc scanner) (Activity, error) {
	var a Activity
	err := sc.Scan(&a.ID, &a.Kind, &a.Date, &a.StartTime, &a.Duration, &a.Name, &a.CategoryID, &a.Category, &a.Tags)
	return a, err
}

func (s *server) getActivity(id int64) (Activity, error) {
	return scanActivity(s.db.QueryRow(activitySelect+` WHERE a.id = ?`, id))
}

func (s *server) listActivities(w http.ResponseWriter, r *http.Request) {
	kind, date := r.URL.Query().Get("kind"), r.URL.Query().Get("date")
	if (kind != "planned" && kind != "done") || !validDate(date) {
		writeErr(w, http.StatusBadRequest, "kind (planned|done) and date (yyyy-MM-dd) are required")
		return
	}
	rows, err := s.db.Query(activitySelect+` WHERE a.kind = ? AND a.date = ? ORDER BY a.start_time, a.id`, kind, date)
	if err != nil {
		serverErr(w, err)
		return
	}
	defer rows.Close()
	out := []Activity{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			serverErr(w, err)
			return
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type activityInput struct {
	Kind       string `json:"kind"`
	Date       string `json:"date"`
	StartTime  string `json:"startTime"`
	Duration   int    `json:"duration"`
	Name       string `json:"name"`
	CategoryID int64  `json:"categoryId"`
	Tags       string `json:"tags"`
}

func (s *server) createActivity(w http.ResponseWriter, r *http.Request) {
	var in activityInput
	if !readJSON(w, r, &in) {
		return
	}
	if in.Kind != "planned" && in.Kind != "done" {
		writeErr(w, http.StatusBadRequest, "kind must be planned or done")
		return
	}
	if !validDate(in.Date) || !validTime(in.StartTime) {
		writeErr(w, http.StatusBadRequest, "invalid date or startTime")
		return
	}
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if ok, err := s.categoryExists(in.CategoryID); err != nil {
		serverErr(w, err)
		return
	} else if !ok {
		writeErr(w, http.StatusBadRequest, "unknown category")
		return
	}
	res, err := s.db.Exec(
		`INSERT INTO activity (kind, date, start_time, duration, name, category_id, tags) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		in.Kind, in.Date, in.StartTime, in.Duration, in.Name, in.CategoryID, in.Tags)
	if err != nil {
		serverErr(w, err)
		return
	}
	id, _ := res.LastInsertId()
	a, err := s.getActivity(id)
	if err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// updateActivity changes the same fields the old edit dialog did: start time,
// duration, name, tags and category (never the date or kind).
func (s *server) updateActivity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		StartTime  string `json:"startTime"`
		Duration   int    `json:"duration"`
		Name       string `json:"name"`
		CategoryID int64  `json:"categoryId"`
		Tags       string `json:"tags"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if !validTime(in.StartTime) || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "valid startTime and a name are required")
		return
	}
	if ok, err := s.categoryExists(in.CategoryID); err != nil {
		serverErr(w, err)
		return
	} else if !ok {
		writeErr(w, http.StatusBadRequest, "unknown category")
		return
	}
	res, err := s.db.Exec(
		`UPDATE activity SET start_time = ?, duration = ?, name = ?, category_id = ?, tags = ? WHERE id = ?`,
		in.StartTime, in.Duration, in.Name, in.CategoryID, in.Tags, id)
	if err != nil {
		serverErr(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeErr(w, http.StatusNotFound, "no such activity")
		return
	}
	a, err := s.getActivity(id)
	if err != nil {
		serverErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

const dayStart = "08:30"

// earliest returns the default start time for new work of a kind: the end of
// the item of that kind with the latest start time that day, or dayStart if none
func (s *server) earliest(w http.ResponseWriter, r *http.Request) {
	kind, date := r.URL.Query().Get("kind"), r.URL.Query().Get("date")
	if (kind != "planned" && kind != "done") || !validDate(date) {
		writeErr(w, http.StatusBadRequest, "kind (planned|done) and date (yyy-MM-dd) are required")
		return
	}
	var start string
	var duration int
	err := s.db.QueryRow(
		`SELECT start_time, duration FROM activity WHERE kind = ? AND date = ? ORDER BY start_time DESC, id DESC LIMIT 1`,
		kind, date).Scan(&start, &duration)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusOK, map[string]string{"time": dayStart})
	case err != nil:
		log.Printf("earliest: %v", err)
		writeJSON(w, http.StatusOK, map[string]string{"time": dayStart})
	default:
		writeJSON(w, http.StatusOK, map[string]string{"time": formatMinutes(minutesOf(start) + duration)})
	}
}

// ---- report ---------------------------------------------------------------

type bar struct {
	Category string             `json:"category"`
	Hours    float64            `json:"hours"`
	Tags     map[string]float64 `json:"tags"` // tag string -> hours
}

func (s *server) report(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if !validDate(from) || !validDate(to) || from > to {
		writeErr(w, http.StatusBadRequest, "from and to (yyyy-MM-dd, from <= to) are required")
		return
	}
	rows, err := s.db.Query(`
		SELECT a.kind, c.name, a.tags, SUM(a.duration)
		FROM activity a JOIN category c ON c.id = a.category_id
		WHERE a.date >= ? AND a.date <= ?
		GROUP BY a.kind, c.name, a.tags`, from, to)
	if err != nil {
		serverErr(w, err)
		return
	}
	defer rows.Close()

	acc := map[string]map[string]*bar{"planned": {}, "done": {}}
	for rows.Next() {
		var kind, cat, tags string
		var minutes int
		if err := rows.Scan(&kind, &cat, &tags, &minutes); err != nil {
			serverErr(w, err)
			return
		}
		if tags == "" {
			tags = "untagged"
		}
		b := acc[kind][cat]
		if b == nil {
			b = &bar{Category: cat, Tags: map[string]float64{}}
			acc[kind][cat] = b
		}
		h := float64(minutes) / 60.0
		b.Hours += h
		b.Tags[tags] += h
	}
	if err := rows.Err(); err != nil {
		serverErr(w, err)
		return
	}

	flatten := func(m map[string]*bar) []bar {
		out := make([]bar, 0, len(m))
		for _, b := range m {
			out = append(out, *b)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
		return out
	}
	writeJSON(w, http.StatusOK, map[string][]bar{
		"planned": flatten(acc["planned"]),
		"done":    flatten(acc["done"]),
	})
}
