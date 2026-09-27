#!/usr/bin/env bash
# Sets up the throwaway Jellyfin in docker-compose.dev.yml for development:
# completes the startup wizard, creates the "dev" user (password "dev"),
# adds Movies and TV libraries, marks some items watched or favourite, and
# writes an API key for JellyTrim to dev/jellyfin-api-key.
#
# Safe to run more than once. Only ever point it at the dev container.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
JF=${JELLYFIN_URL:-http://localhost:8096}
USER_NAME=dev
USER_PASS=dev
KEY_FILE="$root/dev/jellyfin-api-key"
CLIENT='MediaBrowser Client="JellyTrim dev bootstrap", Device="dev", DeviceId="jellytrim-dev-bootstrap", Version="0.0.0"'

case "$JF" in
  http://localhost:*|http://127.0.0.1:*) ;;
  *) echo "bootstrap: refusing to run against $JF; this script is for the local dev container only." >&2; exit 1 ;;
esac
command -v jq >/dev/null || { echo "bootstrap: jq is required" >&2; exit 1; }

log() { echo "bootstrap: $*"; }

# api METHOD PATH [JSON] [TOKEN]
api() {
  local method=$1 path=$2 body=${3:-} token=${4:-}
  local auth=$CLIENT
  [ -n "$token" ] && auth="$CLIENT, Token=\"$token\""
  if [ -n "$body" ]; then
    curl -fsS -X "$method" "$JF$path" -H "Authorization: $auth" -H 'Content-Type: application/json' -d "$body"
  else
    curl -fsS -X "$method" "$JF$path" -H "Authorization: $auth"
  fi
}

log "waiting for Jellyfin at $JF"
for _ in $(seq 1 90); do
  if info=$(curl -fsS "$JF/System/Info/Public" 2>/dev/null); then break; fi
  sleep 2
done
[ -n "${info:-}" ] || { echo "bootstrap: Jellyfin did not start" >&2; exit 1; }
log "Jellyfin $(echo "$info" | jq -r .Version) is up"

if [ "$(echo "$info" | jq -r .StartupWizardCompleted)" != "true" ]; then
  log "completing the startup wizard"
  api POST /Startup/Configuration '{"ServerName":"jellytrim-dev","UICulture":"en-GB","MetadataCountryCode":"GB","PreferredMetadataLanguage":"en"}' >/dev/null
  api GET /Startup/User >/dev/null   # creates the first user; POST fails without it
  api POST /Startup/User "{\"Name\":\"$USER_NAME\",\"Password\":\"$USER_PASS\"}" >/dev/null
  api POST /Startup/RemoteAccess '{"EnableRemoteAccess":true}' >/dev/null
  api POST /Startup/Complete >/dev/null
fi

auth=$(api POST /Users/AuthenticateByName "{\"Username\":\"$USER_NAME\",\"Pw\":\"$USER_PASS\"}")
token=$(echo "$auth" | jq -r .AccessToken)
user_id=$(echo "$auth" | jq -r .User.Id)
[ -n "$token" ] && [ "$token" != null ] || { echo "bootstrap: sign-in failed" >&2; exit 1; }
log "signed in as $USER_NAME"

# API key for JellyTrim. POST returns 204 with no body, so read it back.
key=$(api GET /Auth/Keys "" "$token" | jq -r '[.Items[] | select(.AppName == "JellyTrim")][0].AccessToken // empty')
if [ -z "$key" ]; then
  api POST "/Auth/Keys?app=JellyTrim" "" "$token" >/dev/null
  key=$(api GET /Auth/Keys "" "$token" | jq -r '[.Items[] | select(.AppName == "JellyTrim")][0].AccessToken // empty')
fi
[ -n "$key" ] || { echo "bootstrap: could not create an API key" >&2; exit 1; }
umask 077
printf '%s\n' "$key" >"$KEY_FILE"
log "API key written to dev/jellyfin-api-key"

# Libraries, with internet metadata turned off so scans are fast and repeatable.
existing=$(api GET /Library/VirtualFolders "" "$token" | jq -r '.[].Name')
# Empty fetcher lists for every item type stop Jellyfin looking items up online,
# so names stay as the fixture folder names and nothing is downloaded.
type_options=$(jq -nc '[ "Movie","Series","Season","Episode","BoxSet" ] | map({Type: ., MetadataFetchers: [], MetadataFetcherOrder: [], ImageFetchers: [], ImageFetcherOrder: [], ImageOptions: []})')
library_options=$(jq -nc --argjson t "$type_options" '{LibraryOptions: {EnableRealtimeMonitor: true, EnableInternetProviders: false, SaveLocalMetadata: false, EnableChapterImageExtraction: false, ExtractChapterImagesDuringLibraryScan: false, EnableTrickplayImageExtraction: false, EnableEmbeddedTitles: false, AutomaticRefreshIntervalDays: 0, MetadataSavers: [], DisabledLocalMetadataReaders: [], TypeOptions: $t}}')
add_library() { # name type path
  if echo "$existing" | grep -qx "$1"; then return 0; fi
  log "adding library $1"
  api POST "/Library/VirtualFolders?name=$(jq -rn --arg v "$1" '$v|@uri')&collectionType=$2&paths=$(jq -rn --arg v "$3" '$v|@uri')&refreshLibrary=true" \
    "$library_options" \
    "$token" >/dev/null
}
add_library Movies movies /media/movies
add_library TV tvshows /media/tv

log "waiting for the library scan"
prev=-1
for _ in $(seq 1 60); do
  count=$(api GET "/Items?userId=$user_id&recursive=true&includeItemTypes=Movie,Episode&limit=0" "" "$token" | jq -r .TotalRecordCount)
  if [ "$count" = "$prev" ] && [ "$count" -gt 0 ]; then break; fi
  prev=$count
  sleep 3
done
log "$count movies and episodes found"

item_id() { # name
  api GET "/Items?userId=$user_id&recursive=true&includeItemTypes=Movie,Episode&searchTerm=$(jq -rn --arg v "$1" '$v|@uri')" "" "$token" \
    | jq -r '.Items[0].Id // empty'
}
mark() { # played|favourite name [date]
  local id; id=$(item_id "$2")
  [ -n "$id" ] || { log "item $2 not found, skipping"; return 0; }
  case "$1" in
    played) api POST "/UserPlayedItems/$id?userId=$user_id&datePlayed=${3:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}" "" "$token" >/dev/null ;;
    favourite) api POST "/UserFavoriteItems/$id?userId=$user_id" "" "$token" >/dev/null ;;
  esac
  log "marked $2 as $1"
}

# A spread of watch states for policy testing.
long_ago=$(date -u -v-200d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '200 days ago' +%Y-%m-%dT%H:%M:%SZ)
recent=$(date -u -v-5d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '5 days ago' +%Y-%m-%dT%H:%M:%SZ)
mark played Charlie "$long_ago"
mark played Delta "$long_ago"
mark played Alpha "$recent"
mark played Golf "$long_ago"
mark favourite Golf
mark favourite Bravo

log "done. Jellyfin: $JF (user $USER_NAME / $USER_PASS). JellyTrim: http://localhost:8097"
log "In JellyTrim setup, use http://jellyfin:8096 and the key in dev/jellyfin-api-key."
