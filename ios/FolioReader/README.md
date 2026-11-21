# Folio iOS Client (`FolioReader`)

Native SwiftUI client for your self-hosted **Folio** EPUB library, designed for zero-configuration Tailscale (`folio`) discovery, offline reading, and bidirectional KOReader/CrossPoint progress sync.

## Features

- **Zero-Config Discovery**:
  - Races `https://folio/api/discover` (Tailscale Serve), `http://folio:8080/api/discover`, `http://folio:3000/api/discover`, and LAN Bonjour (`_folio._tcp.local.`) automatically on launch.
  - Supports 1-tap pairing via `folio://connect?url=...` from Folio's Web UI.
- **Offline Library & Caching**:
  - Caches your catalog locally so you can browse and read without a connection.
  - Downloads `.epub` files into `Application Support/FolioOffline/Books/` and pre-extracts them using `ZIPFoundation`.
- **Native Paginated Book Reader**:
  - Uses `WKWebView` with CSS multi-column pagination, horizontal swipe & edge-tap page turning, distraction-free chrome toggle, TOC drawer, font family/size controls, and *Paper / Sepia / E-Ink / Night* themes.
- **Offline-Resilient Progress Sync**:
  - Syncs reading positions (`PUT /syncs/progress` & `GET /syncs/progress/{hash}`) automatically using your Tailscale identity.
  - Queues offline reading checkpoints locally and flushes them automatically via `NWPathMonitor` as soon as connectivity returns.

## Setup on macOS (Xcode)

### Option A — Generate Xcode Project with XcodeGen (Recommended)

```bash
brew install xcodegen
cd ios/FolioReader
xcodegen generate
open FolioReader.xcodeproj
```

1. Select the **FolioReader** target → **Signing & Capabilities** → pick your Apple Developer Team.
2. Connect your iPhone (or choose an iOS 17+ Simulator) and press **Run (`Cmd + R`)**.

### Option B — Manual Xcode Project

1. Create a new **iOS App** (SwiftUI, iOS 17.0+) in Xcode.
2. Add Swift Package Dependency: `https://github.com/weichsel/ZIPFoundation.git` (from `0.9.19`).
3. Replace the default sources with `Sources/` and copy `Info.plist` (which registers `_folio._tcp` Bonjour permissions, `folio://` URL scheme, and ATS settings for Tailnet HTTP/HTTPS).
