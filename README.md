# AgriSmart

AgriSmart is a global, farmer-focused crop-rotation decision-support prototype. Farmers choose a location, share their priorities and optional soil details, and receive an explainable rotation suggestion built from local climate patterns.

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

AgriSmart helps farmers make more informed planting decisions by combining local farm details with climate analysis and agronomic guidance.

- Lets a farmer enter a location, soil information, recent crop history, and farming priorities
- Uses NASA POWER data to compare rainfall, temperature, heat stress, and seasonal patterns over the last decade
- Identifies the strongest crop-rotation windows for the farm and explains the reasoning in plain language
- Recommends crop choices based on risk, water use, soil health, and the farmer's goals such as income, food security, or healthier soil
- Shows a short-term forecast and recent conditions so farmers can understand whether the upcoming season looks wetter, drier, or hotter than usual
- Includes an AI assistant that answers farm-plan questions using the generated context for the location and recommendation
- Provides a NASA satellite map view for better situational awareness in the target area

AgriSmart is designed as a decision-support tool, not a guaranteed yield forecast. It helps farmers interpret climate and crop data, while encouraging local expert advice before planting.
