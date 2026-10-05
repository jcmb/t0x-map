# t0x-map

Map browser for Trimble **T02 / T04 / T05** files under `/mnt/GPS_Admin/GNSS_Data` on [gnssplot.eng.trimble.com](https://gnssplot.eng.trimble.com/t0x/).

Single Go binary with embedded web UI. Metadata (lat/lon, time span) is extracted with `viewdat` and stored in SQLite.

## Layout

```
/mnt/GPS_Admin/GNSS_Data/{Group}/{Receiver}/*.T02|*.T04|*.T05
```

Extensions are **case-sensitive** (Linux): only `.T02`, `.T04`, `.T05`.

Groups are listed in config; receiver directories are discovered each index run.

## Build

Native (macOS for local smoke tests):

```bash
make build
./bin/t0x-map version
```

Cross-compile for **Linux x86_64** (gnssplot) from macOS — no Docker required (`CGO_ENABLED=0`):

```bash
make build-linux
# → bin/t0x-map-linux-amd64

make dist
# → dist/t0x-map-<version>-linux-amd64.tar.gz
```

## Deploy from macOS → gnssplot

### First install

On the Mac:

```bash
cd /Users/gkirk/Documents/GitHub/t0x-map
make dist
scp dist/t0x-map-*-linux-amd64.tar.gz gnssplot@gnssplot.eng.trimble.com:
```

On the server (`gnssplot`):

```bash
tar -xzf t0x-map-*-linux-amd64.tar.gz
cd t0x-map-*-linux-amd64

sudo install -d /usr/local/bin /etc/t0x-map /var/lib/t0x-map
sudo install -m 755 t0x-map /usr/local/bin/t0x-map
sudo test -f /etc/t0x-map/config.yaml || \
  sudo install -m 644 config.example.yaml /etc/t0x-map/config.yaml
sudo install -m 644 t0x-map.service /etc/systemd/system/t0x-map.service
sudo install -m 644 t0x-map-index.service /etc/systemd/system/t0x-map-index.service
sudo install -m 644 t0x-map-index.timer /etc/systemd/system/t0x-map-index.timer

sudo systemctl daemon-reload
sudo systemctl enable --now t0x-map.service
sudo systemctl enable --now t0x-map-index.timer
sudo t0x-map index -config /etc/t0x-map/config.yaml -progress
```

Apache (once): ensure `proxy` / `proxy_http` / `headers` are enabled, and `/t0x/` proxies to `127.0.0.1:8787` (see Reverse proxy below). Keep `base_path: ""` in config.

### Binary-only upgrade (usual case)

```bash
# Mac
cd /Users/gkirk/Documents/GitHub/t0x-map
make build-linux
scp bin/t0x-map-linux-amd64 gnssplot@gnssplot.eng.trimble.com:/tmp/t0x-map

# Server
sudo install -m 755 /tmp/t0x-map /usr/local/bin/t0x-map
sudo systemctl restart t0x-map.service
t0x-map version   # expect current VERSION
```

Units:

- `t0x-map.service` — long-running HTTP server
- `t0x-map-index.timer` — hourly full index

The service runs as user `gnssplot` by default (edit the unit if your account differs).

## Config

See [`config.example.yaml`](config.example.yaml). First install copies it to `/etc/t0x-map/config.yaml` if missing.

## Commands

```bash
# HTTP server (embedded map UI)
t0x-map serve -config /etc/t0x-map/config.yaml

# Full rescan (new/changed files + remove DB rows for deleted files)
t0x-map index -config /etc/t0x-map/config.yaml

# Same, with [n/total] progress lines (useful for first import)
t0x-map index -config /etc/t0x-map/config.yaml -progress

# Single file (call from download tool after a new T0x lands)
t0x-map index -config /etc/t0x-map/config.yaml -file /mnt/GPS_Admin/GNSS_Data/BASES/WCO_Base/foo.T04

# Inspect stored start/end times (debug date filters)
t0x-map files -config /etc/t0x-map/config.yaml -stats
t0x-map files -config /etc/t0x-map/config.yaml -group BASES -receiver WCO_Base -limit 20
```

## Reverse proxy (Apache)

Preferred: include the location fragment in the **existing** `gnssplot.trimble-wco.com` SSL vhost (avoids a second VirtualHost on the same name):

```bash
# Required first — without these, apachectl fails with: Invalid command 'ProxyPass'
sudo a2enmod proxy proxy_http headers
sudo systemctl restart apache2

sudo cp deploy/apache-t0x-map.location.conf /etc/apache2/conf-available/t0x-map.conf
sudo a2enconf t0x-map
sudo systemctl reload apache2
```

Full site file (only if this host is dedicated / not already defined): [`deploy/apache-t0x-map.conf`](deploy/apache-t0x-map.conf).

Proxy maps `https://gnssplot.eng.trimble.com/t0x/` → `http://127.0.0.1:8787/` (prefix stripped). Keep `base_path: ""` in `/etc/t0x-map/config.yaml`.

If the browser reports **too many redirects** and `curl -kI` shows `Location: ./`, the ProxyPass slash pairing is wrong or an old `FileServer` empty-path redirect is still running. Use the `ProxyPass /t0x/ http://127.0.0.1:8787/` form (trailing slash on **both** sides), set `DirectorySlash Off` under `/t0x/`, remove any `Redirect` to `./`, and redeploy t0x-map ≥ 0.5.

## Download-tool hook

After writing a new T0x file:

```bash
t0x-map index -config /etc/t0x-map/config.yaml -file "$NEW_FILE"
```

## Requirements on the server

- `viewdat` on `PATH` (or set `viewdat_path`)
- Read access to `data_root`
- Write access to `db_path`
