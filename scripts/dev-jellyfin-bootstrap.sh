#!/usr/bin/env bash
# Sets up the throwaway Jellyfin in docker-compose.dev.yml for development:
# completes the startup wizard, creates the "dev" user (password "dev") and
# three more users (alex, sam and robin, each with their name as password),
# adds Movies and TV libraries, sets who watched or favourited what (see
# the table near the end), and writes an API key for JellyTrim to
# dev/jellyfin-api-key.
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

# More users, so the watch rules (any one, majority, everyone) differ.
# Each signs in once, which also gives them recent activity in Jellyfin.
other_users="alex sam robin"
existing_users=$(api GET /Users "" "$token" | jq -r '.[].Name')
for name in $other_users; do
  if ! echo "$existing_users" | grep -qx "$name"; then
    api POST /Users/New "$(jq -nc --arg n "$name" '{Name: $n, Password: $n}')" "$token" >/dev/null
    log "created user $name"
  fi
done
sign_in() { # name: sets tok_<name> and uid_<name>
  local a; a=$(api POST /Users/AuthenticateByName "$(jq -nc --arg n "$1" --arg p "$2" '{Username: $n, Pw: $p}')")
  printf -v "tok_$1" '%s' "$(echo "$a" | jq -r .AccessToken)"
  printf -v "uid_$1" '%s' "$(echo "$a" | jq -r .User.Id)"
}
tok_dev=$token uid_dev=$user_id
for name in $other_users; do sign_in "$name" "$name"; done
log "signed in as $other_users"

# Who watched or favourited what. Cells: - (nothing), old (played 200 days
# ago), new (played 5 days ago), fav (favourite), old+fav. The script sets
# exactly this state, unmarking anything else, so running it again changes
# nothing.
users="dev alex sam robin"
matrix='Alpha - new - -
Bravo fav - - -
Charlie old old old -
Delta old - - -
Echo old old old old
Golf old - old+fav -'
long_ago=$(date -u -v-200d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '200 days ago' +%Y-%m-%dT%H:%M:%SZ)
recent=$(date -u -v-5d +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '5 days ago' +%Y-%m-%dT%H:%M:%SZ)

set_state() { # user item_id item_name cell
  local tv="tok_$1" uv="uid_$1" state played fav
  local tok=${!tv} uid=${!uv}
  state=$(api GET "/Items/$2?userId=$uid" "" "$tok" | jq -r '"\(.UserData.Played) \(.UserData.IsFavorite)"')
  played=${state% *} fav=${state#* }
  case "$4" in
    *old*|*new*)
      if [ "$played" != true ]; then
        local when=$long_ago; case "$4" in *new*) when=$recent ;; esac
        api POST "/UserPlayedItems/$2?userId=$uid&datePlayed=$when" "" "$tok" >/dev/null
        log "$1 played $3"
      fi ;;
    *)
      if [ "$played" = true ]; then
        api DELETE "/UserPlayedItems/$2?userId=$uid" "" "$tok" >/dev/null
        log "$1 unplayed $3"
      fi ;;
  esac
  case "$4" in
    *fav*) if [ "$fav" != true ]; then api POST "/UserFavoriteItems/$2?userId=$uid" "" "$tok" >/dev/null; log "$1 favourited $3"; fi ;;
    *) if [ "$fav" = true ]; then api DELETE "/UserFavoriteItems/$2?userId=$uid" "" "$tok" >/dev/null; log "$1 unfavourited $3"; fi ;;
  esac
}

echo "$matrix" | while read -r name cells; do
  id=$(item_id "$name")
  [ -n "$id" ] || { log "item $name not found, skipping"; continue; }
  set -- $cells
  for u in $users; do set_state "$u" "$id" "$name" "$1"; shift; done
done

# Read the state back from Jellyfin and print it.
cell() { # user item_id
  local tv="tok_$1" uv="uid_$1"
  local tok=${!tv} uid=${!uv}
  api GET "/Items/$2?userId=$uid" "" "$tok" | jq -r '.UserData as $d
    | [ (if $d.Played then "played " + (($d.LastPlayedDate // "")[0:10]) else empty end),
        (if $d.IsFavorite then "fav" else empty end) ] | if length == 0 then "-" else join(", ") end'
}
log "watch state:"
printf '  %-8s' item; for u in $users; do printf '  %-22s' "$u"; done; echo
echo "$matrix" | while read -r name _; do
  id=$(item_id "$name")
  [ -n "$id" ] || continue
  printf '  %-8s' "$name"
  for u in $users; do printf '  %-22s' "$(cell "$u" "$id")"; done
  echo
done

log "done. Jellyfin: $JF (users $USER_NAME, alex, sam and robin; each password is the name). JellyTrim: http://localhost:8097"
log "In JellyTrim setup, use http://jellyfin:8096 and the key in dev/jellyfin-api-key."
