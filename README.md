# Folio — Self-Hosted EPUB Library, Web Reader, KOReader Sync & Xteink X3 Companion

**Folio** is an open-source (MIT), self-hosted web application written in **Go** for managing your personal EPUB library. It pairs a high-craft web library and reader with **Tailscale Zero-Trust Authentication**, native **KOReader Progress Sync (`kosync`)**, **OPDS 1.2 & 2.0 catalogs**, and wireless book pushing to **Xteink X3 / X4 e-readers running CrossPoint Reader firmware**.

---

## Key Features

- **Tailscale Zero-Trust Authentication (`internal/tailscale`)**
  - **Tailscale Serve Identity Headers**: Automatically authenticates users via `Tailscale-User-Login`, `Tailscale-User-Name`, and `Tailscale-User-Profile-Pic` injected by `tailscale serve` or the included Docker Tailscale sidecar (`tailscale-serve.json`).
  - **Tailscale LocalAPI `WhoIs`**: Queries `/localapi/v0/whois?addr=<remote_addr>` directly over the local `tailscaled` Unix socket (`/var/run/tailscale/tailscaled.sock` or `TS_SOCKET`) to identify any tailnet peer (`100.64.0.0/10` / `fd7a:115c:a1e0::/48`) and its MagicDNS node name.
  - **E-Reader Sync Passkey Pairing**: Automatically links your Tailscale identity (`reader@tailnet.ts.net`) to a KOReader MD5 sync key (`x-auth-user` / `x-auth-key`) so microcontroller e-readers like the **ESP32-C3 Xteink X3** (which cannot run a full `tailscaled` daemon) can sync reading progress to the exact same account.
  - **Flexible Enforcement (`TAILSCALE_AUTH_MODE=auto|required|off`) & Allowlists (`TAILSCALE_ALLOWED_USERS`)**: Use `required` in production to enforce strict Tailnet identity on the Web UI and API, or `auto` for zero-config local development with an interactive Tailscale session switcher.
- **EPUB Metadata & Book Cover Management**
  - Automatic parsing of EPUB 2 & EPUB 3 packages (`META-INF/container.xml`, OPF Dublin Core `dc:title`, `dc:creator`, `dc:subject`, `dc:description`, `dc:publisher`, `dc:date`, `dc:language`, `dc:identifier`, plus Calibre/EPUB3 series & series index).
  - Automatic book cover extraction (`properties="cover-image"`, `<meta name="cover">`, manifest heuristics) with fallback bespoke typographic bookplate generation.
  - Edit metadata and upload replacement covers from the browser, with optional **OPF Repack** that writes edited metadata and covers directly back into the `.epub` file so OPDS downloads and Xteink X3 transfers include your edits.
- **KOReader & CrossPoint Compatible Progress Sync**
  - Byte-compatible with stock `sync.koreader.rocks` (`POST /users/create`, `GET /users/auth`, `PUT /syncs/progress`, `GET /syncs/progress/{document}`).
  - Implements **CrossPoint Reader Extended Sync (`/api/v1`)** including lossless `CompactPosition` (`PUT /api/v1/progress`, `PUT /api/v1/progress/batch`, `GET /api/v1/progress/changes`), bookmarks delta-sync (`/api/v1/bookmarks/{document}`), and clippings/highlights (`/api/v1/clippings/{document}`).
  - Automatically computes both **KOReader Binary Partial MD5 (`partial_md5_checksum`)** and **Filename MD5** for every book in your library so reading progress syncs seamlessly between your Xteink X3, KOReader devices, and the built-in Web Reader.
- **OPDS 1.2 (Atom XML) & OPDS 2.0 (JSON) Catalog**
  - Browse and download books directly on your e-reader via `/opds` (OPDS 1.2 Atom XML with OpenSearch `/opds/opensearch.xml`, Recent, All Books, Authors, Series, and Subject facets) or `/opds/v2` (OPDS 2.0 JSON).
- **Built-in Web EPUB Reader**
  - Read any book in your browser with full chapter navigation, Table of Contents, bookmarks, custom typography (*Newsreader*, *Georgia*, *Plus Jakarta Sans*, *JetBrains Mono*), four surface themes (*Warm Paper*, *Sepia*, *E-Ink Crisp*, *Archival Night*), and real-time bidirectional KOReader progress sync.
- **Xteink X3 with CrossPoint Reader Integration**
  - **Wireless Book Push**: Push EPUBs directly to your Xteink X3 while it is in **File Transfer** or **Calibre Wireless** mode using:
    1. **CrossPoint WebSocket Fast Upload (Port 81)**: `START:<filename>:<size>:<path>` &rarr; `READY` &rarr; binary chunk streaming &rarr; `PROGRESS:<received>:<total>` &rarr; `DONE`.
    2. **CrossPoint HTTP Multipart Upload (Port 80)**: `POST /upload?path=/Books&overwrite=1` (atomic `.part` staging and automatic library index refresh).
    3. **CrossPoint WebDAV (Port 80)**: `PUT /Books/<book>.epub`.
    4. **Browser-Direct Push**: Transfer directly from your browser to the Xteink X3 over local Wi-Fi or the X3's hotspot (`192.168.4.1` / `crosspoint.local`) even when Folio is hosted remotely.
  - **UDP Network Discovery (Port 8134)**: Broadcasts `hello` to discover CrossPoint Reader devices on your LAN.
  - **Remote SD Card File Manager & One-Click Provisioning**: Inspect `/api/status` (hardware model, firmware, RSSI, free ESP32-C3 heap), browse/delete files via `/api/files`, and automatically configure Folio's OPDS feed (`/api/opds`) and KOReader sync URL (`/api/settings`) on your Xteink X3 with one click.
- **SQLite (Default) & PostgreSQL Support**
  - Zero-config **SQLite** storage out of the box (`DB_DRIVER=sqlite`), or switch to **PostgreSQL** (`DB_DRIVER=postgres`) via environment variables.

---

## Quick Start with Docker & Tailscale

### 1. Run Pre-Built Image from GitHub Container Registry (`ghcr.io`)

Multi-architecture images (`linux/amd64` and `linux/arm64`) are published automatically via [`.github/workflows/docker-publish.yml`](.github/workflows/docker-publish.yml):

```bash
docker run -d \
  --name folio \
  -p 8080:8080 \
  -v folio_data:/data \
  ghcr.io/jobi/folio:latest
```

Or using Docker Compose (SQLite default):

```bash
docker compose up -d --build
```

Open `http://localhost:8080` in your browser.

### 2. With Tailscale Sidecar (Automatic HTTPS + Tailscale Serve Identity)

```bash
TS_AUTHKEY=tskey-auth-xxxxx \
TAILSCALE_AUTH_MODE=required \
docker compose --profile tailscale up -d --build
```

Or on a Linux host already running `tailscaled`:

```bash
tailscale serve --bg 8080
```

### 3. With PostgreSQL

```bash
DB_DRIVER=postgres \
DB_DSN="postgres://folio:folio@postgres:5432/folio?sslmode=disable" \
docker compose --profile postgres up -d --build
```

---

## Running from Source (Linux / Go)

```bash
go build -o folio ./cmd/folio
./folio
```

---

## Connecting Your Xteink X3 (CrossPoint Reader)

1. **Direct Wireless Push**:
   - On your Xteink X3 running CrossPoint Reader, open **File Transfer** (or **Calibre Wireless**) and connect to your Wi-Fi network. Note the IP address shown on the X3 screen (or use `crosspoint.local`).
   - In Folio, click **Xteink X3 Companion**, add your device IP (HTTP port `80`, WebSocket port `81`), or click **Scan LAN (UDP 8134)**.
   - Click **Push X3** on any book card to stream the EPUB directly into `/Books` on your X3's SD card.
2. **OPDS Catalog on Xteink X3**:
   - Click **Save OPDS & Sync to X3** inside Folio's Xteink X3 Companion modal while File Transfer mode is active, **or** manually add `http://<your-server-ip>:8080/opds` under CrossPoint's OPDS browser.
3. **KOReader Progress Sync (with Tailscale Passkey)**:
   - Open the **Tailscale Authentication & Device Passkeys** modal in Folio to view or rotate your KOReader sync passkey, and set your Custom Sync Server URL in CrossPoint Reader or KOReader to `http://<your-server-ip>:8080`.

---

## License

Released under the [MIT License](LICENSE).
