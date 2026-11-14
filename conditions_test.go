package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testToday = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

// buildSeries makes daily data for the 11 years before testToday.
// rainFor/tempFor/tmaxFor receive the date and return that day's value.
func buildSeries(rainFor, tempFor, tmaxFor func(time.Time) float64, lagDays int) powerSeries {
	p := powerSeries{rain: map[string]float64{}, temp: map[string]float64{}, tmax: map[string]float64{}, root: map[string]float64{}}
	for d := testToday.AddDate(-11, 0, 0); !d.After(testToday); d = d.AddDate(0, 0, 1) {
		k := dayKey(d)
		if testToday.Sub(d) < time.Duration(lagDays)*24*time.Hour {
			p.rain[k], p.temp[k], p.tmax[k], p.root[k] = -999, -999, -999, -999
			continue
		}
		p.rain[k], p.temp[k], p.tmax[k], p.root[k] = rainFor(d), tempFor(d), tmaxFor(d), 0.5
	}
	return p
}

func steady(v float64) func(time.Time) float64 { return func(time.Time) float64 { return v } }

func findLevel(rep conditionsReport, topic string) string {
	for _, f := range rep.Findings {
		if f.Topic == topic {
			return f.Level
		}
	}
	return ""
}

func TestConditionsIgnoreNASALagAndReportNormalWeather(t *testing.T) {
	p := buildSeries(steady(4), steady(24), steady(30), 4)
	rep, err := analyzeConditions(p, testToday)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DataThrough != "2026-11-10" {
		t.Fatalf("data_through = %s, want 2026-11-10 (4-day lag)", rep.DataThrough)
	}
	if findLevel(rep, "Rainfall") != "ok" || findLevel(rep, "Temperature") != "ok" || findLevel(rep, "Dry spell") != "" {
		t.Fatalf("normal weather should raise no warnings: %+v", rep.Findings)
	}
	if rep.BaselineYears < 9 {
		t.Fatalf("baseline years = %d", rep.BaselineYears)
	}
}

func TestConditionsFlagSevereDroughtHeatAndDrySpell(t *testing.T) {
	cutoff := testToday.AddDate(0, 0, -40)
	rain := func(d time.Time) float64 {
		if d.After(cutoff) {
			return 0.2 // recent: almost no rain
		}
		return 5 // normal years
	}
	tmax := func(d time.Time) float64 {
		if d.After(cutoff) {
			return 37
		}
		return 30
	}
	temp := func(d time.Time) float64 {
		if d.After(cutoff) {
			return 27
		}
		return 24
	}
	rep, err := analyzeConditions(buildSeries(rain, temp, tmax, 3), testToday)
	if err != nil {
		t.Fatal(err)
	}
	if findLevel(rep, "Rainfall") != "alert" || findLevel(rep, "Dry spell") != "alert" || findLevel(rep, "Heat") != "alert" || findLevel(rep, "Temperature") != "watch" {
		t.Fatalf("drought not flagged: %+v", rep.Findings)
	}
	if rep.DaysSinceRain != conditionsWindowDays || rep.DrierThanYears != rep.BaselineYears {
		t.Fatalf("days since rain %d, drier than %d/%d", rep.DaysSinceRain, rep.DrierThanYears, rep.BaselineYears)
	}
}

func TestConditionsFlagWetterThanUsual(t *testing.T) {
	cutoff := testToday.AddDate(0, 0, -40)
	rain := func(d time.Time) float64 {
		if d.After(cutoff) {
			return 12
		}
		return 4
	}
	rep, err := analyzeConditions(buildSeries(rain, steady(24), steady(30), 3), testToday)
	if err != nil {
		t.Fatal(err)
	}
	if findLevel(rep, "Rainfall") != "watch" || !strings.Contains(rep.Findings[0].Message, "Wetter") {
		t.Fatalf("wet spell not flagged: %+v", rep.Findings)
	}
}

func TestConditionsDoNotAlarmDuringNormallyDrySeason(t *testing.T) {
	rep, err := analyzeConditions(buildSeries(steady(0.1), steady(24), steady(30), 3), testToday)
	if err != nil {
		t.Fatal(err)
	}
	if findLevel(rep, "Rainfall") != "ok" || findLevel(rep, "Dry spell") != "" {
		t.Fatalf("dry season should not alarm: %+v", rep.Findings)
	}
}

func TestConditionsFailWhenDataMissing(t *testing.T) {
	if _, err := analyzeConditions(powerSeries{}, testToday); err == nil {
		t.Fatal("expected an error with no data")
	}
	short := buildSeries(steady(4), steady(24), steady(30), 3)
	for k := range short.rain {
		if d, _ := powerDate(k); d.Year() < 2024 {
			delete(short.rain, k)
		}
	}
	if _, err := analyzeConditions(short, testToday); err == nil {
		t.Fatal("expected an error with fewer than 3 baseline years")
	}
}

func TestConditionsHandlerCachesAndRoundsLocation(t *testing.T) {
	calls := 0
	srv := &app{
		httpClient: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if !strings.Contains(r.URL.RawQuery, "latitude=0.3500") {
				t.Errorf("coordinates not rounded: %s", r.URL.RawQuery)
			}
			p := buildSeries(steady(4), steady(24), steady(30), 3)
			body := map[string]any{"properties": map[string]any{"parameter": map[string]any{
				"PRECTOTCORR": p.rain, "T2M": p.temp, "T2M_MAX": p.tmax, "GWETROOT": p.root,
			}}}
			raw, _ := json.Marshal(body)
			return jsonResponse(200, string(raw)), nil
		})},
	}
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		srv.conditions(rec, httptest.NewRequest("GET", "/api/conditions?lat=0.3534&lon=32.7552", nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"findings"`) {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
	}
	if calls != 1 {
		t.Fatalf("NASA was called %d times; second request should come from cache", calls)
	}
}

func TestConditionsHandlerRejectsBadLocationAndUpstreamErrors(t *testing.T) {
	srv := &app{httpClient: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) { return jsonResponse(500, ""), nil })}}
	rec := httptest.NewRecorder()
	srv.conditions(rec, httptest.NewRequest("GET", "/api/conditions?lat=200&lon=0", nil))
	if rec.Code != 400 {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.conditions(rec, httptest.NewRequest("GET", "/api/conditions?lat=1&lon=1", nil))
	if rec.Code != 502 {
		t.Fatalf("status %d, want 502", rec.Code)
	}
}
