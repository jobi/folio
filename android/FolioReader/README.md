# Folio Android Client (`FolioReader`)

Native Jetpack Compose Android client for your self-hosted **Folio** EPUB library, built for modern Android devices and optimized out of the box for **Boox Palma (Android 11 / E-Ink)**.

## Highlights
- **Boox Palma & E-Ink Ready (`minSdk 26` / Android 8.0–15+)**:
  - **Volume Up / Down buttons turn pages by default** (Down = Next, Up = Previous, with optional swap & toggle in Reader **Aa** and Settings), plus Page Up / Page Down / D-pad support.
  - Auto-detects ONYX/Boox E-Ink devices to default to the high-contrast **E-Ink** theme and disable page-turn slide animations (zero ghosting).
- **Zero-Config Discovery**:
  - Races `https://folio`, `http://folio:8080`, `http://folio:3000`, `http://folio.local:8080`, and LAN mDNS (`_folio._tcp.`).
  - Supports `folio://connect?url=...` deep links and custom manual server URLs.
- **Offline Reading & Fast Cover Cache**:
  - Downloads and pre-extracts `.epub` files locally for offline reading.
  - Two-tier (RAM + downsampled disk) cover cache prevents reloading when scrolling.
- **Furthest-Along Progress Sync**:
  - Syncs reading positions with Folio / CrossPoint / KOReader without clobbering further-along progress from other devices, with a manual **Sync (↻)** button and explicit override prompt.

## Build & Install

```bash
cd android/FolioReader
./gradlew assembleDebug
adb install -r app/build/outputs/apk/debug/app-debug.apk
```
