package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const nasaBaseURL = "https://power.larc.nasa.gov/api/temporal/daily/point"
const countriesURL = "https://api.worldbank.org/v2/country?format=json&per_page=400"
const geocodingURL = "https://geocoding-api.open-meteo.com/v1/search"

type app struct {
	db               *sql.DB
	httpClient       *http.Client
	countryCacheMu   sync.Mutex
	countryCache     []cachedCountry
	countryCacheTill time.Time
	chatLimiter      *rateLimiter

	conditionsLimiter *rateLimiter
	conditionsMu      sync.Mutex
	conditionsCache   map[string]cachedConditions
	forecastMu        sync.Mutex
	forecastCache     map[string]cachedForecast
}

type planRequest struct {
	Location        string   `json:"location"`
	Continent       string   `json:"continent"`
	Country         string   `json:"country"`
	CountryCode     string   `json:"country_code"`
	Region          string   `json:"region"`
	District        string   `json:"district"`
	Locality        string   `json:"locality"`
	Latitude        float64  `json:"latitude"`
	Longitude       float64  `json:"longitude"`
	SoilType        string   `json:"soil_type"`
	SoilPH          *float64 `json:"soil_ph"`
	CurrentRotation []string `json:"current_rotation"`
	Priorities      []string `json:"priorities"`
}

type countryOption struct {
	Name string `json:"name"`
	Code string `json:"code"`
}

type placeOption struct {
	Name        string  `json:"name"`
	Country     string  `json:"country"`
	CountryCode string  `json:"country_code"`
	Admin1      string  `json:"admin1,omitempty"`
	Admin2      string  `json:"admin2,omitempty"`
	Admin3      string  `json:"admin3,omitempty"`
	Admin4      string  `json:"admin4,omitempty"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

type seasonClimate struct {
	RainfallMM           float64 `json:"rainfall_mm"`
	MeanTempC            float64 `json:"mean_temp_c"`
	HeatDays             float64 `json:"heat_days"`
	DrySpells            float64 `json:"dry_spells"`
	EvapotranspirationMM float64 `json:"evapotranspiration_mm"`
	RootWetness          float64 `json:"root_wetness"`
	SeasonLengthDays     float64 `json:"season_length_days"`
	RainfallTrendMM      float64 `json:"rainfall_trend_mm_per_decade"`
	YearsOfData          int     `json:"years_of_data"`
	StartMonth           int     `json:"start_month"`
	EndMonth             int     `json:"end_month"`
	Label                string  `json:"label"`
}

type climateReport struct {
	SeasonA seasonClimate `json:"season_a"`
	SeasonB seasonClimate `json:"season_b"`
}

type planResponse struct {
	ID              int64         `json:"id,omitempty"`
	Rotation        []string      `json:"rotation"`
	Confidence      string        `json:"confidence"`
	Headline        string        `json:"headline"`
	Reasons         []string      `json:"reasons"`
	Risks           []string      `json:"risks"`
	Climate         climateReport `json:"climate"`
	DataSource      string        `json:"data_source"`
	DataPeriod      string        `json:"data_period"`
	Recommendations []cropAdvice  `json:"recommendations"`
	CreatedAt       time.Time     `json:"created_at,omitempty"`
}

type cropAdvice struct {
	Crop   string `json:"crop"`
	Season string `json:"season"`
	Reason string `json:"reason"`
}

type crop struct {
	name         string
	family       string
	waterNeed    float64
	tempMin      float64
	tempMax      float64
	maturityDays float64
	nitrogen     float64
	drought      float64
	value        float64
	food         float64
}

var crops = []crop{
	{name: "Beans", family: "legume", waterNeed: 350, tempMin: 18, tempMax: 28, maturityDays: 90, nitrogen: 1, drought: .55, value: .7, food: .9},
	{name: "Maize", family: "grass", waterNeed: 520, tempMin: 18, tempMax: 32, maturityDays: 120, nitrogen: -1, drought: .35, value: .85, food: 1},
	{name: "Sorghum", family: "grass", waterNeed: 420, tempMin: 20, tempMax: 35, maturityDays: 100, nitrogen: -.5, drought: .85, value: .6, food: .75},
	{name: "Soybean", family: "legume", waterNeed: 400, tempMin: 20, tempMax: 30, maturityDays: 110, nitrogen: 1, drought: .65, value: .8, food: .65},
	{name: "Sweet potato", family: "root", waterNeed: 450, tempMin: 20, tempMax: 32, maturityDays: 120, nitrogen: .2, drought: .7, value: .65, food: .8},
	{name: "Groundnut", family: "legume", waterNeed: 430, tempMin: 20, tempMax: 30, maturityDays: 110, nitrogen: 1, drought: .72, value: .72, food: .7},
}

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required; see .env.example")
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		log.Fatalf("open PostgreSQL connection: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("connect to PostgreSQL: %v", err)
	}
	if err := createSchema(ctx, db); err != nil {
		log.Fatalf("initialize PostgreSQL schema: %v", err)
	}

	server := &app{db: db, httpClient: &http.Client{Timeout: 45 * time.Second}, chatLimiter: newRateLimiter(chatRequestsPerMin), conditionsLimiter: newRateLimiter(30)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", server.health)
	mux.HandleFunc("GET /api/locations/countries", server.listCountries)
	mux.HandleFunc("GET /api/locations/search", server.searchPlaces)
	mux.HandleFunc("GET /api/locations/reverse", server.reverseLocation)
	mux.HandleFunc("GET /api/conditions", server.conditions)
	mux.HandleFunc("GET /api/forecast", server.forecast)
	mux.HandleFunc("POST /api/plans", server.createPlan)
	mux.HandleFunc("POST /api/chat", server.chat)
	mux.HandleFunc("GET /", serveAsset("index.html", "text/html; charset=utf-8"))
	mux.HandleFunc("GET /styles.css", serveAsset("styles.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /script.js", serveAsset("script.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /api-config.js", serveAsset("api-config.js", "text/javascript; charset=utf-8"))

	addr := envOr("ADDR", ":"+envOr("PORT", "8080"))
	log.Printf("AgriSmart listening on %s", addr)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           withSecurityHeaders(withCORS(os.Getenv("ALLOWED_ORIGINS"), mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      55 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Fatal(httpServer.ListenAndServe())
}

func createSchema(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS farm_plans (
			id BIGSERIAL PRIMARY KEY,
			location TEXT NOT NULL DEFAULT '',
			continent TEXT NOT NULL DEFAULT '',
			country TEXT NOT NULL DEFAULT '',
			country_code TEXT NOT NULL DEFAULT '',
			region TEXT NOT NULL DEFAULT '',
			district TEXT NOT NULL DEFAULT '',
			locality TEXT NOT NULL DEFAULT '',
			latitude DOUBLE PRECISION NOT NULL,
			longitude DOUBLE PRECISION NOT NULL,
			soil_type TEXT NOT NULL DEFAULT '',
			soil_ph DOUBLE PRECISION,
			priorities JSONB NOT NULL,
			current_rotation JSONB NOT NULL,
			result JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	for _, column := range []string{
		`ALTER TABLE farm_plans ADD COLUMN IF NOT EXISTS continent TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE farm_plans ADD COLUMN IF NOT EXISTS country TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE farm_plans ADD COLUMN IF NOT EXISTS country_code TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE farm_plans ADD COLUMN IF NOT EXISTS region TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE farm_plans ADD COLUMN IF NOT EXISTS district TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE farm_plans ADD COLUMN IF NOT EXISTS locality TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.ExecContext(ctx, column); err != nil {
			return err
		}
	}
	_, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS farm_plans_created_at_idx ON farm_plans (created_at DESC)`)
	return err
}

func (s *app) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Database is unavailable.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *app) listCountries(w http.ResponseWriter, r *http.Request) {
	continent := strings.TrimSpace(r.URL.Query().Get("continent"))
	if !supportedContinent(continent) {
		writeError(w, http.StatusBadRequest, "Choose a supported continent.")
		return
	}
	allCountries, err := s.getCountries(r.Context())
	if err != nil {
		log.Printf("load country list: %v", err)
		writeError(w, http.StatusBadGateway, "Country choices could not be loaded. Please try again.")
		return
	}
	countries := make([]countryOption, 0)
	for _, country := range allCountries {
		if country.Continent == continent {
			countries = append(countries, country.Option)
		}
	}
	writeJSON(w, http.StatusOK, countries)
}

type cachedCountry struct {
	Option    countryOption
	Continent string
}

func (s *app) getCountries(ctx context.Context) ([]cachedCountry, error) {
	s.countryCacheMu.Lock()
	if time.Now().Before(s.countryCacheTill) {
		cached := append([]cachedCountry(nil), s.countryCache...)
		s.countryCacheMu.Unlock()
		return cached, nil
	}
	s.countryCacheMu.Unlock()

	requestCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, countriesURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("REST Countries returned HTTP %d", resp.StatusCode)
	}
	var response []json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode country data: %w", err)
	}
	if len(response) != 2 {
		return nil, errors.New("World Bank country response is incomplete")
	}
	var rows []struct {
		Name   string `json:"name"`
		Code   string `json:"iso2Code"`
		Region struct {
			Value string `json:"value"`
		} `json:"region"`
	}
	if err := json.Unmarshal(response[1], &rows); err != nil {
		return nil, fmt.Errorf("decode World Bank country rows: %w", err)
	}
	countries := make([]cachedCountry, 0, len(rows))
	for _, item := range rows {
		if item.Name == "" || len(item.Code) != 2 || item.Region.Value == "Aggregates" {
			continue
		}
		continent := countryContinent(item.Code, item.Region.Value)
		if continent == "" {
			continue
		}
		country := cachedCountry{
			Option:    countryOption{Name: item.Name, Code: strings.ToUpper(item.Code)},
			Continent: continent,
		}
		countries = append(countries, country)
	}
	sort.Slice(countries, func(i, j int) bool {
		return countries[i].Option.Name < countries[j].Option.Name
	})
	s.countryCacheMu.Lock()
	s.countryCache = countries
	s.countryCacheTill = time.Now().Add(24 * time.Hour)
	s.countryCacheMu.Unlock()
	return countries, nil
}

func countryContinent(code, region string) string {
	region = strings.TrimSpace(region)
	asiaCodes := map[string]bool{
		"AF": true, "AM": true, "AZ": true, "BH": true, "BD": true, "BT": true, "BN": true,
		"KH": true, "CN": true, "CY": true, "GE": true, "IN": true, "ID": true, "IR": true,
		"IQ": true, "IL": true, "JP": true, "JO": true, "KZ": true, "KW": true, "KG": true,
		"LA": true, "LB": true, "MY": true, "MV": true, "MN": true, "MM": true, "NP": true,
		"KP": true, "OM": true, "PK": true, "PS": true, "PH": true, "QA": true, "SA": true,
		"SG": true, "KR": true, "LK": true, "SY": true, "TW": true, "TJ": true, "TH": true,
		"TL": true, "TR": true, "TM": true, "AE": true, "UZ": true, "VN": true, "YE": true,
	}
	oceaniaCodes := map[string]bool{
		"AS": true, "AU": true, "CK": true, "FJ": true, "PF": true, "GU": true, "KI": true,
		"MH": true, "FM": true, "NR": true, "NC": true, "NZ": true, "NU": true, "NF": true,
		"MP": true, "PW": true, "PG": true, "WS": true, "SB": true, "TK": true, "TO": true,
		"TV": true, "VU": true, "WF": true,
	}
	southAmericaCodes := map[string]bool{
		"AR": true, "BO": true, "BR": true, "CL": true, "CO": true, "EC": true, "FK": true,
		"GF": true, "GY": true, "PE": true, "PY": true, "SR": true, "UY": true, "VE": true,
	}
	switch region {
	case "Sub-Saharan Africa":
		return "Africa"
	case "North America":
		return "North America"
	case "Latin America & Caribbean":
		if southAmericaCodes[code] {
			return "South America"
		}
		return "North America"
	case "South Asia":
		return "Asia"
	case "East Asia & Pacific":
		if oceaniaCodes[code] {
			return "Oceania"
		}
		return "Asia"
	case "Europe & Central Asia":
		if asiaCodes[code] {
			return "Asia"
		}
		return "Europe"
	case "Middle East, North Africa, Afghanistan & Pakistan":
		if asiaCodes[code] {
			return "Asia"
		}
		return "Africa"
	default:
		return ""
	}
}

func (s *app) searchPlaces(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	countryCode := strings.ToUpper(strings.TrimSpace(query.Get("country_code")))
	name := strings.TrimSpace(query.Get("name"))
	region := strings.TrimSpace(query.Get("region"))
	district := strings.TrimSpace(query.Get("district"))
	language := normalizeGeocodingLanguage(query.Get("language"))
	if len(countryCode) != 2 || name == "" || len(name) > 100 || len(region) > 100 || len(district) > 100 {
		writeError(w, http.StatusBadRequest, "Choose a country and enter a town, village, or nearby place.")
		return
	}
	requestCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	params := url.Values{
		"name":        {name},
		"count":       {"20"},
		"language":    {language},
		"format":      {"json"},
		"countryCode": {countryCode},
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, geocodingURL+"?"+params.Encode(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not search for this place.")
		return
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		log.Printf("place search request failed: %v", err)
		writeError(w, http.StatusBadGateway, "Place search is unavailable. Please try again.")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("Open-Meteo Geocoding returned HTTP %d", resp.StatusCode)
		writeError(w, http.StatusBadGateway, "Place search is unavailable. Please try again.")
		return
	}
	var response struct {
		Results []placeOption `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&response); err != nil {
		log.Printf("decode place search results: %v", err)
		writeError(w, http.StatusBadGateway, "Place search returned invalid results.")
		return
	}
	places := make([]placeOption, 0, len(response.Results))
	for _, place := range response.Results {
		if !strings.EqualFold(place.CountryCode, countryCode) ||
			math.IsNaN(place.Latitude) || math.IsInf(place.Latitude, 0) ||
			math.IsNaN(place.Longitude) || math.IsInf(place.Longitude, 0) ||
			place.Latitude < -90 || place.Latitude > 90 || place.Longitude < -180 || place.Longitude > 180 {
			continue
		}
		if region != "" && place.Admin1 != "" && !containsFold(place.Admin1, region) {
			continue
		}
		adminAreas := strings.TrimSpace(place.Admin2 + " " + place.Admin3 + " " + place.Admin4)
		if district != "" && adminAreas != "" && !containsFold(adminAreas, district) {
			continue
		}
		places = append(places, place)
	}
	writeJSON(w, http.StatusOK, places)
}

func normalizeGeocodingLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if separator := strings.IndexAny(language, "-_"); separator >= 0 {
		language = language[:separator]
	}
	if len(language) != 2 || language[0] < 'a' || language[0] > 'z' || language[1] < 'a' || language[1] > 'z' {
		return "en"
	}
	return language
}

func supportedContinent(continent string) bool {
	switch continent {
	case "Africa", "Asia", "Europe", "North America", "South America", "Oceania":
		return true
	default:
		return false
	}
}

func containsFold(value, query string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(query))
}

func (s *app) createPlan(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var request planRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "Please check the farm details and try again.")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "Send one farm plan at a time.")
		return
	}
	if err := validateRequest(&request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	climate, startYear, endYear, err := fetchClimate(r.Context(), s.httpClient, request.Latitude, request.Longitude)
	if err != nil {
		log.Printf("NASA POWER data request failed: %v", err)
		writeError(w, http.StatusBadGateway, "Climate data could not be retrieved right now. Please try again later.")
		return
	}
	result := recommend(request, climate, startYear, endYear)

	prioritiesJSON, err := json.Marshal(request.Priorities)
	if err != nil {
		log.Printf("encode priorities: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not save this plan.")
		return
	}
	currentJSON, err := json.Marshal(request.CurrentRotation)
	if err != nil {
		log.Printf("encode previous crop: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not save this plan.")
		return
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		log.Printf("encode recommendation: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not save this plan.")
		return
	}
	err = s.db.QueryRowContext(r.Context(), `
		INSERT INTO farm_plans (location, continent, country, country_code, region, district, locality,
			latitude, longitude, soil_type, soil_ph, priorities, current_rotation, result)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, created_at`,
		request.Location, request.Continent, request.Country, request.CountryCode, request.Region, request.District, request.Locality,
		request.Latitude, request.Longitude, request.SoilType, request.SoilPH, prioritiesJSON, currentJSON, resultJSON).
		Scan(&result.ID, &result.CreatedAt)
	if err != nil {
		log.Printf("save recommendation: %v", err)
		writeError(w, http.StatusInternalServerError, "Could not save this plan. Please try again.")
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func validateRequest(req *planRequest) error {
	if math.IsNaN(req.Latitude) || math.IsInf(req.Latitude, 0) || req.Latitude < -90 || req.Latitude > 90 {
		return errors.New("Enter a valid latitude between -90 and 90.")
	}
	if math.IsNaN(req.Longitude) || math.IsInf(req.Longitude, 0) || req.Longitude < -180 || req.Longitude > 180 {
		return errors.New("Enter a valid longitude between -180 and 180.")
	}
	if req.SoilPH != nil && (*req.SoilPH < 3 || *req.SoilPH > 10 || math.IsNaN(*req.SoilPH) || math.IsInf(*req.SoilPH, 0)) {
		return errors.New("Soil pH must be between 3 and 10.")
	}
	// GPS lookups can fail to name the country; the coordinates are what the
	// climate data needs, so an empty continent/country code is allowed.
	if (req.Continent != "" && !supportedContinent(req.Continent)) || strings.TrimSpace(req.Country) == "" ||
		len(req.Country) > 100 || (req.CountryCode != "" && len(req.CountryCode) != 2) {
		return errors.New("Choose a continent and country.")
	}
	for _, character := range req.CountryCode {
		if character < 'A' || character > 'Z' {
			return errors.New("Choose a valid country.")
		}
	}
	if strings.TrimSpace(req.Region) == "" || strings.TrimSpace(req.Locality) == "" {
		return errors.New("Enter your state or region and a town or village.")
	}
	if len(req.Location) > 300 || len(req.Region) > 100 || len(req.District) > 100 || len(req.Locality) > 100 {
		return errors.New("Location details are too long. Please shorten them.")
	}
	if req.SoilType != "" && req.SoilType != "loam" && req.SoilType != "sandy_loam" && req.SoilType != "clay" {
		return errors.New("Choose a soil type from the list.")
	}
	if len(req.Priorities) == 0 || len(req.Priorities) > 3 {
		return errors.New("Choose one to three farm priorities.")
	}
	allowed := map[string]bool{"income": true, "food": true, "soil": true, "water": true, "risk": true}
	seen := make(map[string]bool)
	for _, priority := range req.Priorities {
		if !allowed[priority] || seen[priority] {
			return errors.New("One or more farm priorities are invalid.")
		}
		seen[priority] = true
	}
	for _, name := range req.CurrentRotation {
		found := false
		for _, knownCrop := range crops {
			if strings.EqualFold(name, knownCrop.name) {
				found = true
				break
			}
		}
		if !found {
			return errors.New("Choose a crop from the list for last season.")
		}
	}
	return nil
}

func fetchClimate(ctx context.Context, client *http.Client, latitude, longitude float64) (climateReport, int, int, error) {
	endYear := time.Now().UTC().Year() - 1
	startYear := endYear - 9
	params := url.Values{
		"parameters": {"PRECTOTCORR,T2M,T2M_MAX,GWETROOT,EVPTRNS"},
		"community":  {"AG"},
		"longitude":  {strconv.FormatFloat(longitude, 'f', 4, 64)},
		"latitude":   {strconv.FormatFloat(latitude, 'f', 4, 64)},
		"start":      {fmt.Sprintf("%d0101", startYear)},
		"end":        {fmt.Sprintf("%d1231", endYear)},
		"format":     {"JSON"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, nasaBaseURL+"?"+params.Encode(), nil)
	if err != nil {
		return climateReport{}, 0, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return climateReport{}, 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return climateReport{}, 0, 0, fmt.Errorf("NASA POWER returned HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Properties struct {
			Parameter map[string]map[string]float64 `json:"parameter"`
		} `json:"properties"`
		Messages []string `json:"messages"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 12<<20)).Decode(&payload); err != nil {
		return climateReport{}, 0, 0, fmt.Errorf("decode NASA POWER response: %w", err)
	}
	rain, hasRain := payload.Properties.Parameter["PRECTOTCORR"]
	temp, hasTemp := payload.Properties.Parameter["T2M"]
	if !hasRain || !hasTemp || len(rain) == 0 || len(temp) == 0 {
		return climateReport{}, 0, 0, fmt.Errorf("NASA POWER response has no usable rainfall or temperature data: %s", strings.Join(payload.Messages, "; "))
	}
	windowA, windowB := chooseRainWindows(rain, startYear, endYear)
	report := climateReport{
		SeasonA: summarizeSeason(rain, temp, payload.Properties.Parameter["T2M_MAX"], payload.Properties.Parameter["EVPTRNS"], payload.Properties.Parameter["GWETROOT"], startYear, endYear, windowA),
		SeasonB: summarizeSeason(rain, temp, payload.Properties.Parameter["T2M_MAX"], payload.Properties.Parameter["EVPTRNS"], payload.Properties.Parameter["GWETROOT"], startYear, endYear, windowB),
	}
	if report.SeasonA.YearsOfData < 3 || report.SeasonB.YearsOfData < 3 {
		return climateReport{}, 0, 0, errors.New("NASA POWER returned too few complete seasons")
	}
	return report, startYear, endYear, nil
}

type yearStats struct {
	rain, tempSum, etSum, rootSum float64
	tempCount, etCount, rootCount int
	heatDays, drySpells           float64
	days, currentDryRun           int
	firstWetDay, lastWetDay       int
}

type seasonWindow struct {
	start int
	end   int
}

func chooseRainWindows(rain map[string]float64, startYear, endYear int) (seasonWindow, seasonWindow) {
	type yearMonth struct {
		year  int
		month int
	}
	annualRain := make(map[yearMonth]float64)
	dates := make([]string, 0, len(rain))
	for dateKey := range rain {
		dates = append(dates, dateKey)
	}
	sort.Strings(dates)
	for _, dateKey := range dates {
		if len(dateKey) < 8 {
			continue
		}
		year, errYear := strconv.Atoi(dateKey[:4])
		month, errMonth := strconv.Atoi(dateKey[4:6])
		amount := rain[dateKey]
		if errYear == nil && errMonth == nil && year >= startYear && year <= endYear &&
			month >= 1 && month <= 12 && validPowerValue(amount) {
			annualRain[yearMonth{year: year, month: month}] += amount
		}
	}
	var totals [13]float64
	var sampleYears [13]int
	for key, value := range annualRain {
		totals[key.month] += value
		sampleYears[key.month]++
	}
	for month := 1; month <= 12; month++ {
		if sampleYears[month] > 0 {
			totals[month] /= float64(sampleYears[month])
		}
	}
	type rankedWindow struct {
		window seasonWindow
		rain   float64
	}
	windows := make([]rankedWindow, 0, 10)
	for month := 1; month <= 10; month++ {
		windows = append(windows, rankedWindow{
			window: seasonWindow{start: month, end: month + 2},
			rain:   totals[month] + totals[month+1] + totals[month+2],
		})
	}
	sort.Slice(windows, func(i, j int) bool {
		if windows[i].rain == windows[j].rain {
			return windows[i].window.start < windows[j].window.start
		}
		return windows[i].rain > windows[j].rain
	})
	first := windows[0].window
	for _, candidate := range windows[1:] {
		if candidate.window.end < first.start || candidate.window.start > first.end {
			second := candidate.window
			if second.start < first.start {
				return second, first
			}
			return first, second
		}
	}
	return first, windows[len(windows)-1].window
}

func summarizeSeason(rain, temp, tmax, et, root map[string]float64, start, end int, window seasonWindow) seasonClimate {
	years := make(map[int]*yearStats)
	for year := start; year <= end; year++ {
		years[year] = &yearStats{}
	}
	dates := make([]string, 0, len(rain))
	for dateKey := range rain {
		dates = append(dates, dateKey)
	}
	sort.Strings(dates)
	for _, dateKey := range dates {
		amount := rain[dateKey]
		if len(dateKey) < 8 {
			continue
		}
		year, errYear := strconv.Atoi(dateKey[:4])
		month, errMonth := strconv.Atoi(dateKey[4:6])
		if errYear != nil || errMonth != nil || month < window.start || month > window.end || years[year] == nil || !validPowerValue(amount) {
			continue
		}
		day := years[year]
		day.days++
		day.rain += amount
		if amount >= 1 {
			date, dateErr := time.Parse("20060102", dateKey)
			if dateErr == nil {
				dayOfYear := date.YearDay()
				if day.firstWetDay == 0 {
					day.firstWetDay = dayOfYear
				}
				day.lastWetDay = dayOfYear
			}
			if day.currentDryRun >= 7 {
				day.drySpells++
			}
			day.currentDryRun = 0
		} else {
			day.currentDryRun++
		}
		if value, ok := temp[dateKey]; ok {
			day.tempSum, day.tempCount = addValue(day.tempSum, day.tempCount, value)
		}
		if maxTemp, ok := tmax[dateKey]; ok && validPowerValue(maxTemp) && maxTemp > 35 {
			day.heatDays++
		}
		if value, ok := et[dateKey]; ok && validPowerValue(value) {
			day.etSum += value
			day.etCount++
		}
		if value, ok := root[dateKey]; ok && validPowerValue(value) {
			day.rootSum += value
			day.rootCount++
		}
	}

	result := seasonClimate{
		StartMonth: window.start,
		EndMonth:   window.end,
		Label:      monthName(window.start) + "–" + monthName(window.end),
	}
	var rainfallSeries []float64
	for year := start; year <= end; year++ {
		stats := years[year]
		if stats.days < 60 {
			continue
		}
		result.YearsOfData++
		result.RainfallMM += stats.rain
		result.MeanTempC += safeAverage(stats.tempSum, stats.tempCount)
		result.HeatDays += stats.heatDays
		result.DrySpells += stats.drySpells
		if stats.firstWetDay > 0 {
			result.SeasonLengthDays += float64(stats.lastWetDay - stats.firstWetDay + 1)
		}
		if stats.etCount > 0 {
			result.EvapotranspirationMM += stats.etSum
		}
		if stats.rootCount > 0 {
			result.RootWetness += stats.rootSum / float64(stats.rootCount)
		}
		rainfallSeries = append(rainfallSeries, stats.rain)
	}
	if result.YearsOfData > 0 {
		count := float64(result.YearsOfData)
		result.RainfallMM /= count
		result.MeanTempC /= count
		result.HeatDays /= count
		result.DrySpells /= count
		result.SeasonLengthDays /= count
		result.EvapotranspirationMM /= count
		result.RootWetness /= count
		result.RainfallTrendMM = trendPerDecade(rainfallSeries)
	}

	return result
}

func monthName(month int) string {
	return time.Month(month).String()
}

func validPowerValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > -900
}

func addValue(total float64, count int, value float64) (float64, int) {
	if validPowerValue(value) {
		return total + value, count + 1
	}
	return total, count
}

func safeAverage(total float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func trendPerDecade(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	var sumX, sumY, sumXY, sumX2 float64
	for index, value := range values {
		x := float64(index)
		sumX += x
		sumY += value
		sumXY += x * value
		sumX2 += x * x
	}
	n := float64(len(values))
	denominator := n*sumX2 - sumX*sumX
	if denominator == 0 {
		return 0
	}
	return ((n*sumXY - sumX*sumY) / denominator) * 10
}

func recommend(req planRequest, climate climateReport, startYear, endYear int) planResponse {
	seasons := []seasonClimate{climate.SeasonA, climate.SeasonB, climate.SeasonA, climate.SeasonB}
	seasonNames := []string{
		"Season A (" + climate.SeasonA.Label + ")",
		"Season B (" + climate.SeasonB.Label + ")",
		"Season A next year (" + climate.SeasonA.Label + ")",
		"Season B next year (" + climate.SeasonB.Label + ")",
	}
	rotation := make([]string, 0, 4)
	advice := make([]cropAdvice, 0, 4)
	reasons := make([]string, 0, 6)
	lastFamily := ""
	lastNitrogen := 0.0
	prioritySet := make(map[string]bool)
	for _, priority := range req.Priorities {
		prioritySet[priority] = true
	}
	for _, currentCrop := range req.CurrentRotation {
		for _, knownCrop := range crops {
			if strings.EqualFold(currentCrop, knownCrop.name) {
				lastFamily, lastNitrogen = knownCrop.family, knownCrop.nitrogen
				break
			}
		}
	}
	for i, season := range seasons {
		bestCrop, bestScore, bestFit := crops[0], -1.0, 0.0
		for _, candidate := range crops {
			fit := cropFit(candidate, season)
			score := fit
			if prioritySet["income"] {
				score += candidate.value * .08
			}
			if prioritySet["food"] {
				score += candidate.food * .08
			}
			if prioritySet["soil"] {
				score += candidate.nitrogen * .04
			}
			if prioritySet["water"] {
				score += (1 - math.Min(1, candidate.waterNeed/600)) * .08
			}
			if prioritySet["risk"] {
				score += candidate.drought * .08
			}
			if candidate.family == lastFamily {
				score = -1
			}
			if lastNitrogen < 0 && candidate.nitrogen > 0 {
				score += .1
			}
			if score > bestScore {
				bestCrop, bestScore, bestFit = candidate, score, fit
			}
		}
		rotation = append(rotation, bestCrop.name)
		advice = append(advice, cropAdvice{Crop: bestCrop.name, Season: seasonNames[i], Reason: cropReason(bestCrop, season, bestFit)})
		lastFamily, lastNitrogen = bestCrop.family, bestCrop.nitrogen
	}

	if strings.TrimSpace(req.SoilType) == "" {
		reasons = append(reasons, "You left soil type blank, so the plan does not assume a soil type.")
	} else {
		reasons = append(reasons, "Soil type was recorded as "+strings.ReplaceAll(req.SoilType, "_", " ")+". Crop suitability still depends on a local soil test.")
	}
	if req.SoilPH != nil && *req.SoilPH < 5.5 {
		reasons = append(reasons, "Your entered soil pH is below 5.5; acidity can limit legumes, so ask an extension worker about soil treatment.")
	}
	if climate.SeasonA.RainfallTrendMM < 0 || climate.SeasonB.RainfallTrendMM < 0 {
		reasons = append(reasons, "At least one rainy season shows a declining rainfall trend in this historical record.")
	} else {
		reasons = append(reasons, "The rainfall trend is calculated from the available historical record; it is not a forecast.")
	}
	if prioritySet["soil"] {
		reasons = append(reasons, "The rotation includes nitrogen-building legumes and avoids repeating the same crop family in consecutive seasons.")
	}
	risks := []string{
		"NASA POWER is a broad-area estimate, not a measurement from your exact field.",
		"Crop water needs and market values are indicative starting values; confirm varieties, prices, and local conditions before planting.",
		"The rainfall-based three-month windows are not a confirmed local planting calendar; check planting dates with a local extension worker.",
	}
	if climate.SeasonA.YearsOfData < 10 || climate.SeasonB.YearsOfData < 10 {
		risks = append(risks, "Fewer than 10 complete years were available for at least one season, so treat its climate averages cautiously.")
	}
	if climate.SeasonA.RainfallMM < 400 || climate.SeasonB.RainfallMM < 400 {
		risks = append(risks, "At least one rainy season has average rainfall below 400 mm; consider a drought-tolerant variety and verify water access.")
	}
	confidence := "Medium"
	if climate.SeasonA.YearsOfData >= 10 && climate.SeasonB.YearsOfData >= 10 && len(risks) == 3 {
		confidence = "Higher data coverage"
	}
	location := strings.TrimSpace(req.Location)
	if location == "" {
		location = fmt.Sprintf("%.3f, %.3f", req.Latitude, req.Longitude)
	}
	return planResponse{
		Rotation: rotation, Confidence: confidence,
		Headline: fmt.Sprintf("A starting point for %s based on recent rainfall and temperature patterns.", location),
		Reasons:  reasons, Risks: risks, Climate: climate,
		DataSource: "NASA POWER daily point data", DataPeriod: fmt.Sprintf("%d–%d", startYear, endYear),
		Recommendations: advice,
	}
}

func cropFit(c crop, season seasonClimate) float64 {
	water := math.Min(1, season.RainfallMM/c.waterNeed)
	temperature := 1.0
	if season.MeanTempC < c.tempMin {
		temperature = math.Max(.2, 1-(c.tempMin-season.MeanTempC)/12)
	} else if season.MeanTempC > c.tempMax {
		temperature = math.Max(.2, 1-(season.MeanTempC-c.tempMax)/12)
	}
	length := math.Min(1, season.SeasonLengthDays/c.maturityDays)
	stress := (season.HeatDays/40 + season.DrySpells/5) * (1 - c.drought)
	return math.Max(.05, math.Min(1, math.Min(water, math.Min(temperature, length))-stress))
}

func cropReason(c crop, season seasonClimate, fit float64) string {
	fitText := "may face water or season-length stress"
	if fit >= .8 {
		fitText = "has a good fit with the average rainfall and season length"
	} else if fit >= .6 {
		fitText = "has a moderate fit with the average rainfall and season length"
	}
	if c.nitrogen > 0 {
		return fmt.Sprintf("%s %s and is a legume that can contribute nitrogen to the rotation.", fitText, c.name)
	}
	if c.drought >= .7 {
		return fmt.Sprintf("%s %s and is relatively tolerant of dry conditions.", fitText, c.name)
	}
	return fmt.Sprintf("%s %s.", fitText, c.name)
}

func serveAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		http.ServeFile(w, r, name)
	}
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func withCORS(allowedOrigins string, next http.Handler) http.Handler {
	origins := make(map[string]struct{})
	for _, origin := range strings.Split(allowedOrigins, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins[origin] = struct{}{}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		_, allowed := origins[origin]
		if r.Method == http.MethodOptions {
			if origin == "" || !allowed {
				http.Error(w, "Origin is not allowed.", http.StatusForbidden)
				return
			}
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if allowed {
			w.Header().Add("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
