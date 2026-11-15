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
const API_BASE_URL = String(window.AGRISMART_API_BASE || "").replace(/\/+$/, "");

function apiURL(path) {
  return `${API_BASE_URL}${path}`;
}

let selectedPlace = null;
let countryLoadId = 0;
let nasaMap = null;
let placeMarker = null;
let imageryLayer = null;
let vegetationLayer = null;
let currentMapImageDate = "";
let formRevision = 0;
let gpsRequestId = 0;
let lastPlan = null;
let lastConditions = null;
let conditionsRequestId = 0;
let lastForecast = null;
let forecastRequestId = 0;

const gpsButton = document.getElementById("use-gps");
const gpsMessage = document.getElementById("gps-message");
const manualDetails = document.getElementById("manual-details");

if (window.location.protocol === "file:") {
  formMessage.textContent = "You opened this page directly. Start the AgriSmart web server and open http://localhost:8080 to use location search and crop planning.";
}

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
    const response = await fetch(apiURL(`/api/locations/countries?continent=${encodeURIComponent(continentSelect.value)}`));
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
    const response = await fetch(apiURL(`/api/locations/search?${params}`));
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

gpsButton.addEventListener("click", () => {
  if (!("geolocation" in navigator)) {
    gpsFail("This browser cannot share your location. Please enter it by hand.");
    return;
  }
  if (!window.isSecureContext) {
    gpsFail("Location sharing needs a secure (https) page. Please enter your location by hand.");
    return;
  }
  resetPlaceSelection();
  const requestId = gpsRequestId;
  setGpsBusy(true, "Finding your location…");
  gpsMessage.textContent = "Waiting for your phone. Tap Allow if it asks for permission.";
  navigator.geolocation.getCurrentPosition(
    async (position) => {
      if (requestId !== gpsRequestId) return;
      const { latitude, longitude, accuracy } = position.coords;
      setGpsBusy(true, "Finding your area…");
      try {
        const params = new URLSearchParams({
          lat: latitude.toFixed(5),
          lon: longitude.toFixed(5),
          language: navigator.language || "en"
        });
        const response = await fetch(apiURL(`/api/locations/reverse?${params}`));
        const place = await response.json();
        if (!response.ok) throw new Error(place.error || "Could not look up your area.");
        if (requestId !== gpsRequestId) return;
        selectedPlace = {
          source: "gps",
          name: place.locality,
          path: place.location,
          latitude: place.latitude,
          longitude: place.longitude,
          meta: place
        };
        selectedPlaceLabel.textContent = `Your location: ${place.location} (${place.latitude.toFixed(2)}, ${place.longitude.toFixed(2)})`;
        selectedPlaceLabel.hidden = false;
        const rounded = Math.round(accuracy);
        gpsMessage.textContent = accuracy > 5000
          ? `Location found, but only accurate to about ${rounded} m. Try again outside or with GPS switched on for a better result.`
          : `Location found (accurate to about ${rounded} m). You can request your plan now.`;
        manualDetails.open = false;
        submitButton.disabled = false;
        clearResult();
        loadConditions(selectedPlace);
      } catch (error) {
        if (requestId !== gpsRequestId) return;
        gpsFail(errorMessage(error, "Could not look up your area. Please enter it by hand."));
      } finally {
        if (requestId === gpsRequestId) setGpsBusy(false);
      }
    },
    (error) => {
      if (requestId !== gpsRequestId) return;
      setGpsBusy(false);
      if (error.code === error.PERMISSION_DENIED) {
        gpsFail("Location permission was blocked. Allow location for this site in your browser or phone settings and try again, or enter it by hand.");
      } else if (error.code === error.TIMEOUT) {
        gpsFail("Your phone took too long to find you. Move to an open area, switch GPS on, and try again, or enter it by hand.");
      } else {
        gpsFail("Your location is not available right now. Check that GPS is on, or enter it by hand.");
      }
    },
    { enableHighAccuracy: true, timeout: 20000, maximumAge: 60000 }
  );
});

function setGpsBusy(busy, label) {
  gpsButton.disabled = busy;
  gpsButton.replaceChildren();
  const icon = document.createElement("span");
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = "📍";
  gpsButton.append(icon, ` ${busy ? label : "Use my current location"}`);
}

function gpsFail(message) {
  setGpsBusy(false);
  gpsMessage.textContent = message;
  manualDetails.open = true;
}

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
  const farm = selectedPlace.source === "gps"
    ? {
        location: selectedPlace.meta.location,
        continent: selectedPlace.meta.continent,
        country: selectedPlace.meta.country,
        country_code: selectedPlace.meta.country_code,
        region: selectedPlace.meta.region,
        district: selectedPlace.meta.district,
        locality: selectedPlace.meta.locality
      }
    : {
        location: selectedPlace.path,
        continent: continentSelect.value,
        country: countrySelect.selectedOptions[0].textContent,
        country_code: countrySelect.value,
        region: regionInput.value.trim(),
        district: districtInput.value.trim(),
        locality: localityInput.value.trim()
      };
  const payload = {
    ...farm,
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
    const response = await fetch(apiURL("/api/plans"), {
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
    loadConditions(selectedPlace);
  });
  return button;
}

function resetPlaceSelection() {
  gpsRequestId++;
  hideConditions();
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
  lastPlan = result;
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
    nasaMap.on("overlayadd overlayremove", (event) => {
      if (event.layer === vegetationLayer) {
        mapNote.textContent = nasaMap.hasLayer(vegetationLayer)
          ? "MODIS Terra NDVI shows broad vegetation greenness in the latest available 16-day composite. It cannot identify crop types or assess an individual field."
          : `NASA MODIS Terra imagery dated ${currentMapImageDate}. Visual context only; not a crop-health diagnosis.`;
      }
    });
  } else {
    nasaMap.setView(center, 6);
  }
  if (placeMarker) placeMarker.remove();
  placeMarker = L.marker(center).addTo(nasaMap).bindPopup(place.path);
  const imageDate = new Date(Date.now() - 5 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);
  currentMapImageDate = imageDate;
  const imageryURL = `https://gibs.earthdata.nasa.gov/wmts/epsg3857/best/MODIS_Terra_CorrectedReflectance_TrueColor/default/${imageDate}/GoogleMapsCompatible_Level9/{z}/{y}/{x}.jpg`;
  const vegetationURL = "https://gibs.earthdata.nasa.gov/wmts/epsg3857/best/MODIS_Terra_L3_NDVI_16Day/default/GoogleMapsCompatible_Level9/{z}/{y}/{x}.png";
  if (!imageryLayer) {
    imageryLayer = L.tileLayer(imageryURL, {
      maxNativeZoom: 9,
      attribution: "Imagery: NASA EOSDIS GIBS"
    }).addTo(nasaMap);
    vegetationLayer = L.tileLayer(vegetationURL, {
      maxNativeZoom: 9,
      opacity: 0.72,
      attribution: "Vegetation index: NASA EOSDIS GIBS / MODIS Terra"
    });
    L.control.layers(
      { "True-colour satellite": imageryLayer },
      { "16-day vegetation index (NDVI)": vegetationLayer },
      { collapsed: false }
    ).addTo(nasaMap);
    vegetationLayer.once("tileerror", () => {
      if (nasaMap.hasLayer(vegetationLayer)) {
        mapNote.textContent = "NASA MODIS vegetation imagery is temporarily unavailable.";
      }
    });
    imageryLayer.once("tileerror", () => {
      mapNote.textContent = "NASA imagery is temporarily unavailable. Try opening the location in NASA Worldview.";
    });
  } else {
    imageryLayer.setUrl(imageryURL);
  }
  mapNote.textContent = nasaMap.hasLayer(vegetationLayer)
    ? "MODIS Terra NDVI shows broad vegetation greenness in the latest available 16-day composite. It cannot identify crop types or assess an individual field."
    : `NASA MODIS Terra imagery dated ${imageDate}. Visual context only; not a crop-health diagnosis.`;

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
  lastPlan = null;
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

/* ---------- Chat assistant ---------- */
const chatToggle = document.getElementById("chat-toggle");
const chatPanel = document.getElementById("chat-panel");
const chatClose = document.getElementById("chat-close");
const chatLog = document.getElementById("chat-log");
const chatForm = document.getElementById("chat-form");
const chatInput = document.getElementById("chat-input");
const chatSend = document.getElementById("chat-send");
const chatSuggestions = document.getElementById("chat-suggestions");
const chatHistory = [];
const suggestionPrompts = [
  "What should I do about current conditions?",
  "Why this rotation?",
  "Should I plant this week?",
  "What if the rains come late?"
];

function openChat() {
  chatPanel.hidden = false;
  chatToggle.hidden = true;
  chatToggle.setAttribute("aria-expanded", "true");
  if (chatLog.children.length === 0) {
    addBubble("bot", "Hello! I can explain your crop plan or answer farming questions. What would you like to know?");
    renderSuggestions();
  }
  chatInput.focus();
}

function closeChat() {
  chatPanel.hidden = true;
  chatToggle.hidden = false;
  chatToggle.setAttribute("aria-expanded", "false");
  chatToggle.focus();
}

chatToggle.addEventListener("click", openChat);
chatClose.addEventListener("click", closeChat);
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !chatPanel.hidden) closeChat();
});

function renderSuggestions() {
  chatSuggestions.replaceChildren(...suggestionPrompts.map((text) => {
    const chip = document.createElement("button");
    chip.type = "button";
    chip.className = "chat-chip";
    chip.textContent = text;
    chip.addEventListener("click", () => sendChat(text));
    return chip;
  }));
}

function addBubble(kind, text) {
  const bubble = document.createElement("div");
  bubble.className = `chat-bubble ${kind}`;
  if (kind === "bot") {
    renderAssistantMessage(bubble, text);
  } else {
    bubble.textContent = text;
  }
  chatLog.append(bubble);
  chatLog.scrollTop = chatLog.scrollHeight;
  return bubble;
}

function renderAssistantMessage(container, text) {
  let paragraph = null;
  let list = null;

  const flushParagraph = () => {
    if (paragraph) container.append(paragraph);
    paragraph = null;
  };
  const flushList = () => {
    if (list) container.append(list);
    list = null;
  };

  for (const line of text.split(/\r?\n/)) {
    const listMatch = line.match(/^\s*(?:([-*])|(\d+)[.)])\s+(.+)$/);
    const headingMatch = line.match(/^#{1,3}\s+(.+)$/);
    if (!line.trim()) {
      flushParagraph();
      flushList();
      continue;
    }
    if (listMatch) {
      flushParagraph();
      const type = listMatch[1] ? "ul" : "ol";
      if (!list || list.tagName.toLowerCase() !== type) {
        flushList();
        list = document.createElement(type);
      }
      const item = document.createElement("li");
      appendFormattedText(item, listMatch[3]);
      list.append(item);
      continue;
    }
    flushList();
    if (headingMatch) {
      flushParagraph();
      const heading = document.createElement("strong");
      appendFormattedText(heading, headingMatch[1]);
      container.append(heading);
      continue;
    }
    if (!paragraph) paragraph = document.createElement("p");
    else paragraph.append(document.createElement("br"));
    appendFormattedText(paragraph, line);
  }
  flushParagraph();
  flushList();
}

function appendFormattedText(container, text) {
  const formatting = /(\*\*[^*\n]+?\*\*|__[^_\n]+?__|`[^`\n]+`|\*[^*\n]+?\*)/g;
  let position = 0;
  for (const match of text.matchAll(formatting)) {
    container.append(document.createTextNode(text.slice(position, match.index)));
    const token = match[0];
    const isBold = token.startsWith("**") || token.startsWith("__");
    const element = document.createElement(isBold ? "strong" : token.startsWith("`") ? "code" : "em");
    element.textContent = isBold ? token.slice(2, -2) : token.slice(1, -1);
    container.append(element);
    position = match.index + token.length;
  }
  container.append(document.createTextNode(text.slice(position)));
}

function chatContext() {
  if (!lastPlan && !selectedPlace) return null;
  const context = {};
  if (selectedPlace) {
    context.location = selectedPlace.path;
    context.latitude = Number(selectedPlace.latitude.toFixed(2));
    context.longitude = Number(selectedPlace.longitude.toFixed(2));
  }
  if (lastConditions) context.conditions = lastConditions;
  if (lastForecast) {
    context.forecast = {
      provider: lastForecast.provider,
      rain_next_7_days_mm: lastForecast.rain_next_7_days_mm,
      rain_next_14_days_mm: lastForecast.rain_next_14_days_mm,
      first_rain_date: lastForecast.first_rain_date,
      findings: lastForecast.findings,
      outlook: lastForecast.outlook,
      days: lastForecast.days.map((d) => [d.date, d.rain_mm, d.tmax_c, d.tmin_c])
    };
  }
  if (lastPlan) {
    context.plan = {
      rotation: lastPlan.rotation,
      confidence: lastPlan.confidence,
      headline: lastPlan.headline,
      recommendations: lastPlan.recommendations,
      reasons: lastPlan.reasons,
      risks: lastPlan.risks,
      climate: lastPlan.climate,
      data_source: lastPlan.data_source,
      data_period: lastPlan.data_period
    };
  }
  return context;
}

chatForm.addEventListener("submit", (event) => {
  event.preventDefault();
  sendChat(chatInput.value);
});

async function sendChat(rawText) {
  const text = rawText.trim();
  if (!text || chatSend.disabled) return;
  chatInput.value = "";
  chatSuggestions.replaceChildren();
  addBubble("user", text);
  chatHistory.push({ role: "user", content: text });
  chatSend.disabled = true;
  const typing = addBubble("bot typing", "Thinking…");
  try {
    const response = await fetch(apiURL("/api/chat"), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        messages: chatHistory.slice(-12),
        context: chatContext(),
        language: navigator.language || "en"
      })
    });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || "The assistant could not answer.");
    typing.remove();
    addBubble("bot", data.reply);
    chatHistory.push({ role: "assistant", content: data.reply });
  } catch (error) {
    typing.remove();
    chatHistory.pop();
    addBubble("error", errorMessage(error, "The assistant could not answer. Please try again."));
  } finally {
    chatSend.disabled = false;
    chatInput.focus();
  }
}

/* ---------- Recent NASA conditions ---------- */
const conditionsCard = document.getElementById("conditions-card");
const conditionsStatus = document.getElementById("conditions-status");
const forecastCard = document.getElementById("forecast-card");

async function loadConditions(place) {
  const requestId = ++conditionsRequestId;
  lastConditions = null;
  conditionsCard.hidden = true;
  conditionsStatus.hidden = false;
  conditionsStatus.textContent = "Checking the latest NASA data for your location…";
  try {
    const params = new URLSearchParams({ lat: place.latitude.toFixed(2), lon: place.longitude.toFixed(2) });
    const response = await fetch(apiURL(`/api/conditions?${params}`));
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || "Recent conditions are unavailable.");
    if (requestId !== conditionsRequestId) return;
    lastConditions = data;
    renderConditions(data);
    conditionsStatus.hidden = true;
  } catch (error) {
    if (requestId !== conditionsRequestId) return;
    conditionsStatus.textContent = errorMessage(error, "Recent conditions are unavailable right now.");
  }
  // The forecast is fetched afterwards so the server can reuse the cached NASA
  // data to combine "what happened" with "what is coming".
  if (requestId === conditionsRequestId) loadForecast(place);
}

async function loadForecast(place) {
  const requestId = ++forecastRequestId;
  lastForecast = null;
  forecastCard.hidden = true;
  try {
    const params = new URLSearchParams({ lat: place.latitude.toFixed(2), lon: place.longitude.toFixed(2) });
    const response = await fetch(apiURL(`/api/forecast?${params}`));
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || "The forecast is unavailable.");
    if (requestId !== forecastRequestId) return;
    lastForecast = data;
    renderForecast(data);
  } catch (error) {
    if (requestId !== forecastRequestId) return;
    forecastStatusMessage(errorMessage(error, "The forecast is unavailable right now."));
  }
}

function forecastStatusMessage(message) {
  conditionsStatus.hidden = false;
  conditionsStatus.textContent = lastConditions ? `Forecast: ${message}` : message;
}

function renderForecast(data) {
  const maxRain = Math.max(10, ...data.days.map((day) => day.rain_mm));
  const chart = document.getElementById("forecast-chart");
  const labels = document.createElement("div");
  labels.className = "forecast-labels";
  chart.replaceChildren(...data.days.map((day) => {
    const column = document.createElement("div");
    column.className = "forecast-day";
    column.title = `${day.date}: ${day.rain_mm.toFixed(1)} mm rain, ${Math.round(day.tmin_c)}–${Math.round(day.tmax_c)}°C`;
    const amount = document.createElement("span");
    amount.className = "forecast-amount";
    amount.textContent = day.rain_mm >= 1 ? Math.round(day.rain_mm) : "";
    const bar = document.createElement("div");
    bar.className = `forecast-bar${day.rain_mm < 0.5 ? " none" : day.rain_mm >= 25 ? " heavy" : ""}`;
    bar.style.height = `${Math.max(2, (day.rain_mm / maxRain) * 100)}%`;
    column.append(amount, bar);
    return column;
  }));
  chart.setAttribute("aria-label",
    `Forecast rain for the next ${data.days.length} days: ${Math.round(data.rain_next_7_days_mm)} mm in the next 7 days and ${Math.round(data.rain_next_14_days_mm)} mm in total.`);
  // Day-of-month labels live in a second row so they line up with the bars.
  labels.replaceChildren(...data.days.map((day) => {
    const label = document.createElement("span");
    label.textContent = String(Number(day.date.slice(8, 10)));
    return label;
  }));
  const oldLabels = chart.parentElement.querySelector(".forecast-labels");
  if (oldLabels) oldLabels.remove();
  chart.after(labels);

  const findings = [...(data.outlook || []), ...data.findings];
  document.getElementById("forecast-findings").replaceChildren(...findings.map(findingItem));
  document.getElementById("forecast-note").textContent = `Forecast from ${data.provider}. ${data.note}`;
  forecastCard.hidden = false;
}

function findingItem(finding) {
  const item = document.createElement("li");
  item.className = `finding level-${finding.level}`;
  const title = document.createElement("strong");
  title.textContent = finding.topic;
  if (finding.level !== "ok") {
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = finding.level === "alert" ? "ACT NOW" : "WATCH";
    title.append(badge);
  }
  const text = document.createElement("span");
  text.textContent = finding.message;
  item.append(title, text);
  return item;
}

function hideConditions() {
  forecastRequestId++;
  lastForecast = null;
  forecastCard.hidden = true;
  conditionsRequestId++;
  lastConditions = null;
  conditionsCard.hidden = true;
  conditionsStatus.hidden = true;
}

function renderConditions(data) {
  document.getElementById("conditions-window").textContent =
    `Last ${data.window_days} days of NASA data (${data.window_label}) compared with the same time in the previous ${data.baseline_years} years.`;
  const recent = data.recent;
  const normal = data.normal;
  document.getElementById("conditions-metrics").replaceChildren(
    metric("Rain (usual)", `${Math.round(recent.rainfall_mm)} mm (${Math.round(normal.rainfall_mm)} mm)`),
    metric("Average temperature (usual)", `${recent.mean_temp_c.toFixed(1)}°C (${normal.mean_temp_c.toFixed(1)}°C)`),
    metric("Rain in last 7 days", `${Math.round(data.rain_last_7_days_mm)} mm`),
    metric("Days since 1 mm+ rain", `${data.days_since_rain}${data.days_since_rain >= data.window_days ? "+" : ""}`)
  );
  document.getElementById("conditions-findings").replaceChildren(...data.findings.map(findingItem));
  document.getElementById("conditions-note").textContent = `${data.source}, up to ${data.data_through}. ${data.note}`;
  conditionsCard.hidden = false;
}

document.getElementById("conditions-ask").addEventListener("click", () => {
  openChat();
  sendChat("Based on recent conditions and the forecast for my farm, what should I do in the next two weeks?");
});
