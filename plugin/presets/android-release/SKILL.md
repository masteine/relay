---
name: android-release
description: Relay preset for an Android app released through GitHub Releases or Google Play internal track. Build with Gradle, unit and Compose UI tests without an emulator, deploy with `gh release create`.
---

What this preset knows, so that `relay.yml` does not have to:

| | Default | Override in relay.yml |
|---|---|---|
| Build | `./gradlew assembleRelease` (`build.sh`) | — |
| Test | `./gradlew testDebugUnitTest` (`test.sh`); Compose UI tests run as unit tests with Robolectric, no emulator in v0 | `test:` |
| Deploy | `gh release create v<version> app/build/outputs/apk/release/*.apk` (`deploy.sh`); Play internal track is a v1 option | `deploy:` |
| Signing | `KEYSTORE_*` from the runner's environment, never from the repository | — |
| Files triage reads first | `app/build.gradle(.kts)`, `settings.gradle(.kts)`, `app/src/main/AndroidManifest.xml`, `gradle/libs.versions.toml` | — |

v0 ships only the scripts' skeleton: the plan stage reads this file to know
the project's conventions; the implement, test and release stages that run the
scripts arrive in v1.
