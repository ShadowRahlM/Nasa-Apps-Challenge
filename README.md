# AgriSmart

**Climate-informed crop planning for farmers around the world.**

AgriSmart combines farm location, farmer priorities, recent crop history, NASA climate data, and practical crop-rotation rules to suggest a plan that farmers can understand and discuss with local agricultural experts.

It is a decision-support prototype—not a yield guarantee, a substitute for local agronomy advice, or a field-level remote-sensing diagnosis.

## What it offers

- **Global location selection:** choose a continent, country, region, and locality, or use GPS. Place matches show their administrative hierarchy before a farmer confirms a location.
- **Explainable rotation advice:** see a four-season crop rotation, why crops were selected, relevant climate indicators, and risks to review.
- **Historical climate context:** compare the two wettest non-overlapping three-month rainfall windows from a 10-year NASA POWER record.
- **Recent conditions:** compare recent rainfall and temperature with historical conditions for the same period.
- **Short-term outlook:** view daily rainfall and temperature estimates for the next 14 days.
- **NASA map layers:** explore NASA GIBS MODIS Terra true-colour imagery and an optional MODIS Terra NDVI layer on an interactive map; open the location in NASA Worldview.
- **Farm assistant:** ask questions about the generated plan and its climate context. The assistant is optional and requires a Groq API key.
- **PostgreSQL storage:** save submitted farm details and recommendation results.

## How it works

1. The farmer chooses a location and confirms a matching place or GPS result.
2. The Go API requests 10 years of daily NASA POWER climate data for the location.
3. The application summarizes rainfall, temperature, hot days, root-zone wetness, evapotranspiration, and rainfall trends. It finds two non-overlapping three-month windows with the highest historical rainfall totals.
4. A rule-based scoring engine compares those climate indicators with a starter crop catalogue and the farmer's chosen priorities. Rotation rules avoid consecutive crops from the same family and favor legumes after nitrogen-depleting crops where possible.
5. The interface presents the rotation, rationale, evidence, uncertainty, and risks for local review. The plan is saved in PostgreSQL.

The three-month climate windows are identified statistically; they are **not verified local planting calendars**. Check planting dates, crop varieties, and soil management with a local extension worker.

## Technology

- **Backend:** Go 1.22+, standard `net/http`, PostgreSQL via `pgx`
- **Frontend:** HTML, CSS, vanilla JavaScript, Leaflet
- **Database:** PostgreSQL
- **NASA data:** NASA POWER daily point API and NASA EOSDIS GIBS imagery
- **Location services:** World Bank country API, Open-Meteo Geocoding, OpenStreetMap Nominatim reverse geocoding for GPS
- **Forecast:** Open-Meteo by default; optional Meteomatics provider
- **Farm assistant:** optional Groq Chat Completions API
- **Deployment configuration:** Render for the Go API; Netlify for the static frontend

## Run locally

### Requirements

- Go 1.22 or later
- Docker Compose
- Internet access to the data providers listed above

### Start PostgreSQL and the application

From the project directory:

```sh
docker compose up -d db
export DATABASE_URL='postgres://agrismart:local_dev_password@localhost:5432/agrismart?sslmode=disable'
go run .
```

Open [http://localhost:8080](http://localhost:8080). The API health check is available at [http://localhost:8080/api/health](http://localhost:8080/api/health).

The application creates and updates its database schema on startup. Stop the app with `Ctrl+C`, then stop PostgreSQL with:

```sh
docker compose stop db
```

To delete the local database and all saved plans, run `docker compose down -v`.

## Configuration

The server reads configuration from environment variables; it does not automatically load a `.env` file.

| Variable | Required? | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | Yes | PostgreSQL connection string. |
| `ADDR` | No | Server listen address; defaults to `:8080`. |
| `PORT` | No | Port fallback used by hosted environments when `ADDR` is unset. |
| `ALLOWED_ORIGINS` | No | Comma-separated allowed browser origins for cross-origin frontend/API deployments. |
| `GROQ_API_KEY` | No | Enables the AI farm assistant. Never commit a real key. |
| `GROQ_MODEL` | No | Groq model name; defaults to the model configured in the app. |
| `METEOMATICS_USER` / `METEOMATICS_PASSWORD` | No | Optional forecast credentials. If missing or the provider fails, the app uses Open-Meteo. |
| `API_BASE_URL` | Netlify build | HTTPS origin of the deployed Go API; required by `node netlify-build.js`. |

See [.env.example](./.env.example) for development values and comments. The example database password is only for local development.

### Optional AI assistant

Create a Groq API key, then set it in the server environment:

```sh
export GROQ_API_KEY='your-key'
export GROQ_MODEL='qwen/qwen3.8-27b'
```

The key stays on the server and is not included in the frontend bundle. Without it, the core planner, climate views, and map continue to work, but chat is unavailable.

## API overview

| Method | Route | Description |
| --- | --- | --- |
| `GET` | `/api/health` | Check application/database health. |
| `GET` | `/api/locations/countries?continent=Africa` | List countries for a supported continent. |
| `GET` | `/api/locations/search` | Find matching places and administrative areas. |
| `GET` | `/api/locations/reverse?lat=...&lon=...` | Resolve a GPS location to a place hierarchy. |
| `POST` | `/api/plans` | Generate a climate-informed rotation and save it. |
| `GET` | `/api/conditions?lat=...&lon=...` | Compare recent NASA POWER conditions with historical values. |
| `GET` | `/api/forecast?lat=...&lon=...` | Return the 14-day forecast. |
| `POST` | `/api/chat` | Ask the optional farm assistant about the plan. |

## NASA data and mapping

### NASA POWER

The crop planner uses daily point data for the previous 10 complete calendar years:

| Parameter | How the application uses it |
| --- | --- |
| `PRECTOTCORR` | Corrected precipitation in mm/day; used for rainfall totals, wet-window selection, and historical rainfall trends. |
| `T2M` | Air temperature at 2 metres; averaged for each climate window. |
| `T2M_MAX` | Daily maximum air temperature at 2 metres; used to count days above 35°C. |
| `EVPTRNS` | Evapotranspiration/transpiration estimate; summarized as a broad rainfall-minus-evapotranspiration indicator. |
| `GWETROOT` | Modelled root-zone wetness index; used as climate context, not as a field soil test. |

Recent-condition analysis uses the latest available NASA POWER daily values and compares a recent 30-day window with the same period in up to 10 earlier years. NASA POWER data can be several days behind and represents a grid cell, not a sensor at the farm.

### Forecast

The outlook shows daily precipitation and minimum/maximum temperature for up to 14 days. Open-Meteo is the default provider. Meteomatics can be configured as an optional provider; Open-Meteo is used as a fallback. Forecasts are estimates and can change.

### NASA GIBS map

The interactive map offers MODIS Terra corrected true-colour imagery and an optional 16-day MODIS Terra NDVI layer. NDVI represents broad vegetation greenness; clouds, mixed land cover, timing, and pixel size limit what it can say about an individual farm. It does not identify crop types, delineate fields, or affect the crop ranking. NASA Worldview provides a way to explore additional imagery layers.

### Other location and map services

- World Bank API: country names and broad regional groupings.
- Open-Meteo Geocoding: typed place search, coordinates, and available administrative hierarchy.
- OpenStreetMap Nominatim: reverse geocoding for GPS. GPS coordinates are rounded to approximately 1 km before lookup and use.
- NASA GIBS: satellite image tiles shown by Leaflet.

The interface also names possible future integrations such as NASA FIRMS, FAO GAEZ, and SRTM. These are not inputs to the current recommendation unless explicitly described above.

## Deploy

The repository includes [render.yaml](./render.yaml) for the Go API and [netlify.toml](./netlify.toml) for the static frontend.

1. Deploy the Go service using the Render blueprint. Configure `DATABASE_URL` with a PostgreSQL connection string, `ALLOWED_ORIGINS` with the frontend origin, and optionally `GROQ_API_KEY` / `GROQ_MODEL`.
2. Provision PostgreSQL and use its managed connection string; do not use the development password in production.
3. Deploy the frontend on Netlify with `API_BASE_URL` set to the HTTPS origin of the Go service. The build script copies the static site to `dist` and writes the configured API origin into `api-config.js`.
4. Add the deployed Netlify origin to the backend's `ALLOWED_ORIGINS`.
5. Keep provider credentials in the hosting platform's secret/environment settings, not in Git or frontend configuration.

## Tests and checks

```sh
go test ./...
go vet ./...
go build ./...
node --check script.js
```

## Limitations and responsible use

- This prototype has six starter crops with generic crop characteristics. It is not a complete global crop catalogue, and values should be verified with regional agronomists and local crop, variety, and market data.
- Climate suitability and historical wet windows do not account for every local planting calendar, irrigation source, pest, disease, field condition, or market price.
- NASA POWER and satellite products are broad-area datasets, not farm-level measurements. The map is situational context, not an agronomic diagnosis.
- The interface is written in English. Browser/device translation is available at the user's discretion, but language coverage and agricultural terminology accuracy vary.
- The prototype has no user accounts or per-farmer access controls. Saved farm records contain location details. Do not use production farmer data until authentication, access control, data retention, privacy review, and backups are in place.
- AI assistant answers may be inaccurate. Review important decisions with a qualified local agricultural extension worker.
