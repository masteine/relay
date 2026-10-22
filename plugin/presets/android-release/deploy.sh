#!/bin/sh
# Relay preset android-release · deploy. GitHub Release with the release APK(s).
# Usage: deploy.sh <version>
set -eu
v="${1:?version, e.g. 1.4.0}"
set -- app/build/outputs/apk/release/*.apk
[ -f "$1" ] || { echo "android-release: no release APK, run build.sh first" >&2; exit 2; }
exec gh release create "v$v" "$@" --generate-notes
