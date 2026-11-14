# AgriSmart

AgriSmart is a global, farmer-focused crop-rotation decision-support prototype. Farmers choose a location, share their priorities and optional soil details, and receive an explainable rotation suggestion based on historical climate indicators.

The project uses a Go HTTP server, PostgreSQL, NASA POWER climate data, NASA GIBS satellite imagery, and a lightweight HTML/CSS/JavaScript interface.

## What it does

- Guides the farmer through continent, country, region/state/province, and locality.
- Finds matching places and coordinates using Open-Meteo Geocoding.
- Fetches the previous 10 complete years of daily NASA POWER climate data for the selected coordinates.
- Identifies the two wettest non-overlapping three-month windows in the historical record.
- Scores candidate crops against rainfall, temperature, wet-season duration, heat, drought tolerance, crop-family rotation, soil contribution, and the farmer's selected priorities.
- Explains the recommendation, supporting climate figures, assumptions, and risks.
- Displays an interactive NASA GIBS MODIS Terra true-colour map centered on the selected location, with a link to explore the area in NASA Worldview.
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

3. Start the website and API:

   ```sh
   go run .
   ```

4. Open [http://localhost:8080](http://localhost:8080).

The database schema is created by the server at startup. The database can be stopped with `docker compose stop db`. To remove the local database and its saved plans, run `docker compose down -v`.

## Configuration

- `DATABASE_URL` is required.
- `ADDR` optionally sets the listen address; it defaults to `:8080`.
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

The recommendation view uses NASA EOSDIS GIBS MODIS Terra corrected true-colour imagery to provide geographic context. The map can be panned and zoomed, and NASA Worldview opens the area for exploration of other available layers. The image is not a crop-health diagnosis or a field boundary map.

Potential future NASA map/data integrations include FIRMS fire detections for fire awareness, MODIS vegetation indices for broad vegetation-change monitoring, and SRTM elevation for terrain and erosion context. These datasets are **not currently inputs to the recommendation**.

### Other services

- **World Bank country API:** country names and broad regional groupings for the location selector.
- **Open-Meteo Geocoding:** place-name lookup, administrative hierarchy, and coordinates. Search uses the browser language where supported.
- **Leaflet:** interactive map display; basemap imagery is supplied by NASA GIBS.

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
