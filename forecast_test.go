package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeDays(n int, rain, tmax, tmin float64) []forecastDay {
	start := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	days := make([]forecastDay, n)
	for i := range days {
		days[i] = forecastDay{Date: start.AddDate(0, 0, i).Format("2006-01-02"), RainMM: rain, TMaxC: tmax, TMinC: tmin}
	}
	return days
}

func hasFinding(fs []conditionFinding, topic, level string) bool {
	for _, f := range fs {
		if f.Topic == topic && f.Level == level {
			return true
		}
	}
	return false
}

func TestAnalyzeForecastFlagsHeavyRainHeatAndFrost(t *testing.T) {
	days := makeDays(14, 1, 36, 15)
	days[2].RainMM = 60
	days[3].TMinC = -1
	rep, err := analyzeForecast(days, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep.Findings, "Heavy rain", "alert") || !hasFinding(rep.Findings, "Heat forecast", "watch") || !hasFinding(rep.Findings, "Cold", "alert") {
		t.Fatalf("missing findings: %+v", rep.Findings)
	}
	if rep.Rain7MM != 66 || rep.RainyDays7 != 7 || rep.FirstRainDate != "2026-11-14" {
		t.Fatalf("summary wrong: %+v", rep)
	}
	if _, err := analyzeForecast(makeDays(5, 0, 30, 20), "test"); err == nil {
		t.Fatal("expected an error for a short forecast")
	}
}

func TestCombineOutlook(t *testing.T) {
	dry := &conditionsReport{Recent: conditionsStats{RainfallMM: 20}, Normal: conditionsStats{RainfallMM: 120}, DaysSinceRain: 12}
	noRain, _ := analyzeForecast(makeDays(14, 0, 30, 20), "t")
	if out := combineOutlook(dry, noRain); !hasFinding(out, "Dry spell outlook", "alert") {
		t.Fatalf("dry spell continuing not flagged: %+v", out)
	}
	rainy, _ := analyzeForecast(makeDays(14, 5, 30, 20), "t")
	dry.DaysSinceRain = 2
	if out := combineOutlook(dry, rainy); !hasFinding(out, "Rain outlook", "ok") {
		t.Fatalf("relief not reported: %+v", out)
	}
	wet := &conditionsReport{Recent: conditionsStats{RainfallMM: 300}, Normal: conditionsStats{RainfallMM: 120}}
	heavy := makeDays(14, 2, 30, 20)
	heavy[1].RainMM = 40
	heavyRep, _ := analyzeForecast(heavy, "t")
	if out := combineOutlook(wet, heavyRep); !hasFinding(out, "Waterlogging outlook", "alert") {
		t.Fatalf("waterlogging not flagged: %+v", out)
	}
	season := &conditionsReport{Recent: conditionsStats{RainfallMM: 0}, Normal: conditionsStats{RainfallMM: 2}, DaysSinceRain: 30}
	if out := combineOutlook(season, noRain); len(out) != 0 {
		t.Fatalf("normal dry season should not alarm: %+v", out)
	}
	if out := combineOutlook(nil, noRain); out != nil {
		t.Fatal("no conditions means no combined findings")
	}
}

func openMeteoBody(days []forecastDay, nullRainAt int) string {
	var times []string
	var rain, tmax, tmin []any
	for i, d := range days {
		times = append(times, d.Date)
		if i == nullRainAt {
			rain = append(rain, nil)
		} else {
			rain = append(rain, d.RainMM)
		}
		tmax = append(tmax, d.TMaxC)
		tmin = append(tmin, d.TMinC)
	}
	raw, _ := json.Marshal(map[string]any{"daily": map[string]any{"time": times, "precipitation_sum": rain, "temperature_2m_max": tmax, "temperature_2m_min": tmin}})
	return string(raw)
}

func nasaBody() string {
	p := buildSeries(steady(4), steady(24), steady(30), 3)
	raw, _ := json.Marshal(map[string]any{"properties": map[string]any{"parameter": map[string]any{
		"PRECTOTCORR": p.rain, "T2M": p.temp, "T2M_MAX": p.tmax, "GWETROOT": p.root}}})
	return string(raw)
}

func meteomaticsBody(now time.Time) string {
	first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	series := func(param string, v float64) map[string]any {
		var dates []map[string]any
		for i := 0; i < forecastHorizonDays; i++ {
			dates = append(dates, map[string]any{"date": first.AddDate(0, 0, i).Format("2006-01-02T15:04:05Z"), "value": v})
		}
		return map[string]any{"parameter": param, "coordinates": []any{map[string]any{"dates": dates}}}
	}
	raw, _ := json.Marshal(map[string]any{"data": []any{series("precip_24h:mm", 3), series("t_max_2m_24h:C", 31), series("t_min_2m_24h:C", 19)}})
	return string(raw)
}

func routedApp(counts map[string]int, meteomaticsStatus int) *app {
	return &app{httpClient: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		counts[r.URL.Host]++
		switch {
		case strings.Contains(r.URL.Host, "power.larc"):
			return jsonResponse(200, nasaBody()), nil
		case strings.Contains(r.URL.Host, "open-meteo"):
			return jsonResponse(200, openMeteoBody(makeDays(14, 1, 30, 20), 3)), nil
		case strings.Contains(r.URL.Host, "meteomatics"):
			if u, p, ok := r.BasicAuth(); !ok || u != "demo" || p != "secret" {
				return jsonResponse(401, ""), nil
			}
			return jsonResponse(meteomaticsStatus, meteomaticsBody(time.Now().UTC())), nil
		}
		return nil, fmt.Errorf("unexpected host %s", r.URL.Host)
	})}}
}

func TestForecastDefaultsToOpenMeteoSkipsGapsCachesAndAddsOutlook(t *testing.T) {
	t.Setenv("METEOMATICS_USER", "")
	counts := map[string]int{}
	srv := routedApp(counts, 200)
	var rep forecastReport
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		srv.forecast(rec, httptest.NewRequest("GET", "/api/forecast?lat=0.35&lon=32.76", nil))
		if rec.Code != 200 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		rep = forecastReport{}
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
	}
	if rep.Provider != "Open-Meteo" || len(rep.Days) != 13 {
		t.Fatalf("provider %s, days %d (one day had a null and must be skipped)", rep.Provider, len(rep.Days))
	}
	if counts["api.open-meteo.com"] != 1 || counts["power.larc.nasa.gov"] != 1 {
		t.Fatalf("second request should be served from cache: %v", counts)
	}
}

func TestForecastPrefersMeteomaticsAndShiftsDaysBack(t *testing.T) {
	t.Setenv("METEOMATICS_USER", "demo")
	t.Setenv("METEOMATICS_PASSWORD", "secret")
	counts := map[string]int{}
	srv := routedApp(counts, 200)
	rec := httptest.NewRecorder()
	srv.forecast(rec, httptest.NewRequest("GET", "/api/forecast?lat=0.35&lon=32.76", nil))
	var rep forecastReport
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	if rep.Provider != "Meteomatics" || len(rep.Days) != forecastHorizonDays {
		t.Fatalf("provider %s, days %d", rep.Provider, len(rep.Days))
	}
	if today := time.Now().UTC().Format("2006-01-02"); rep.Days[0].Date != today {
		t.Fatalf("first day %s, want today %s (24h values are assigned to the day they end)", rep.Days[0].Date, today)
	}
	if counts["api.open-meteo.com"] != 0 {
		t.Fatal("Open-Meteo should not be called when Meteomatics succeeds")
	}
}

func TestForecastFallsBackToOpenMeteoWhenMeteomaticsFails(t *testing.T) {
	t.Setenv("METEOMATICS_USER", "demo")
	t.Setenv("METEOMATICS_PASSWORD", "secret")
	srv := routedApp(map[string]int{}, 500)
	rec := httptest.NewRecorder()
	srv.forecast(rec, httptest.NewRequest("GET", "/api/forecast?lat=1&lon=1", nil))
	var rep forecastReport
	_ = json.Unmarshal(rec.Body.Bytes(), &rep)
	if rec.Code != 200 || rep.Provider != "Open-Meteo" {
		t.Fatalf("status %d provider %s", rec.Code, rep.Provider)
	}
}

func TestForecastStillWorksWhenNASAConditionsFail(t *testing.T) {
	t.Setenv("METEOMATICS_USER", "")
	srv := &app{httpClient: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "open-meteo") {
			return jsonResponse(200, openMeteoBody(makeDays(14, 1, 30, 20), -1)), nil
		}
		return jsonResponse(503, ""), nil
	})}}
	rec := httptest.NewRecorder()
	srv.forecast(rec, httptest.NewRequest("GET", "/api/forecast?lat=1&lon=1", nil))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), `"outlook"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestForecastRejectsBadInputAndUpstreamFailure(t *testing.T) {
	t.Setenv("METEOMATICS_USER", "")
	srv := &app{httpClient: &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) { return jsonResponse(500, ""), nil })}}
	rec := httptest.NewRecorder()
	srv.forecast(rec, httptest.NewRequest("GET", "/api/forecast?lat=x&lon=1", nil))
	if rec.Code != 400 {
		t.Fatalf("status %d, want 400", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.forecast(rec, httptest.NewRequest("GET", "/api/forecast?lat=1&lon=1", nil))
	if rec.Code != 502 {
		t.Fatalf("status %d, want 502", rec.Code)
	}
}
