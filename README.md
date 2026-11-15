# AgriSmart

**Climate-informed crop planning for smallholder farmers.** AgriSmart turns a farm’s location, priorities, and optional soil and crop-history details into an explainable crop-rotation plan.

It combines historical climate analysis from **NASA POWER** with short-term weather outlooks, **NASA GIBS** satellite imagery, and an AI farm assistant. Built as a decision-support prototype with a Go backend, PostgreSQL, and a lightweight HTML/CSS/JavaScript frontend.

## Features

- One-tap GPS or guided location selection
- Historical analysis of rainfall, temperature, heat, and seasonal patterns
- Explainable crop-rotation suggestions based on climate, farmer priorities, and recent crop history
- Recent conditions and a 14-day forecast outlook
- Interactive NASA satellite imagery with an optional vegetation-index layer
- AI farm assistant that can answer questions using the generated farm plan
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
