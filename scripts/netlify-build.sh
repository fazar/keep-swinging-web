#!/usr/bin/env bash
# Generates web/static/config.js for Netlify so the UI can call a remote API.
# Set KEEP_SWINGING_API_BASE in Netlify (e.g. https://your-api.fly.dev) with no trailing slash.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
base="${KEEP_SWINGING_API_BASE:-}"
# Escape for embedding in single-quoted JS string: end quote, append escaped quote, reopen
js_base="${base//\\/\\\\}"
js_base="${js_base//\'/\\\'}"
printf "%s\n" "window.__KEEP_SWINGING_API_BASE__ = '${js_base}';" >"${root}/web/static/config.js"
