package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestRoundCoordinateKeepsAboutOneKilometre(t *testing.T) {
	if got := roundCoordinate(0.347612); got != 0.35 {
		t.Fatalf("roundCoordinate = %v, want 0.35", got)
	}
	if got := roundCoordinate(-1.2349); got != -1.23 {
		t.Fatalf("roundCoordinate = %v, want -1.23", got)
	}
}

func TestFillFromAddressPrefersMostLocalNames(t *testing.T) {
	var place reversePlace
	fillFromAddress(&place, map[string]string{
		"country_code": "ug", "country": "Uganda", "state": "Central Region",
		"county": "Mukono District", "town": "Mukono",
	})
	if place.CountryCode != "UG" || place.Region != "Central Region" || place.District != "Mukono District" || place.Locality != "Mukono" {
		t.Fatalf("unexpected place: %+v", place)
	}
}

func TestCleanChatMessagesEnforcesAlternationAndEndsWithUser(t *testing.T) {
	got, err := cleanChatMessages([]chatMessage{
		{Role: "assistant", Content: "Hello"},
		{Role: "user", Content: "one"},
		{Role: "user", Content: "two"},
		{Role: "assistant", Content: "answer"},
		{Role: "user", Content: "three"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Role != "user" || got[0].Content != "one\ntwo" || got[2].Content != "three" {
		t.Fatalf("unexpected messages: %+v", got)
	}
	if _, err := cleanChatMessages([]chatMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "yo"}}); err == nil {
		t.Fatal("expected an error when the last message is not from the user")
	}
	if _, err := cleanChatMessages([]chatMessage{{Role: "system", Content: "x"}}); err == nil {
		t.Fatal("expected an error for an invalid role")
	}
}

func TestTruncateDoesNotSplitMultiByteCharacters(t *testing.T) {
	got := truncate("Habari za asubuhi — mkulima", 17)
	if !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
	if got := truncate("ශ්‍රී ලංකා", 4); !utf8.ValidString(got) {
		t.Fatalf("truncate produced invalid UTF-8: %q", got)
	}
}

func TestRateLimiterBlocksAfterLimit(t *testing.T) {
	l := newRateLimiter(2)
	if !l.allow("a") || !l.allow("a") {
		t.Fatal("first two requests should pass")
	}
	if l.allow("a") {
		t.Fatal("third request should be blocked")
	}
	if !l.allow("b") {
		t.Fatal("a different client should not be blocked")
	}
}

func TestValidateRequestAllowsGPSWithoutContinent(t *testing.T) {
	req := &planRequest{
		Latitude: 0.35, Longitude: 32.6, Country: "Unknown country",
		Region: "Unknown region", Locality: "Near 0.35, 32.6", Priorities: []string{"income"},
	}
	if err := validateRequest(req); err != nil {
		t.Fatalf("GPS request without a named continent should be valid: %v", err)
	}
}

func TestReverseLocationFillsPlaceAndContinent(t *testing.T) {
	var gotQuery string
	srv := &app{
		httpClient: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
			gotQuery = r.URL.RawQuery
			if r.Header.Get("User-Agent") == "" {
				t.Error("Nominatim requires a User-Agent")
			}
			return jsonResponse(200, `{"address":{"country_code":"ug","country":"Uganda","state":"Central Region","county":"Mukono District","town":"Mukono"}}`), nil
		})},
		countryCache:     []cachedCountry{{Option: countryOption{Name: "Uganda", Code: "UG"}, Continent: "Africa"}},
		countryCacheTill: time.Now().Add(time.Hour),
	}
	rec := httptest.NewRecorder()
	srv.reverseLocation(rec, httptest.NewRequest("GET", "/api/locations/reverse?lat=0.35348&lon=32.75529", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var place reversePlace
	if err := json.Unmarshal(rec.Body.Bytes(), &place); err != nil {
		t.Fatal(err)
	}
	if place.Continent != "Africa" || place.CountryCode != "UG" || place.Locality != "Mukono" || place.Latitude != 0.35 || place.Longitude != 32.76 {
		t.Fatalf("unexpected place: %+v", place)
	}
	if !strings.Contains(gotQuery, "lat=0.35") || strings.Contains(gotQuery, "0.353") {
		t.Fatalf("coordinates should be rounded before leaving the server: %s", gotQuery)
	}
}

func TestReverseLocationFallsBackWhenLookupFails(t *testing.T) {
	srv := &app{
		httpClient:       &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) { return jsonResponse(503, ""), nil })},
		countryCacheTill: time.Now().Add(time.Hour),
	}
	rec := httptest.NewRecorder()
	srv.reverseLocation(rec, httptest.NewRequest("GET", "/api/locations/reverse?lat=0.35&lon=32.6", nil))
	if rec.Code != 200 {
		t.Fatalf("a failed name lookup must not block the farmer; status %d", rec.Code)
	}
	var place reversePlace
	_ = json.Unmarshal(rec.Body.Bytes(), &place)
	req := &planRequest{Continent: place.Continent, Country: place.Country, CountryCode: place.CountryCode, Region: place.Region,
		Locality: place.Locality, Latitude: place.Latitude, Longitude: place.Longitude, Priorities: []string{"income"}}
	if err := validateRequest(req); err != nil {
		t.Fatalf("fallback place should still pass plan validation: %v", err)
	}
}

func TestReverseLocationRejectsBadCoordinates(t *testing.T) {
	srv := &app{httpClient: &http.Client{}}
	for _, q := range []string{"", "lat=abc&lon=1", "lat=91&lon=0", "lat=0&lon=181", "lat=NaN&lon=0"} {
		rec := httptest.NewRecorder()
		srv.reverseLocation(rec, httptest.NewRequest("GET", "/api/locations/reverse?"+q, nil))
		if rec.Code != 400 {
			t.Fatalf("query %q: status %d, want 400", q, rec.Code)
		}
	}
}

func TestChatCallsAnthropicWithServerKeyAndFarmContext(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var sent map[string]any
	srv := &app{
		chatLimiter: newRateLimiter(5),
		httpClient: &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") == "" {
				t.Error("missing Anthropic auth headers")
			}
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &sent)
			return jsonResponse(200, `{"content":[{"type":"text","text":"Plant beans first."}]}`), nil
		})},
	}
	body := `{"messages":[{"role":"user","content":"What should I plant?"}],"context":{"location":"Mukono, Uganda","plan":{"rotation":["Beans","Maize"]}}}`
	rec := httptest.NewRecorder()
	srv.chat(rec, httptest.NewRequest("POST", "/api/chat", strings.NewReader(body)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Plant beans first.") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if system, _ := sent["system"].(string); !strings.Contains(system, "Mukono, Uganda") || !strings.Contains(system, "<farm_context>") {
		t.Fatalf("farm context missing from system prompt: %v", sent["system"])
	}
	if strings.Contains(rec.Body.String(), "test-key") {
		t.Fatal("API key must never reach the browser")
	}
}

func TestChatWithoutAPIKeyExplainsSetupIsMissing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	srv := &app{chatLimiter: newRateLimiter(5), httpClient: &http.Client{}}
	rec := httptest.NewRecorder()
	srv.chat(rec, httptest.NewRequest("POST", "/api/chat", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`)))
	if rec.Code != 503 {
		t.Fatalf("status %d, want 503", rec.Code)
	}
}
