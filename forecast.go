package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openMeteoForecastURL = "https://api.open-meteo.com/v1/forecast"
	meteomaticsBaseURL   = "https://api.meteomatics.com"
	forecastHorizonDays  = 14
	forecastCacheTTL     = time.Hour
	forecastCacheMax     = 500
)

type forecastDay struct {
	Date   string  `json:"date"`
	RainMM float64 `json:"rain_mm"`
	TMaxC  float64 `json:"tmax_c"`
	TMinC  float64 `json:"tmin_c"`
}

type forecastReport struct {
	Provider      string             `json:"provider"`
	Days          []forecastDay      `json:"days"`
	Rain7MM       float64            `json:"rain_next_7_days_mm"`
	Rain14MM      float64            `json:"rain_next_14_days_mm"`
	RainyDays7    int                `json:"rainy_days_next_7"`
	FirstRainDate string             `json:"first_rain_date,omitempty"`
	Findings      []conditionFinding `json:"findings"`
	Outlook       []conditionFinding `json:"outlook,omitempty"`
	Note          string             `json:"note"`
}

type cachedForecast struct {
	report  forecastReport
	expires time.Time
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

func fetchOpenMeteo(ctx context.Context, client *http.Client, lat, lon float64) ([]forecastDay, error) {
	params := url.Values{
		"latitude":      {formatFloat(lat)},
		"longitude":     {formatFloat(lon)},
		"daily":         {"precipitation_sum,temperature_2m_max,temperature_2m_min"},
		"forecast_days": {fmt.Sprint(forecastHorizonDays)},
		"timezone":      {"auto"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openMeteoForecastURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Open-Meteo returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Daily struct {
			Time []string   `json:"time"`
			Rain []*float64 `json:"precipitation_sum"`
			TMax []*float64 `json:"temperature_2m_max"`
			TMin []*float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode Open-Meteo response: %w", err)
	}
	d := body.Daily
	var days []forecastDay
	for i, date := range d.Time {
		if i >= len(d.Rain) || i >= len(d.TMax) || i >= len(d.TMin) || d.Rain[i] == nil || d.TMax[i] == nil || d.TMin[i] == nil {
			continue // skip days with gaps rather than inventing values
		}
		days = append(days, forecastDay{Date: date, RainMM: *d.Rain[i], TMaxC: *d.TMax[i], TMinC: *d.TMin[i]})
	}
	return days, nil
}

// fetchMeteomatics uses the Meteomatics Weather API (basic auth). Its 24-hour
// parameters are accumulated or extreme values over the 24 hours *ending* at
// the requested time, so each value is assigned to the previous UTC day.
// Any failure here makes the caller fall back to Open-Meteo.
func fetchMeteomatics(ctx context.Context, client *http.Client, user, pass string, lat, lon float64, now time.Time) ([]forecastDay, error) {
	first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	last := first.AddDate(0, 0, forecastHorizonDays-1)
	const layout = "2006-01-02T15:04:05Z"
	path := fmt.Sprintf("%s/%s--%s:P1D/precip_24h:mm,t_max_2m_24h:C,t_min_2m_24h:C/%s,%s/json",
		meteomaticsBaseURL, first.Format(layout), last.Format(layout), formatFloat(lat), formatFloat(lon))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, pass)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Meteomatics returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			Parameter   string `json:"parameter"`
			Coordinates []struct {
				Dates []struct {
					Date  string   `json:"date"`
					Value *float64 `json:"value"`
				} `json:"dates"`
			} `json:"coordinates"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode Meteomatics response: %w", err)
	}
	byDay := map[string]*forecastDay{}
	seen := map[string]map[string]bool{}
	for _, series := range body.Data {
		if len(series.Coordinates) == 0 {
			continue
		}
		for _, point := range series.Coordinates[0].Dates {
			stamp, err := time.Parse(time.RFC3339, point.Date)
			if err != nil || point.Value == nil {
				continue
			}
			day := stamp.UTC().AddDate(0, 0, -1).Format("2006-01-02")
			entry := byDay[day]
			if entry == nil {
				entry = &forecastDay{Date: day}
				byDay[day] = entry
				seen[day] = map[string]bool{}
			}
			switch series.Parameter {
			case "precip_24h:mm":
				entry.RainMM = *point.Value
			case "t_max_2m_24h:C":
				entry.TMaxC = *point.Value
			case "t_min_2m_24h:C":
				entry.TMinC = *point.Value
			default:
				continue
			}
			seen[day][series.Parameter] = true
		}
	}
	var days []forecastDay
	for i := 0; i < forecastHorizonDays; i++ {
		day := first.AddDate(0, 0, i-1).Format("2006-01-02")
		if entry := byDay[day]; entry != nil && len(seen[day]) == 3 {
			days = append(days, *entry)
		}
	}
	return days, nil
}

// ---------------------------------------------------------------------------
// Analysis
// ---------------------------------------------------------------------------

func prettyDate(date string) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return t.Format("Mon 2 Jan")
}

func analyzeForecast(days []forecastDay, provider string) (forecastReport, error) {
	if len(days) < 7 {
		return forecastReport{}, errors.New("forecast has fewer than 7 usable days")
	}
	rep := forecastReport{
		Provider: provider,
		Days:     days,
		Note:     "Forecasts become less certain after about 5 days. Check again before making planting or spraying decisions.",
	}
	var heaviest, coldest, hottest forecastDay
	coldest = days[0]
	hottest = days[0]
	hotDays := 0
	for i, d := range days {
		rep.Rain14MM += d.RainMM
		if i < 7 {
			rep.Rain7MM += d.RainMM
			if d.RainMM >= 1 {
				rep.RainyDays7++
			}
			if d.TMaxC >= 35 {
				hotDays++
			}
		}
		if d.RainMM >= 1 && rep.FirstRainDate == "" {
			rep.FirstRainDate = d.Date
		}
		if d.RainMM > heaviest.RainMM {
			heaviest = d
		}
		if d.TMinC < coldest.TMinC {
			coldest = d
		}
		if d.TMaxC > hottest.TMaxC {
			hottest = d
		}
	}

	add := func(topic, level, format string, args ...any) {
		rep.Findings = append(rep.Findings, conditionFinding{Topic: topic, Level: level, Message: fmt.Sprintf(format, args...)})
	}
	if rep.Rain7MM < 5 {
		add("Rain forecast", "ok", "Little or no rain is forecast for the next 7 days (%.0f mm in total).", rep.Rain7MM)
	} else {
		add("Rain forecast", "ok", "About %.0f mm of rain is forecast over the next 7 days, falling on %d days.", rep.Rain7MM, rep.RainyDays7)
	}
	switch {
	case heaviest.RainMM >= 50:
		add("Heavy rain", "alert", "Very heavy rain is forecast on %s (about %.0f mm in one day). Expect runoff, erosion and waterlogging.", prettyDate(heaviest.Date), heaviest.RainMM)
	case heaviest.RainMM >= 25:
		add("Heavy rain", "watch", "A heavy rain day is forecast on %s (about %.0f mm). Runoff and waterlogging are possible.", prettyDate(heaviest.Date), heaviest.RainMM)
	}
	switch {
	case hottest.TMaxC >= 40:
		add("Heat forecast", "alert", "Extreme heat is forecast: up to %.0f°C on %s.", hottest.TMaxC, prettyDate(hottest.Date))
	case hotDays >= 3:
		add("Heat forecast", "watch", "%d of the next 7 days are forecast to reach 35°C or more.", hotDays)
	}
	switch {
	case coldest.TMinC <= 0:
		add("Cold", "alert", "A freezing night is forecast on %s (%.0f°C). Frost can damage young crops.", prettyDate(coldest.Date), coldest.TMinC)
	case coldest.TMinC <= 3:
		add("Cold", "watch", "A near-freezing night is forecast on %s (%.0f°C). Watch for frost.", prettyDate(coldest.Date), coldest.TMinC)
	}
	return rep, nil
}

// combineOutlook joins what has happened recently with what is coming.
func combineOutlook(cond *conditionsReport, fc forecastReport) []conditionFinding {
	if cond == nil {
		return nil
	}
	var out []conditionFinding
	normalDry := cond.Normal.RainfallMM < 15
	if !normalDry {
		ratio := cond.Recent.RainfallMM / cond.Normal.RainfallMM
		switch {
		case cond.DaysSinceRain >= 7 && fc.Rain7MM < 5:
			out = append(out, conditionFinding{Topic: "Dry spell outlook", Level: "alert", Message: fmt.Sprintf(
				"The dry spell is likely to continue: %d days without meaningful rain already, and only %.0f mm is forecast for the next 7 days.", cond.DaysSinceRain, fc.Rain7MM)})
		case ratio < 0.6 && fc.Rain7MM >= 20:
			out = append(out, conditionFinding{Topic: "Rain outlook", Level: "ok", Message: fmt.Sprintf(
				"Relief may be coming: it has been dry lately, and about %.0f mm of rain is forecast over the next 7 days.", fc.Rain7MM)})
		case ratio > 1.5 && maxDayRain(fc.Days[:min(7, len(fc.Days))]) >= 25:
			out = append(out, conditionFinding{Topic: "Waterlogging outlook", Level: "alert", Message: "It has already been much wetter than usual, and more heavy rain is forecast. Fields may stay waterlogged and the risk of erosion and crop disease is higher."})
		}
	}
	return out
}

func maxDayRain(days []forecastDay) float64 {
	best := 0.0
	for _, d := range days {
		if d.RainMM > best {
			best = d.RainMM
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// HTTP handler
// ---------------------------------------------------------------------------

func (s *app) forecast(w http.ResponseWriter, r *http.Request) {
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
	now := time.Now().UTC()

	rep, ok := s.cachedForecast(formatFloat(lat) + "," + formatFloat(lon))
	if !ok {
		var days []forecastDay
		provider := ""
		user := strings.TrimSpace(envOr("METEOMATICS_USER", ""))
		pass := strings.TrimSpace(envOr("METEOMATICS_PASSWORD", ""))
		if user != "" && pass != "" {
			var err error
			if days, err = fetchMeteomatics(ctx, s.httpClient, user, pass, lat, lon, now); err == nil && len(days) >= 7 {
				provider = "Meteomatics"
			} else {
				log.Printf("Meteomatics forecast unavailable, falling back to Open-Meteo: %v (days=%d)", err, len(days))
				days = nil
			}
		}
		if provider == "" {
			var err error
			if days, err = fetchOpenMeteo(ctx, s.httpClient, lat, lon); err != nil {
				log.Printf("forecast request failed: %v", err)
				writeError(w, http.StatusBadGateway, "The weather forecast could not be retrieved right now. Please try again later.")
				return
			}
			provider = "Open-Meteo"
		}
		var err error
		if rep, err = analyzeForecast(days, provider); err != nil {
			log.Printf("forecast analysis failed: %v", err)
			writeError(w, http.StatusBadGateway, "The forecast for this location is incomplete right now.")
			return
		}
		s.storeForecast(formatFloat(lat)+","+formatFloat(lon), rep)
	}

	// Recent conditions are cached, so this is usually free. If they are
	// unavailable the forecast is still returned on its own.
	if cond, err := s.conditionsFor(ctx, lat, lon, now); err == nil {
		rep.Outlook = combineOutlook(&cond, rep)
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *app) cachedForecast(key string) (forecastReport, bool) {
	s.forecastMu.Lock()
	defer s.forecastMu.Unlock()
	c, ok := s.forecastCache[key]
	if !ok || time.Now().After(c.expires) {
		return forecastReport{}, false
	}
	return c.report, true
}

func (s *app) storeForecast(key string, rep forecastReport) {
	s.forecastMu.Lock()
	defer s.forecastMu.Unlock()
	if s.forecastCache == nil || len(s.forecastCache) >= forecastCacheMax {
		s.forecastCache = make(map[string]cachedForecast)
	}
	s.forecastCache[key] = cachedForecast{report: rep, expires: time.Now().Add(forecastCacheTTL)}
}
