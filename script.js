const form = document.getElementById("planner-form");
const continentSelect = document.getElementById("continent");
const countrySelect = document.getElementById("country");
const regionInput = document.getElementById("region");
const districtInput = document.getElementById("district");
const localityInput = document.getElementById("locality");
const placeButton = document.getElementById("find-place");
const placeMessage = document.getElementById("place-message");
const resultsList = document.getElementById("place-results");
const selectedPlaceLabel = document.getElementById("selected-place");
const submitButton = document.getElementById("submit-button");
const formMessage = document.getElementById("form-message");

let selectedPlace = null;
let countryLoadId = 0;
let nasaMap = null;
let placeMarker = null;
let imageryLayer = null;
let formRevision = 0;

form.addEventListener("input", () => { formRevision++; clearResult(); });
form.addEventListener("change", () => { formRevision++; clearResult(); });

continentSelect.addEventListener("change", async () => {
  const loadId = ++countryLoadId;
  resetPlaceSelection();
  countrySelect.replaceChildren(option("", "Loading countries…"));
  countrySelect.disabled = true;
  disablePlaceFields();
  if (!continentSelect.value) {
    countrySelect.replaceChildren(option("", "Choose a continent first"));
    return;
  }
  try {
    const response = await fetch(`/api/locations/countries?continent=${encodeURIComponent(continentSelect.value)}`);
    const countries = await response.json();
    if (!response.ok) throw new Error(countries.error || "Could not load country choices.");
    if (loadId !== countryLoadId) return;
    countrySelect.replaceChildren(option("", "Choose your country"), ...countries.map((country) => option(country.code, country.name)));
    countrySelect.disabled = false;
  } catch (error) {
    if (loadId !== countryLoadId) return;
    countrySelect.replaceChildren(option("", "Country list unavailable"));
    placeMessage.textContent = errorMessage(error, "Country choices could not be loaded.");
  }
});

countrySelect.addEventListener("change", () => {
  resetPlaceSelection();
  const enabled = Boolean(countrySelect.value);
  regionInput.disabled = !enabled;
  districtInput.disabled = !enabled;
  localityInput.disabled = !enabled;
  placeButton.disabled = !enabled;
  regionInput.value = "";
  districtInput.value = "";
  localityInput.value = "";
  placeMessage.textContent = enabled
    ? "Enter the local administrative area and your town or village, then choose the matching result."
    : "Choose a continent and country to continue.";
});

for (const input of [regionInput, districtInput, localityInput]) {
  input.addEventListener("input", resetPlaceSelection);
}

document.querySelectorAll('input[name="priority"]').forEach((input) => {
  input.addEventListener("change", () => {
    const selected = document.querySelectorAll('input[name="priority"]:checked');
    if (selected.length > 3) {
      input.checked = false;
      formMessage.textContent = "Choose up to three priorities.";
    } else {
      formMessage.textContent = "";
    }
    clearResult();
  });
});

placeButton.addEventListener("click", async () => {
  const locality = localityInput.value.trim();
  if (!countrySelect.value || !regionInput.value.trim() || !locality) {
    placeMessage.textContent = "Choose a country and enter your region and town, village, or nearby place.";
    return;
  }
  resetPlaceSelection();
  const search = {
    countryCode: countrySelect.value,
    region: regionInput.value.trim(),
    district: districtInput.value.trim(),
    locality
  };
  placeButton.disabled = true;
  placeButton.textContent = "Searching places…";
  placeMessage.textContent = "Looking for matching locations.";
  try {
    const params = new URLSearchParams({
      country_code: search.countryCode,
      name: search.locality,
      region: search.region,
      district: search.district,
      language: navigator.language || "en"
    });
    const response = await fetch(`/api/locations/search?${params}`);
    const places = await response.json();
    if (!response.ok) throw new Error(places.error || "Place search failed.");
    if (search.countryCode !== countrySelect.value || search.region !== regionInput.value.trim() ||
      search.district !== districtInput.value.trim() || search.locality !== localityInput.value.trim()) return;
    if (places.length === 0) {
      placeMessage.textContent = "No matching place found. Check the spelling or use a nearby town name.";
      return;
    }
    resultsList.replaceChildren(...places.map(makePlaceOption));
    placeMessage.textContent = "Choose the result that matches your area. Check the region and district before continuing.";
  } catch (error) {
    placeMessage.textContent = errorMessage(error, "Place search is unavailable.");
  } finally {
    placeButton.disabled = false;
    placeButton.textContent = "Find my place";
  }
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  formMessage.textContent = "";
  if (!selectedPlace) {
    formMessage.textContent = "Search for your place and choose the matching result first.";
    return;
  }

  const priorities = [...document.querySelectorAll('input[name="priority"]:checked')].map((input) => input.value);
  if (priorities.length === 0 || priorities.length > 3) {
    formMessage.textContent = "Choose one to three priorities.";
    return;
  }

  const soilPH = document.getElementById("ph").value;
  const previousCrop = document.getElementById("last-crop").value;
  const countryOption = countrySelect.selectedOptions[0];
  const payload = {
    location: selectedPlace.path,
    continent: continentSelect.value,
    country: countryOption.textContent,
    country_code: countrySelect.value,
    region: regionInput.value.trim(),
    district: districtInput.value.trim(),
    locality: localityInput.value.trim(),
    latitude: selectedPlace.latitude,
    longitude: selectedPlace.longitude,
    soil_type: document.getElementById("soil").value,
    soil_ph: soilPH === "" ? null : Number(soilPH),
    current_rotation: previousCrop ? [previousCrop] : [],
    priorities
  };
  const submittedRevision = formRevision;

  submitButton.disabled = true;
  submitButton.textContent = "Checking local climate data…";
  try {
    const response = await fetch("/api/plans", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "Could not make a crop plan.");
    if (submittedRevision !== formRevision) {
      throw new Error("Farm details changed while climate data was loading. Submit again to update your plan.");
    }
    showResult(result);
    formMessage.textContent = "Your plan is ready and saved.";
  } catch (error) {
    formMessage.textContent = errorMessage(error, "Something went wrong. Please try again.");
  } finally {
    submitButton.disabled = false;
    submitButton.innerHTML = 'Get my crop plan <span aria-hidden="true">→</span>';
  }
});

function makePlaceOption(place) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "place-option";
  const name = document.createElement("strong");
  name.textContent = place.name;
  const path = [place.admin4, place.admin3, place.admin2, place.admin1, place.country].filter(Boolean);
  const hierarchy = document.createElement("span");
  hierarchy.textContent = path.join(" · ") || place.country;
  button.append(name, hierarchy);
  button.addEventListener("click", () => {
    selectedPlace = {
      ...place,
      path: [place.name, ...path].filter(Boolean).join(", ")
    };
    if (place.admin1) regionInput.value = place.admin1;
    if (place.admin2) districtInput.value = place.admin2;
    localityInput.value = place.name;
    selectedPlaceLabel.textContent = `Selected location: ${selectedPlace.path} (${place.latitude.toFixed(3)}, ${place.longitude.toFixed(3)})`;
    selectedPlaceLabel.hidden = false;
    resultsList.replaceChildren();
    placeMessage.textContent = "Location selected. Review the hierarchy, then request your plan.";
    submitButton.disabled = false;
    clearResult();
  });
  return button;
}

function resetPlaceSelection() {
  selectedPlace = null;
  selectedPlaceLabel.hidden = true;
  resultsList.replaceChildren();
  submitButton.disabled = true;
  clearResult();
}

function disablePlaceFields() {
  regionInput.disabled = true;
  districtInput.disabled = true;
  localityInput.disabled = true;
  placeButton.disabled = true;
}

function option(value, label) {
  const element = document.createElement("option");
  element.value = value;
  element.textContent = label;
  return element;
}

function showResult(result) {
  document.getElementById("empty-state").hidden = true;
  document.getElementById("result-card").hidden = false;
  document.getElementById("result-title").textContent = result.rotation.join(" → ");
  document.getElementById("result-headline").textContent = result.headline;
  document.getElementById("confidence").textContent = result.confidence;
  updateNASAmap(selectedPlace);

  document.getElementById("rotation-list").replaceChildren(...result.rotation.map((cropName) => {
    const item = document.createElement("li");
    item.textContent = cropName;
    return item;
  }));

  const climate = result.climate;
  document.getElementById("climate-summary").replaceChildren(
    metric(`Season A rain (${climate.season_a.label})`, `${Math.round(climate.season_a.rainfall_mm)} mm`),
    metric(`Season B rain (${climate.season_b.label})`, `${Math.round(climate.season_b.rainfall_mm)} mm`),
    metric(`Rainy window span A / B`, `${Math.round(climate.season_a.season_length_days)} / ${Math.round(climate.season_b.season_length_days)} days`),
    metric("Average temperature A / B", `${climate.season_a.mean_temp_c.toFixed(1)} / ${climate.season_b.mean_temp_c.toFixed(1)}°C`)
  );
  document.getElementById("expanded-climate").textContent =
    `Season A (${climate.season_a.label}): average rainfall minus evapotranspiration ${Math.round(climate.season_a.rainfall_mm - climate.season_a.evapotranspiration_mm)} mm; about ${climate.season_a.heat_days.toFixed(1)} days above 35°C; root-zone wetness index ${Math.round(climate.season_a.root_wetness * 100)}%. Season B (${climate.season_b.label}): average rainfall minus evapotranspiration ${Math.round(climate.season_b.rainfall_mm - climate.season_b.evapotranspiration_mm)} mm; about ${climate.season_b.heat_days.toFixed(1)} days above 35°C; root-zone wetness index ${Math.round(climate.season_b.root_wetness * 100)}%. These are broad climate indicators, not a field water balance or soil test.`;

  const reasons = [
    ...result.recommendations.map((item) => `${item.season}: ${item.reason}`),
    ...result.reasons
  ];
  fillList("reasons-list", reasons);
  fillList("risks-list", result.risks);
  document.getElementById("data-note").textContent =
    `${result.data_source}, ${result.data_period}. Rainfall trend per decade: ${climate.season_a.label} ${signed(climate.season_a.rainfall_trend_mm_per_decade)} mm; ${climate.season_b.label} ${signed(climate.season_b.rainfall_trend_mm_per_decade)} mm. This is historical data, not a forecast.`;
}

function updateNASAmap(place) {
  const mapNote = document.getElementById("map-note");
  if (!window.L) {
    mapNote.textContent = "The map library did not load. The recommendation and NASA climate data are still available.";
    return;
  }
  const center = [place.latitude, place.longitude];
  if (!nasaMap) {
    nasaMap = L.map("nasa-map", { scrollWheelZoom: false, maxZoom: 9 }).setView(center, 6);
  } else {
    nasaMap.setView(center, 6);
  }
  if (placeMarker) placeMarker.remove();
  placeMarker = L.marker(center).addTo(nasaMap).bindPopup(place.path);
  if (imageryLayer) imageryLayer.remove();
  const imageDate = new Date(Date.now() - 5 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  imageryLayer = L.tileLayer(
    `https://gibs.earthdata.nasa.gov/wmts/epsg3857/best/MODIS_Terra_CorrectedReflectance_TrueColor/default/${imageDate}/GoogleMapsCompatible_Level9/{z}/{y}/{x}.jpg`,
    {
      maxNativeZoom: 9,
      attribution: "Imagery: NASA EOSDIS GIBS"
    }
  ).addTo(nasaMap);
  imageryLayer.once("tileerror", () => {
    mapNote.textContent = "NASA imagery is temporarily unavailable. Try opening the location in NASA Worldview.";
  });
  mapNote.textContent = `NASA MODIS Terra imagery dated ${imageDate}. Visual context only; not a crop-health diagnosis.`;

  const bounds = [
    (place.longitude - 0.6).toFixed(4),
    (place.latitude - 0.4).toFixed(4),
    (place.longitude + 0.6).toFixed(4),
    (place.latitude + 0.4).toFixed(4)
  ].join(",");
  const worldview = new URL("https://worldview.earthdata.nasa.gov/");
  worldview.searchParams.set("v", bounds);
  worldview.searchParams.set("t", imageDate);
  document.getElementById("worldview-link").href = worldview.toString();
  window.setTimeout(() => nasaMap.invalidateSize(), 50);
}

function clearResult() {
  document.getElementById("result-card").hidden = true;
  document.getElementById("empty-state").hidden = false;
}

function metric(label, value) {
  const element = document.createElement("div");
  element.className = "metric";
  const title = document.createElement("span");
  title.textContent = label;
  const reading = document.createElement("strong");
  reading.textContent = value;
  element.append(title, reading);
  return element;
}

function fillList(id, entries) {
  document.getElementById(id).replaceChildren(...entries.map((text) => {
    const item = document.createElement("li");
    item.textContent = text;
    return item;
  }));
}

function signed(value) {
  return `${value >= 0 ? "+" : ""}${Math.round(value)}`;
}

function errorMessage(error, fallback) {
  return error instanceof Error ? error.message : fallback;
}

submitButton.disabled = true;
