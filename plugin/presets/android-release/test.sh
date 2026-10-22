#!/bin/sh
# Relay preset android-release · test. Unit + Compose UI tests without an emulator.
set -eu
[ -x ./gradlew ] || { echo "android-release: no ./gradlew in $(pwd)" >&2; exit 2; }
exec ./gradlew --no-daemon testDebugUnitTest
