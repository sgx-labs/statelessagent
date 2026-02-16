#!/usr/bin/env node
"use strict";

// Postinstall script — downloads the prebuilt SAME binary from GitHub Releases.
// Uses only Node built-ins. ES5 syntax for Node 14 compat.

var https = require("https");
var fs = require("fs");
var os = require("os");
var path = require("path");

var VERSION = require("../package.json").version;
var BASE_URL =
  "https://github.com/sgx-labs/statelessagent/releases/download/v" + VERSION;

var MAX_REDIRECTS = 5;
var REQUEST_TIMEOUT = 30000; // 30 seconds

var PLATFORM_MAP = {
  "darwin-arm64": "darwin-arm64",
  "darwin-x64": "darwin-arm64", // Rosetta fallback
  "linux-x64": "linux-amd64",
  "linux-arm64": "linux-arm64",
  "win32-x64": "windows-amd64.exe",
};

function getBinarySuffix() {
  var key = os.platform() + "-" + os.arch();
  var suffix = PLATFORM_MAP[key];
  if (!suffix) {
    console.error(
      "[same] Unsupported platform: " +
        key +
        ". Supported: darwin-arm64, darwin-x64, linux-x64, linux-arm64, win32-x64"
    );
    return null;
  }
  if (key === "darwin-x64") {
    console.log("[same] Intel Mac detected — using ARM64 binary via Rosetta.");
  }
  return suffix;
}

function download(url, dest, cb, redirects) {
  if (redirects === undefined) redirects = 0;
  if (redirects > MAX_REDIRECTS) {
    cb(new Error("Too many redirects (max " + MAX_REDIRECTS + ")"));
    return;
  }

  if (url.indexOf("https") !== 0) {
    cb(new Error("Refusing non-HTTPS URL: " + url));
    return;
  }
  var req = https
    .get(url, { timeout: REQUEST_TIMEOUT }, function (res) {
      // Follow HTTPS redirects only (GitHub → CDN)
      if (
        (res.statusCode >= 301 && res.statusCode <= 303 ||
         res.statusCode === 307 || res.statusCode === 308) &&
        res.headers.location
      ) {
        return download(res.headers.location, dest, cb, redirects + 1);
      }
      if (res.statusCode !== 200) {
        var msg = "HTTP " + res.statusCode;
        if (res.statusCode === 403 || res.statusCode === 429) {
          msg += " (rate limited — try again in a few minutes)";
        }
        cb(new Error(msg));
        return;
      }
      var tmpDest = dest + ".tmp";
      var file = fs.createWriteStream(tmpDest);
      res.pipe(file);
      file.on("finish", function () {
        file.close(function () {
          // Atomic rename — prevents partial binaries
          try {
            fs.renameSync(tmpDest, dest);
          } catch (err) {
            cb(err);
            return;
          }
          cb(null);
        });
      });
      file.on("error", function (err) {
        fs.unlink(tmpDest, function () {});
        cb(err);
      });
    })
    .on("timeout", function () {
      req.destroy();
      cb(new Error("Request timed out after " + (REQUEST_TIMEOUT / 1000) + "s"));
    })
    .on("error", function (err) {
      cb(err);
    });
}

function main() {
  var binDir = path.join(__dirname, "..", "bin");
  var isWindows = os.platform() === "win32";
  var binaryName = isWindows ? "same-binary.exe" : "same-binary";
  var dest = path.join(binDir, binaryName);

  // Idempotent — skip if binary already exists
  if (fs.existsSync(dest)) {
    console.log("[same] Binary already exists, skipping download.");
    return;
  }

  var suffix = getBinarySuffix();
  if (!suffix) {
    // Unsupported platform — exit 0 so npm install doesn't fail
    process.exit(0);
  }

  var url = BASE_URL + "/same-" + suffix;

  // Ensure bin/ exists
  try {
    fs.mkdirSync(binDir, { recursive: true });
  } catch (e) {
    if (e.code !== "EEXIST") {
      try {
        fs.mkdirSync(binDir);
      } catch (e2) {
        if (e2.code !== "EEXIST") throw e2;
      }
    }
  }

  console.log("[same] Downloading SAME v" + VERSION + " for " + os.platform() + "/" + os.arch() + "...");

  download(url, dest, function (err) {
    if (err) {
      console.error("[same] Download failed: " + err.message);
      console.error("[same] The binary will be downloaded on first run.");
      // Clean up partial temp file
      try {
        fs.unlinkSync(dest + ".tmp");
      } catch (e) {}
      // Exit 0 so npm install succeeds — shim will retry on first run
      process.exit(0);
    }

    // chmod 755 on Unix
    if (!isWindows) {
      try {
        fs.chmodSync(dest, 0o755);
      } catch (e) {}
    }

    console.log("[same] Installed successfully.");
  });
}

main();
