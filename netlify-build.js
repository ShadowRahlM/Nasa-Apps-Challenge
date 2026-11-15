const fs = require("node:fs");
const path = require("node:path");

const apiBaseURL = process.env.API_BASE_URL;
if (!apiBaseURL) {
  throw new Error("API_BASE_URL is required to build the Netlify frontend");
}

const apiURL = new URL(apiBaseURL);
if (
  apiURL.protocol !== "https:" ||
  apiURL.pathname !== "/" ||
  apiURL.search ||
  apiURL.hash ||
  apiURL.username ||
  apiURL.password
) {
  throw new Error("API_BASE_URL must be an HTTPS origin without a path or credentials");
}

const outputDirectory = path.join(__dirname, "dist");
fs.mkdirSync(outputDirectory, { recursive: true });

for (const filename of ["index.html", "script.js", "styles.css", "api-config.js"]) {
  fs.copyFileSync(path.join(__dirname, filename), path.join(outputDirectory, filename));
}

fs.writeFileSync(
  path.join(outputDirectory, "api-config.js"),
  `window.AGRISMART_API_BASE = ${JSON.stringify(apiURL.origin)};\n`,
);
