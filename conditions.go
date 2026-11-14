package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	conditionsWindowDays = 30
	conditionsYears      = 10
	conditionsCacheTTL   = 6 * time.Hour
	conditionsCacheMax   = 500
)

type conditionsStats struct {
	RainfallMM  float64  `json:"rainfall_mm"`
	MeanTempC   float64  `json:"mean_temp_c"`
	HeatDays    float64  `json:"heat_days"`
	RootWetness *float64 `json:"root_wetness,omitempty"`
}

type conditionFinding struct {
	Topic   string `json:"topic"`
	Level   string `json:"level"` // ok | watch | alert
	Message string `json:"message"`
}

type conditionsReport struct {
	DataThrough     string             `json:"data_through"`
	WindowDays      int                `json:"window_days"`
	WindowLabel     string             `json:"window_label"`
	Recent          conditionsStats    `json:"recent"`
	Normal          conditionsStats    `json:"normal"`
	BaselineYears   int                `json:"baseline_years"`
	RainLast7DaysMM float64            `json:"rain_last_7_days_mm"`
	DaysSinceRain   int                `json:"days_since_rain"`
	DrierThanYears  int                `json:"drier_than_years"`
	Findings        []conditionFinding `json:"findings"`
	Source          string             `json:"source"`
	Note            string             `json:"note"`
}

type cachedConditions struct {
	report  conditionsReport
	expires time.Time
}

// ---------------------------------------------------------------------------
// Analysis (pure functions, no network)
// ---------------------------------------------------------------------------

type powerSeries struct {
	rain, temp, tmax, root map[string]float64
}

func powerDate(key string) (time.Time, bool) {
	if len(key) != 8 {
		return time.Time{}, false
	}
	d, err := time.Parse("20060102", key)
	return d, err == nil
}

func dayKey(t time.Time) string { return t.Format("20060102") }

type windowStats struct {
	rain     float64
	rainDays int
	tempSum  float64
	tempDays int
	heatDays int
	rootSum  float64
	rootDays int
}

// statsFor summarises the `days` days ending on `end` (inclusive).
func (p powerSeries) statsFor(end time.Time, days int) windowStats {
	var w windowStats
	for i := 0; i < days; i++ {
		key := dayKey(end.AddDate(0, 0, -i))
		if v, ok := p.rain[key]; ok && validPowerValue(v) {
			w.rain += v
			w.rainDays++
		}
		if v, ok := p.temp[key]; ok && validPowerValue(v) {
			w.tempSum += v
			w.tempDays++
		}
		if v, ok := p.tmax[key]; ok && validPowerValue(v) && v > 35 {
			w.heatDays++
		}
		if v, ok := p.root[key]; ok && validPowerValue(v) {
			w.rootSum += v
			w.rootDays++
		}
	}
	return w
}

// latestRainDate finds the newest day with a real rainfall value. NASA POWER
// publishes a few days behind, and the newest days are filled with -999.
func (p powerSeries) latestRainDate(notAfter time.Time) (time.Time, bool) {
	var best time.Time
	found := false
	for key, v := range p.rain {
		d, ok := powerDate(key)
		if !ok || !validPowerValue(v) || d.After(notAfter) {
			continue
		}
		if !found || d.After(best) {
			best, found = d, true
		}
	}
	return best, found
}

func analyzeConditions(p powerSeries, today time.Time) (conditionsReport, error) {
	end, ok := p.latestRainDate(today)
	if !ok {
		return conditionsReport{}, errors.New("NASA POWER returned no recent rainfall values")
	}
	recent := p.statsFor(end, conditionsWindowDays)
	if recent.rainDays < 20 || recent.tempDays < 20 {
		return conditionsReport{}, errors.New("NASA POWER has too few recent days for this location")
	}

	var rainTotals []float64
	var tempSum, heatSum, rootSum float64
	var tempYears, rootYears int
	for k := 1; k <= conditionsYears; k++ {
		w := p.statsFor(end.AddDate(-k, 0, 0), conditionsWindowDays)
		if w.rainDays < 25 || w.tempDays < 25 {
			continue
		}
		rainTotals = append(rainTotals, w.rain)
		tempSum += w.tempSum / float64(w.tempDays)
		tempYears++
		heatSum += float64(w.heatDays)
		if w.rootDays >= 25 {
			rootSum += w.rootSum / float64(w.rootDays)
			rootYears++
		}
	}
	if len(rainTotals) < 3 {
		return conditionsReport{}, errors.New("NASA POWER has too few past years to compare against")
	}
	var normalRain float64
	for _, v := range rainTotals {
		normalRain += v
	}
	normalRain /= float64(len(rainTotals))

	rep := conditionsReport{
		DataThrough:   end.Format("2006-01-02"),
		WindowDays:    conditionsWindowDays,
		WindowLabel:   end.AddDate(0, 0, -(conditionsWindowDays-1)).Format("2 Jan") + " – " + end.Format("2 Jan 2006"),
		BaselineYears: len(rainTotals),
		Recent: conditionsStats{
			RainfallMM: recent.rain,
			MeanTempC:  recent.tempSum / float64(recent.tempDays),
			HeatDays:   float64(recent.heatDays),
		},
		Normal: conditionsStats{
			RainfallMM: normalRain,
			MeanTempC:  tempSum / float64(tempYears),
			HeatDays:   heatSum / float64(len(rainTotals)),
		},
		Source: "NASA POWER daily data",
		Note:   "These are measured and modelled past conditions, not a forecast. NASA POWER is published a few days behind and is a grid estimate, so it may differ from your own field.",
	}
	if recent.rootDays >= 20 {
		v := recent.rootSum / float64(recent.rootDays)
		rep.Recent.RootWetness = &v
	}
	if rootYears >= 3 {
		v := rootSum / float64(rootYears)
		rep.Normal.RootWetness = &v
	}
	for _, v := range rainTotals {
		if recent.rain < v {
			rep.DrierThanYears++
		}
	}
	rep.RainLast7DaysMM = p.statsFor(end, 7).rain
	for i := 0; i < conditionsWindowDays; i++ {
		v, ok := p.rain[dayKey(end.AddDate(0, 0, -i))]
		if !ok || !validPowerValue(v) || v >= 1 {
			break
		}
		rep.DaysSinceRain++
	}
	rep.Findings = interpretConditions(rep)
	return rep, nil
}

// interpretConditions turns numbers into plain statements. It is deliberately
// rule-based: the language model later explains these findings but never
// produces the numbers itself.
func interpretConditions(rep conditionsReport) []conditionFinding {
	var out []conditionFinding
	add := func(topic, level, format string, args ...any) {
		out = append(out, conditionFinding{Topic: topic, Level: level, Message: fmt.Sprintf(format, args...)})
	}

	normalIsDry := rep.Normal.RainfallMM < 15
	if normalIsDry {
		add("Rainfall", "ok", "This is normally a dry time of year here (about %.0f mm in %d days), so little rain is expected.",
			rep.Normal.RainfallMM, rep.WindowDays)
	} else {
		ratio := rep.Recent.RainfallMM / rep.Normal.RainfallMM
		pct := math.Round(ratio * 100)
		switch {
		case ratio < 0.3:
			add("Rainfall", "alert", "Very dry: %.0f mm fell in the last %d days, only %.0f%% of the usual %.0f mm. It was drier than %d of the last %d years.",
				rep.Recent.RainfallMM, rep.WindowDays, pct, rep.Normal.RainfallMM, rep.DrierThanYears, rep.BaselineYears)
		case ratio < 0.6:
			add("Rainfall", "watch", "Drier than usual: %.0f mm in the last %d days, about %.0f%% of the usual %.0f mm.",
				rep.Recent.RainfallMM, rep.WindowDays, pct, rep.Normal.RainfallMM)
		case ratio > 1.5:
			add("Rainfall", "watch", "Wetter than usual: %.0f mm in the last %d days, about %.0f%% of the usual %.0f mm.",
				rep.Recent.RainfallMM, rep.WindowDays, pct, rep.Normal.RainfallMM)
		default:
			add("Rainfall", "ok", "Rainfall is close to normal: %.0f mm in the last %d days (usual %.0f mm).",
				rep.Recent.RainfallMM, rep.WindowDays, rep.Normal.RainfallMM)
		}
		switch {
		case rep.DaysSinceRain >= 14:
			add("Dry spell", "alert", "No meaningful rain (1 mm or more) for %d days up to %s.", rep.DaysSinceRain, rep.DataThrough)
		case rep.DaysSinceRain >= 7:
			add("Dry spell", "watch", "No meaningful rain (1 mm or more) for %d days up to %s.", rep.DaysSinceRain, rep.DataThrough)
		}
	}

	diff := rep.Recent.MeanTempC - rep.Normal.MeanTempC
	switch {
	case diff >= 2:
		add("Temperature", "watch", "Warmer than usual: average %.1f°C, which is %.1f°C above normal (%.1f°C).", rep.Recent.MeanTempC, diff, rep.Normal.MeanTempC)
	case diff <= -2:
		add("Temperature", "watch", "Cooler than usual: average %.1f°C, which is %.1f°C below normal (%.1f°C).", rep.Recent.MeanTempC, -diff, rep.Normal.MeanTempC)
	default:
		add("Temperature", "ok", "Temperature is close to normal: average %.1f°C (usual %.1f°C).", rep.Recent.MeanTempC, rep.Normal.MeanTempC)
	}

	switch {
	case rep.Recent.HeatDays >= 10:
		add("Heat", "alert", "%.0f days above 35°C in the last %d days (usual about %.0f).", rep.Recent.HeatDays, rep.WindowDays, rep.Normal.HeatDays)
	case rep.Recent.HeatDays >= 5:
		add("Heat", "watch", "%.0f days above 35°C in the last %d days (usual about %.0f).", rep.Recent.HeatDays, rep.WindowDays, rep.Normal.HeatDays)
	}

	if w := rep.Recent.RootWetness; w != nil {
		switch {
		case *w <= 0.3:
			add("Soil moisture", "watch", "Modelled root-zone soil is dry (wetness index %.0f%%). This is a satellite-model estimate, not a field test.", *w*100)
		case *w >= 0.85:
			add("Soil moisture", "watch", "Modelled root-zone soil is very wet (wetness index %.0f%%), so waterlogging and slow field access are possible. This is a model estimate, not a field test.", *w*100)
		default:
			add("Soil moisture", "ok", "Modelled root-zone soil moisture looks moderate (wetness index %.0f%%). This is a model estimate, not a field test.", *w*100)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Network + HTTP handler
// ---------------------------------------------------------------------------

func fetchPowerSeries(ctx context.Context, client *http.Client, lat, lon float64, today time.Time) (powerSeries, error) {
	start := today.AddDate(-conditionsYears, 0, -(conditionsWindowDays + 5))
	params := url.Values{
		"parameters": {"PRECTOTCORR,T2M,T2M_MAX,GWETROOT"},
		"community":  {"AG"},
		"longitude":  {strconv.FormatFloat(lon, 'f', 4, 64)},
		"latitude":   {strconv.FormatFloat(lat, 'f', 4, 64)},
		"start":      {dayKey(start)},
		"end":        {dayKey(today)},
		"format":     {"JSON"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nasaBaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return powerSeries{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return powerSeries{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return powerSeries{}, fmt.Errorf("NASA POWER returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Properties struct {
			Parameter map[string]map[string]float64 `json:"parameter"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&payload); err != nil {
		return powerSeries{}, fmt.Errorf("decode NASA POWER response: %w", err)
	}
	pm := payload.Properties.Parameter
	return powerSeries{rain: pm["PRECTOTCORR"], temp: pm["T2M"], tmax: pm["T2M_MAX"], root: pm["GWETROOT"]}, nil
}

func (s *app) conditions(w http.ResponseWriter, r *http.Request) {
	if s.conditionsLimiter != nil && !s.conditionsLimiter.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many requests. Please wait a moment.")
		return
	}
	lat, errLat := parseFloat(r.URL.Query().Get("lat"))
	lon, errLon := parseFloat(r.URL.Query().Get("lon"))
	if errLat != nil || errLon != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		writeError(w, http.StatusBadRequest, "Choose a valid location first.")
		return
	}
	lat, lon = roundCoordinate(lat), roundCoordinate(lon)
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	rep, err := s.conditionsFor(ctx, lat, lon, time.Now().UTC())
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// conditionsFor returns the (cached) recent-conditions report for a rounded
// location. Errors are already phrased for the farmer.
func (s *app) conditionsFor(ctx context.Context, lat, lon float64, today time.Time) (conditionsReport, error) {
	key := formatFloat(lat) + "," + formatFloat(lon)
	if rep, ok := s.cachedConditions(key); ok {
		return rep, nil
	}
	series, err := fetchPowerSeries(ctx, s.httpClient, lat, lon, today)
	if err != nil {
		log.Printf("recent conditions request failed: %v", err)
		return conditionsReport{}, errors.New("Recent NASA data could not be retrieved right now. Please try again later.")
	}
	rep, err := analyzeConditions(series, today)
	if err != nil {
		log.Printf("recent conditions analysis failed: %v", err)
		return conditionsReport{}, errors.New("NASA does not have enough recent data for this location yet.")
	}
	s.storeConditions(key, rep)
	return rep, nil
}

func (s *app) cachedConditions(key string) (conditionsReport, bool) {
	s.conditionsMu.Lock()
	defer s.conditionsMu.Unlock()
	c, ok := s.conditionsCache[key]
	if !ok || time.Now().After(c.expires) {
		return conditionsReport{}, false
	}
	return c.report, true
}

func (s *app) storeConditions(key string, rep conditionsReport) {
	s.conditionsMu.Lock()
	defer s.conditionsMu.Unlock()
	if s.conditionsCache == nil || len(s.conditionsCache) >= conditionsCacheMax {
		s.conditionsCache = make(map[string]cachedConditions)
	}
	s.conditionsCache[key] = cachedConditions{report: rep, expires: time.Now().Add(conditionsCacheTTL)}
}
