# AgriSmart

AgriSmart is a global, farmer-focused crop-rotation decision-support prototype. Farmers choose a location, share their priorities and optional soil details, and receive an explainable rotation suggestion grounded in local climate and forecast conditions.

The project uses a Go HTTP server, PostgreSQL, NASA POWER climate data, NASA GIBS satellite imagery, and a lightweight HTML/CSS/JavaScript interface.

## PR feature highlights

The current release adds the full farmer workflow to the prototype:

- One-tap GPS and guided location selection for farm setup
- Historical climate analysis across the last 10 years and a 14-day forecast outlook
- Explainable crop rotation recommendations based on rainfall, temperature, seasonal windows, and farmer priorities
- AI-powered farm-plan chat to answer questions in the farmer's language using plan context
- Interactive map views with NASA GIBS imagery and NDVI overlay support
- Saved farm plans and recommendation history in PostgreSQL

## What it does
