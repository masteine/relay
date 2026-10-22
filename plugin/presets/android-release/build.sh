#!/bin/sh
# Relay preset android-release · build. v1 fills this in; v0 only checks the toolchain.
set -eu
[ -x ./gradlew ] || { echo "android-release: no ./gradlew in $(pwd)" >&2; exit 2; }
exec ./gradlew --no-daemon assembleRelease
