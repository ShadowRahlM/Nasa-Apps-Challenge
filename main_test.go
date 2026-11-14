package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateRequestRequiresFarmPriorities(t *testing.T) {
	req := validPlanRequest()
	req.Priorities = []string{}
	if err := validateRequest(&req); err == nil || !strings.Contains(err.Error(), "priorities") {
		t.Fatalf("expected priorities validation error, got %v", err)
	}
}

func TestValidateRequestRejectsOutOfRangeCoordinates(t *testing.T) {
	req := validPlanRequest()
	req.Latitude = 91
	if err := validateRequest(&req); err == nil || !strings.Contains(err.Error(), "latitude") {
		t.Fatalf("expected latitude validation error, got %v", err)
	}
}

func TestCropFitRewardsClimateFit(t *testing.T) {
	beans := crops[0]
	fit := cropFit(beans, seasonClimate{
		RainfallMM: 450, MeanTempC: 24, SeasonLengthDays: 90, HeatDays: 0, DrySpells: 0,
	})
	if fit < .95 {
		t.Fatalf("expected a strong climate fit, got %.2f", fit)
	}

	dryFit := cropFit(beans, seasonClimate{
		RainfallMM: 100, MeanTempC: 24, SeasonLengthDays: 40, HeatDays: 5, DrySpells: 3,
	})
	if dryFit >= fit {
		t.Fatalf("dry climate should score below suitable climate: dry %.2f, suitable %.2f", dryFit, fit)
	}
}

func TestRecommendationUsesPreviousCropToAvoidImmediateFamilyRepeat(t *testing.T) {
	req := planRequest{
		Continent: "Africa", Country: "Uganda", CountryCode: "UG", Region: "Central Region", Locality: "Mukono",
		Latitude: 0, Longitude: 32, CurrentRotation: []string{"Maize"},
		Priorities: []string{"income", "soil"},
	}
	climate := climateReport{
		SeasonA: seasonClimate{RainfallMM: 520, MeanTempC: 25, SeasonLengthDays: 86, YearsOfData: 10},
		SeasonB: seasonClimate{RainfallMM: 480, MeanTempC: 24, SeasonLengthDays: 82, YearsOfData: 10},
	}
	result := recommend(req, climate, 2015, 2024)
	if len(result.Rotation) != 4 {
		t.Fatalf("expected four seasonal crops, got %d", len(result.Rotation))
	}
	if result.Rotation[0] == "Maize" || result.Rotation[0] == "Sorghum" {
		t.Fatalf("first suggestion should avoid repeating the previous grass family, got %q", result.Rotation[0])
	}
	for i := 1; i < len(result.Rotation); i++ {
		if familyFor(result.Rotation[i]) == familyFor(result.Rotation[i-1]) {
			t.Fatalf("consecutive crops should have different families: %q, %q", result.Rotation[i-1], result.Rotation[i])
		}
	}
	if result.DataSource != "NASA POWER daily point data" {
		t.Fatalf("unexpected source: %q", result.DataSource)
	}
}

func familyFor(name string) string {
	for _, crop := range crops {
		if crop.name == name {
			return crop.family
		}
	}
	return ""
}

func TestRainWindowsChooseWetNonOverlappingPeriods(t *testing.T) {
	rain := make(map[string]float64)
	for year := 2019; year <= 2020; year++ {
		for month := 1; month <= 12; month++ {
			amount := 1.0
			if month >= 4 && month <= 6 || month >= 9 && month <= 11 {
				amount = 100
			}
			rain[fmt.Sprintf("%04d%02d01", year, month)] = amount
		}
	}
	first, second := chooseRainWindows(rain, 2019, 2020)
	if first != (seasonWindow{start: 4, end: 6}) || second != (seasonWindow{start: 9, end: 11}) {
		t.Fatalf("expected April–June and September–November windows, got %+v and %+v", first, second)
	}
}

func TestCountryContinentCoversWorldBankRegions(t *testing.T) {
	tests := []struct {
		code, region, expected string
	}{
		{"UG", "Sub-Saharan Africa ", "Africa"},
		{"IN", "South Asia", "Asia"},
		{"KZ", "Europe & Central Asia", "Asia"},
		{"FR", "Europe & Central Asia", "Europe"},
		{"CA", "North America", "North America"},
		{"BR", "Latin America & Caribbean ", "South America"},
		{"NZ", "East Asia & Pacific", "Oceania"},
	}
	for _, test := range tests {
		if got := countryContinent(test.code, test.region); got != test.expected {
			t.Errorf("%s in %q: expected %q, got %q", test.code, test.region, test.expected, got)
		}
	}
}

func TestNormalizeGeocodingLanguage(t *testing.T) {
	tests := map[string]string{
		"fr-FR": "fr",
		"AR":    "ar",
		"sw":    "sw",
		"bad!":  "en",
		"":      "en",
	}
	for input, expected := range tests {
		if got := normalizeGeocodingLanguage(input); got != expected {
			t.Errorf("normalizeGeocodingLanguage(%q) = %q, want %q", input, got, expected)
		}
	}
}

func validPlanRequest() planRequest {
	return planRequest{
		Location:  "Mukono, Central Region, Uganda",
		Continent: "Africa", Country: "Uganda", CountryCode: "UG",
		Region: "Central Region", Locality: "Mukono",
		Latitude: 0, Longitude: 32, Priorities: []string{"income"},
	}
}
