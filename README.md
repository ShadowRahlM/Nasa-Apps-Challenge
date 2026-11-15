# AgriSmart

AgriSmart is a global, farmer-focused crop-rotation decision-support prototype. Farmers choose a location, share their priorities and optional soil details, and receive an explainable rotation suggestion based on historical climate indicators.

The project uses a Go HTTP server, PostgreSQL, NASA POWER climate data, NASA GIBS satellite imagery, and a lightweight HTML/CSS/JavaScript interface.

## What it does

- **One-tap GPS:** "Use my current location" detects the farm position from the phone and fills in country, region, district and town automatically. Manual entry (continent, country, region, locality) remains as a fallback.
- **Right now at your farm:** as soon as a location is set, the app compares the last 30 days of NASA POWER data with the same calendar window in the previous 10 years (rainfall, temperature, heat days, dry spell, modelled soil wetness) and shows plain-language findings rated ok / watch / alert. Results are cached per ~1 km cell for 6 hours.
- **Next 14 days:** a rain bar chart and findings from a weather forecast (heavy rain, heat, frost), plus an "outlook" that joins recent conditions with the forecast, for example "dry for 12 days and almost no rain forecast". Forecasts are cached per ~1 km cell for 1 hour.
- **Chat assistant:** a floating "Ask AgriSmart" chat explains the crop plan and answers farming questions in the farmer's language, using the farm's location and plan as context.
- Guides the farmer through continent, country, region/state/province, and locality when GPS is not used.
- Finds matching places and coordinates using Open-Meteo Geocoding.
- Fetches the previous 10 complete years of daily NASA POWER climate data for the selected coordinates.
- Identifies the two wettest non-overlapping three-month windows in the historical record.
- Scores candidate crops against rainfall, temperature, wet-season duration, heat, drought tolerance, crop-family rotation, soil contribution, and the farmer's selected priorities.
- Explains the recommendation, supporting climate figures, assumptions, and risks.
- Displays an interactive NASA GIBS map with MODIS Terra true-colour imagery and an optional 16-day NDVI vegetation-greenness overlay, plus a link to explore the area in NASA Worldview. The NDVI view is visual context only; it does not identify crops or affect crop rankings.
- Saves the farm details and recommendation in PostgreSQL.

## Run locally

Requirements: Go 1.22 or later, Docker Compose, and internet access to NASA POWER, NASA GIBS, Open-Meteo, and the World Bank API.

1. Start PostgreSQL:

   ```sh
   docker compose up -d db
   ```

2. Set the database connection string:

   ```sh
   export DATABASE_URL='postgres://agrismart:local_dev_password@localhost:5432/agrismart?sslmode=disable'
   ```

3. (Optional) Set a Groq API key to enable chat. Create a key at [GroqCloud](https://console.groq.com/keys). In PowerShell, use:

   ```powershell
   $env:GROQ_API_KEY = "your-groq-api-key"
   ```

   Keep the key private and do not commit it. The key is used only by the server.

4. Start the website and API:

   ```sh
   go run .
   ```

5. Open [http://localhost:8080](http://localhost:8080).

The database schema is created by the server at startup. The database can be stopped with `docker compose stop db`. To remove the local database and its saved plans, run `docker compose down -v`.

## Deploy Netlify frontend + Render API

Netlify serves the static frontend; Render runs the Go API and connects it to PostgreSQL. The browser calls the API directly over HTTPS rather than proxying plan requests through Netlify, whose external proxy rewrites have a 26-second timeout.

1. Push this repository to GitHub, GitLab, or another Netlify-supported Git provider.
2. Create the Netlify site from the repository using the repository root. Netlify reads `netlify.toml`; its build publishes only `dist/`, not the Go source. The first build intentionally fails until `API_BASE_URL` is set, but the site URL will still be available for the API's CORS allowlist.
3. In Render, create a managed PostgreSQL database and a Go Web Service from this repository using `render.yaml`. Keep the database and service in the same region, and set `DATABASE_URL` to the database's **internal** connection string. Set `ALLOWED_ORIGINS` to the Netlify site's HTTPS origin (for example, `https://your-site.netlify.app`), with no path or trailing slash. Set `GROQ_API_KEY` if chat should be enabled. Never commit these secrets.
4. In Netlify, set `API_BASE_URL` to the Render service's public HTTPS base URL (for example, `https://agrismart-api.onrender.com`, with no trailing slash), then trigger a new deploy.
5. Verify the Render service's `/api/health` responds with `{"status":"ok"}`. Open the Netlify URL and test country loading, place search, forecast, and a crop plan. A plan request also verifies NASA POWER access and saves a plan to PostgreSQL. If configured, test chat separately. Grant browser location permission to test GPS.

Check the browser's Network and Console panels if a feature fails. A CORS error usually means the Netlify origin in Render's `ALLOWED_ORIGINS` does not exactly match the site origin. Preview deploys use different origins; add only preview domains you trust. Render may have service sleep, usage limits, or charges depending on its current plan.

## Configuration

- `DATABASE_URL` is required.
- `ADDR` optionally sets the listen address. If unset, the server uses `PORT` when provided by a hosting platform, and otherwise defaults to `:8080`.
- `ALLOWED_ORIGINS` is a comma-separated list of exact HTTPS frontend origins allowed to call the API from a browser. Set it for cross-origin hosting; same-origin local development does not require it.
- `API_BASE_URL` is the Netlify build-time public HTTPS base URL for the Go API.
- `GROQ_API_KEY` enables the chat assistant. Create a key at [GroqCloud](https://console.groq.com/keys); it is read only by the server and never sent to the browser. Without it, chat explains that the assistant is not configured. Groq offers a developer/free tier subject to its current usage limits and terms.
- `METEOMATICS_USER` and `METEOMATICS_PASSWORD` optionally switch the forecast to the Meteomatics Weather API (for example via the NASA Space Apps offer, which lasts for the hackathon). If they are unset or Meteomatics fails, the app falls back to Open-Meteo automatically.
- `GROQ_MODEL` optionally selects an active model from the [Groq model list](https://console.groq.com/docs/models); the default is `qwen/qwen3.8-27b`.
- Browsers only allow GPS on **https** pages (or `localhost`). Testing on a phone over `http://<your-pc-ip>:8080` will not work; use an https tunnel or a deployed https URL.
- `.env.example` shows the local development connection string. Docker Compose credentials are for development only; replace them and use managed secrets and TLS before deployment.

## Data sources and variables

### NASA POWER

The API requests daily point data for the previous 10 complete calendar years:

| NASA POWER parameter | Use in AgriSmart |
| --- | --- |
| `PRECTOTCORR` | Daily corrected precipitation, summed to compare three-month rainfall windows and calculate average rainfall and historical trend. |
| `T2M` | Daily 2-metre air temperature, averaged within each window. |
| `T2M_MAX` | Daily maximum 2-metre air temperature, used to count days above 35°C. |
| `EVPTRNS` | Daily evapotranspiration/transpiration estimate, summed for a broad rainfall-minus-evapotranspiration indicator. |
| `GWETROOT` | Modelled root-zone wetness index, averaged as a climate indicator; it is not a field soil test. |

The two highest-rainfall non-overlapping three-month windows are selected separately for each location. These are historical climate windows, **not verified local planting seasons or forecasts**. NASA POWER is gridded data and may not represent conditions on an individual farm. If the service is unavailable, the application reports an error rather than displaying fabricated weather values.

### NASA mapping

The recommendation view uses NASA EOSDIS GIBS MODIS Terra corrected true-colour imagery to provide geographic context, with an optional MODIS Terra 16-day NDVI visualization. MODIS vegetation indices use ~250 m pixels and show broad vegetation greenness; mixed land cover, clouds, and observation timing limit interpretation. The layer cannot identify crop types, diagnose crop health on an individual farm, or affect the crop ranking. NASA Worldview opens the area for exploration of additional layers.

Potential future NASA map/data integrations include quantitative multi-year MODIS vegetation-index analysis, FIRMS fire detections for nearby fire awareness, and SRTM elevation for terrain and erosion context. These datasets are **not currently inputs to crop suitability scoring**.

### Other services

- **World Bank country API:** country names and broad regional groupings for the location selector.
- **Open-Meteo Geocoding:** place-name lookup, administrative hierarchy, and coordinates. Search uses the browser language where supported.
- **OpenStreetMap Nominatim:** reverse geocoding for the GPS button. Coordinates are rounded to about 1 km before leaving the server. Nominatim's usage policy limits heavy traffic; use a commercial reverse-geocoding provider for production.
- **Open-Meteo forecast:** default 14-day forecast, no key needed. Open-Meteo's free tier is for non-commercial use; get a commercial licence before charging farmers or deploying for a business.
- **Meteomatics (optional):** forecast provider used when credentials are set. Its 24-hour values are assigned to the UTC day they end on, so day boundaries can differ slightly from the farm's local day. This integration follows Meteomatics' documented URL format but has not been run against the live service in this repository's tests.
- **Groq API:** powers the chat assistant; the farm location and plan summary are sent with each question. Only send location and farm details you are comfortable sharing with Groq.
- **Leaflet:** interactive map display; basemap imagery is supplied by NASA GIBS.

## How the "right now" analysis works

`conditions.go` fetches daily NASA POWER values up to today and uses the newest day that has real data (POWER runs a few days behind). Findings come from fixed rules in `interpretConditions`, for example rainfall under 60% of normal is "watch", under 30% is "alert", and 14 or more days without 1 mm of rain is "alert". Dry-season locations (under 15 mm normal in the window) are not flagged for low rain. The language model only explains these findings in the chat; it does not generate the numbers. The thresholds are starting points and should be reviewed by an agronomist for your region and crops. This is past conditions, not a forecast.

## Limitations

- The interface is written in English. Users may use their browser/device translation feature; language coverage and agricultural terminology depend on that browser and are not guaranteed for every language.
- The prototype has six starter crops and generic crop characteristics. It is not a complete global crop catalogue, and values should be verified with regional agronomists and local crop/market data.
- Rainfall windows, crop scores, and risk warnings are advisory. Discuss planting dates and varieties with a local agricultural extension worker.
- The prototype does not yet integrate FAO GAEZ crop suitability, a field soil test, pest/disease surveillance, or local market prices.
- A saved plan is associated with its submitted location in this prototype; there is no user account or access-control system. Do not deploy with real farmer data until authentication, retention, privacy, and database security are addressed.

## Development checks

```sh
go test ./...
go vet ./...
go build ./...
node --check script.js
```
