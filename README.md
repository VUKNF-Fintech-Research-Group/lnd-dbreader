# LND Database Reader

A service that continuously reads the Lightning Network Daemon (LND) channel graph and synchronizes it into a MySQL database, together with the full Docker stack around it: a graph-only LND node, MySQL, a database browser, a periodic JSON export served over HTTP, and optional Zabbix monitoring.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.23+-blue.svg)
![LND Version](https://img.shields.io/badge/LND-v0.19.3--beta-blue.svg)

<img width="1082" height="746" alt="Screenshot 2025-08-05 at 14 53 43" src="https://github.com/user-attachments/assets/e60587dd-070d-4353-9d60-53003090d541" />

</br>

## 🚀 Features

- **Continuous Synchronization**: Syncs the LND graph into MySQL at a configurable interval (30 minutes by default)
- **LND 0.19 Graph Architecture**: Reads the graph through LND's own `graph/db` package (Go dependency and the bundled node both pinned to LND v0.19.3-beta)
- **Database Lock Avoidance**: Copies `channel.db` aside before opening it, so the running LND node is never locked out
- **Batch Processing**: 5000-row `INSERT ... ON DUPLICATE KEY UPDATE` statements, each committed on its own
- **Append-Only History**: Rows are upserted, never deleted — `first_seen` / `last_seen` tell the story of every channel, alias and address
- **Data Export**: A gzip'd JSON snapshot of all three tables every 12 hours, served at `/rawdata/`
- **Docker Support**: One Docker Compose stack, hardened defaults (read-only containers, unprivileged users)
- **Monitoring**: Web dashboard, dbgate database browser, LND healthcheck with autoheal, optional Zabbix row-count metrics

</br>

## 📋 Table of Contents
- [Requirements](#-requirements)
- [Quick Start](#-quick-start)
- [Services](#-services)
- [How a Sync Works](#-how-a-sync-works)
- [Configuration](#️-configuration)
- [Database Schema](#-database-schema)
- [Data Export](#-data-export)
- [Monitoring](#-monitoring)
- [Repository Layout](#-repository-layout)
- [Development](#-development)

</br>

## 🔧 Requirements

### System Requirements
- **Ubuntu OS**: Tested using - Ubuntu 22.04.5 LTS
- **Docker & Docker Compose**: For containerized deployment

### Hardware Requirements
- **RAM**: 4GB+ (MySQL is configured with a 1 GB InnoDB buffer pool in `mysql/my.cnf`)
- **Storage**: 20GB+
- **CPU**: 4+ cores

</br>
</br>

## 🚀 Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/VUKNF-Fintech-Research-Group/lnd-dbreader.git
cd lnd-dbreader
```

### 2. Create Docker-Compose file stack

```bash
cp docker-compose.yml.sample docker-compose.yml
```

### 3. Edit configuration (if needed)
```bash
nano docker-compose.yml
```

### 4. Start the Services
Start all services
```bash
./runUpdateThisStack.sh
```
The script creates the `_DATA/` runtime directories and runs `docker-compose down` followed by `docker-compose up -d --build`. Use it for every restart or update — it is idempotent.

</br>

### 5. OPTIONAL: Check the state
Open in web browser:
```
http://<server-ip>/           # dashboard linking to the two tools below
http://<server-ip>/dbgate/    # database browser
http://<server-ip>/rawdata/   # the exported JSON snapshots
```
Run command to check the logs:
```bash
sudo docker-compose logs -f
```
A healthy dbreader log shows one block per sync — `SYNC #n`, the three `Processing ...` lines and `✅ Sync #n completed successfully!`. A `❌ ERROR during sync` line means that sync is skipped and the next tick retries; nothing else is needed. The LND node takes up to 15 minutes to sync to the chain on first start; dbreader's first sync against an empty graph simply imports nothing.

</br>
</br>

## 🧩 Services

All containers live in one Docker Compose stack, named `lnd-dbreader-<service>`:

| Service | Image | Role |
|---------|-------|------|
| `lnd-dbreader-endpoint` | `caddy:2.11-alpine` | The only published port (80). Serves the dashboard at `/`, proxies `/dbgate/`, and serves `_DATA/exporter` read-only at `/rawdata/` |
| `lnd-dbreader-lnd` | `lightninglabs/lnd:v0.19.3-beta` | Graph-only LND node: neutrino backend, fixed peer list, `--noseedbackup` (never holds funds). Runs as user 1000 on a read-only filesystem with its data at `/lnd`. Started by `lnd/start-lnd.sh` |
| `lnd-dbreader-autoheal` | `willfarrell/autoheal:1.2.0` | Restarts the LND container when its healthcheck (synced to chain and at least one peer) fails |
| `lnd-dbreader-dbreader` | `vuknf/lnd-dbreader-dbreader` | The Go sync service — this repository's `dbreader/`. Runs as user 1000 on a read-only filesystem; `/tmp` is a tmpfs because every sync copies `channel.db` there |
| `lnd-dbreader-mysql` | `mysql:8.4.0` | The database. Config in `mysql/my.cnf`, data in `_DATA/mysql` |
| `lnd-dbreader-exporter` | `vuknf/lnd-dbreader-exporter` | Python: writes `_DATA/exporter/lnd-dbreader.json.gz` every 12 hours |
| `lnd-dbreader-dbgate` | `dbgate/dbgate:7.2.3-alpine` | Web database browser, reachable through the endpoint only |
| `lnd-dbreader-zabbix` | `vuknf/lnd-dbreader-zabbix` | **Optional** (commented out in the sample): pushes table row counts to a Zabbix server every 30 minutes |

The LND node's data directory is `_DATA/lnd`, mounted at `/lnd` inside the LND container (not the image's default `/root/.lnd`, which an unprivileged user cannot reach); dbreader mounts `_DATA/lnd/data/graph/mainnet/` as `/data` and reads `channel.db` from there. `runUpdateThisStack.sh` chowns `_DATA` to `1000:1000` on every run, so the unprivileged containers own their files.

</br>

## 🔄 How a Sync Works

Every `SYNC_INTERVAL_MINUTES` (and once at start-up) dbreader:

1. **Copies** the live `channel.db` to `/tmp/channel_copy.db` (a tmpfs — the copy lives in RAM, ~0.5 GB today). bbolt holds an exclusive lock on the file LND has open, so the live file is never touched; the graph directory is mounted read-only. If LND was mid-write the copy can be inconsistent — that sync fails and the next one copies again.
2. **Opens the copy** read-only through LND's `kvdb` and `graph/db` packages, with the graph cache enabled.
3. **Creates the tables** if they are missing (`CREATE TABLE IF NOT EXISTS`, every sync — a wiped database heals itself).
4. **Imports** channel announcements, node announcements and node addresses, in that order. Each importer walks the graph and upserts rows in batches of 5000; every batch is its own autocommit transaction, so a failure keeps what was imported so far and the next sync re-applies the rest (every row is an idempotent upsert).

What is exported is the graph's **topology**: channels (announcement half only), nodes and their addresses. Channel **policies** — fees, CLTV deltas, HTLC limits, the disabled flag — are intentionally not exported.

</br>

## ⚙️ Configuration

Environment variables of the `lnd-dbreader-dbreader` service. The defaults are what the binary uses when a variable is unset; the sample compose file sets the MySQL ones explicitly.

### Environment Variables

| Variable | Built-in default | Sample compose | Description |
|----------|------------------|----------------|-------------|
| `MYSQL_HOST` | `lnd-dbreader-mysql` | `lnd-dbreader-mysql` | MySQL server hostname |
| `MYSQL_PORT` | `3306` | — | MySQL server port |
| `MYSQL_USER` | `lnd-dbreader` | `lnd_data` | MySQL username |
| `MYSQL_PASSWORD` | `lnd-dbreader` | `lnd_data` | MySQL password |
| `MYSQL_DATABASE` | `lnd-dbreader` | `lnd_data` | MySQL database name |
| `LND_DB_PATH` | `/data/channel.db` | — | Path to LND's channel database inside the container |
| `SYNC_INTERVAL_MINUTES` | `30` | — | Minutes between syncs. Decimals work (`1.5`); an unparsable value falls back to 30 with a warning in the log |

An **empty** value counts as unset (`MYSQL_PASSWORD=` yields the default, not an empty password).

### MySQL

`mysql/my.cnf` is mounted into the MySQL container. It raises `innodb_buffer_pool_size` to 1 GB — InnoDB keeps row locks in the buffer pool, and MySQL's 128 MB default overflowed on the upserts once the tables had grown (`Error 1206: The total number of locks exceeds the lock table size`). Changing it requires a MySQL restart (`./runUpdateThisStack.sh`).

### LND node

`lnd/start-lnd.sh` is the LND container's entrypoint, configured through the service's environment in `docker-compose.yml`: `NETWORK`, `NEUTRINO_CONNECT` (the **only** bitcoin peers the node talks to — they must serve compact block filters; `neutrinoChecker.py` tests candidates), `FEE_URL`, `LNDHOST`, and `LNDDIR` (default `/lnd` — must match the volume target and the `--lnddir` flag in the healthcheck's `lncli` calls).

</br>

## 📊 Database Schema

The application creates and maintains three tables. All three are **append-only history**: rows are upserted on their UNIQUE key and never deleted, so a channel that closes, a node that renames itself or an address that disappears keeps its row with a stale `last_seen`. Consumers distinguish current from historical rows by `last_seen`.

### `channel_announcements`
Stores Lightning Network channel announcements.

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT UNSIGNED | Primary key |
| `short_channel_id` | BIGINT UNSIGNED | Channel identifier (the uint64 form) |
| `node_id_1` | VARCHAR(66) | First node public key |
| `node_id_2` | VARCHAR(66) | Second node public key |
| `bitcoin_key_1` | VARCHAR(66) | First node Bitcoin key |
| `bitcoin_key_2` | VARCHAR(66) | Second node Bitcoin key |
| `extra_opaque_data` | TEXT | Additional channel data (hex) |
| `json_data` | JSON | Complete announcement JSON (`short_channel_id` there is in `block x tx x out` form) |
| `first_seen` | TIMESTAMP | First time seen |
| `last_seen` | TIMESTAMP | Last update time |

UNIQUE key: every announced field (`extra_opaque_data` by its first 255 bytes). A re-announcement with different opaque data is a new row.

### `node_announcements`
Stores Lightning Network node announcements.

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT UNSIGNED | Primary key |
| `node_id` | VARCHAR(66) | Node public key |
| `alias` | VARCHAR(255) | Node alias/name |
| `rgb_color` | VARCHAR(7) | Node color (hex) |
| `json_data` | JSON | Complete announcement JSON (id, alias, addresses, timestamp, colour) |
| `first_seen` | TIMESTAMP | First time seen |
| `last_seen` | TIMESTAMP | Last update time |

UNIQUE key: `(node_id, alias, rgb_color)` — a node that renames or recolours itself gets a new row; the old one stays as history.

### `node_addresses`
Stores network addresses for Lightning Network nodes.

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT UNSIGNED | Primary key |
| `node_id` | VARCHAR(66) | Node public key |
| `address` | VARCHAR(255) | IP address, hostname or `.onion` |
| `port` | INT UNSIGNED | Port number (`0` when the address had none) |
| `first_seen` | TIMESTAMP | First time seen |
| `last_seen` | TIMESTAMP | Last update time |

UNIQUE key: `(node_id, address, port)`.

</br>

## 📦 Data Export

`lnd-dbreader-exporter` (`exporter/main.py`) writes `_DATA/exporter/lnd-dbreader.json.gz` three minutes after start and then every 12 hours. The file is written to a temporary name and renamed into place, so a download never sees a half-written file. The endpoint serves the directory at `http://<server-ip>/rawdata/`.

Format:

```json
{
  "meta": {
    "timestamp": 1756200000,
    "exported_at": "2026-08-26 13:31:34",
    "lnd_dbreader_version": "1.0",
    "summary": { "total_channel_announcements": 0, "total_node_addresses": 0, "total_node_announcements": 0 }
  },
  "data": {
    "channel_announcements": [ { "ShortChannelID": 0, "NodeID1": "...", "NodeID2": "..." } ],
    "node_addresses":        [ { "NodeID": "...", "Address": "...", "Port": 0, "FirstSeen": 0, "LastSeen": 0 } ],
    "node_announcements":    [ { "NodeID": "...", "Alias": "...", "FirstSeen": 0, "LastSeen": 0 } ]
  }
}
```

Notes: every table is exported whole (history included); node announcements with an empty alias are skipped; channel rows carry no timestamps.

</br>

## 📈 Monitoring

- **Dashboard** — `http://<server-ip>/` links to the tools below.
- **Database browser** — `http://<server-ip>/dbgate/` (dbgate, connected as root to the MySQL container).
- **LND health** — the LND container's healthcheck requires `synced_to_chain: true` and at least one peer (checked every 30 minutes after a 15-minute start period); `lnd-dbreader-autoheal` restarts it after 5 consecutive failures.
- **Zabbix** (optional) — enable the `lnd-dbreader-zabbix` service in `docker-compose.yml` and point it at your server. Every 30 minutes it sends the row count of each table in `TABLES_TO_CHECK` as a **trapper item whose key is the table name**, on the host named by `ZABBIX_MONITORING_HOST`. Create those trapper items on the Zabbix side first; a count that stops growing means the sync has stalled.

</br>

## 🗂 Repository Layout

```
lnd-dbreader/
├── docker-compose.yml.sample   — the stack; copy to docker-compose.yml (git-ignored)
├── runUpdateThisStack.sh       — idempotent bring-up script
├── neutrinoChecker.py          — tests candidate NEUTRINO_CONNECT peers for compact filters
├── endpoint/Caddyfile          — the ingress: dashboard, /dbgate/, /rawdata/
├── lnd/start-lnd.sh            — LND container entrypoint
├── mysql/my.cnf                — MySQL settings (InnoDB buffer pool)
├── dbreader/
│   ├── Dockerfile              — builds the Go service from app/
│   ├── pushDockerhub.sh
│   └── app/                    — Go module `lnd-dbreader`
│       ├── main.go             — config, sync loop
│       ├── db/                 — schema (initialization.go) and importers (announcements.go)
│       └── models/             — LND type wrappers and the ChannelGraph interface
├── exporter/                   — Python: the 12-hour JSON export
├── zabbix/                     — Python: optional row-count metrics
└── _DATA/                      — runtime data (git-ignored): lnd/, mysql/, exporter/
```

</br>

## 🛠 Development

The sample compose file uses the published images. To build from source, flip the `# Self-built` comment toggles on the `dbreader`, `exporter` or `zabbix` service (`image:` + `build:`), then run `./runUpdateThisStack.sh`. The commented `# Dev` volume lines on the Python services (exporter, zabbix) mount the source directory over `/app` so edits run without a rebuild; the Go service has no live-edit mode — its image holds only the compiled binary, so a change to `dbreader/app` needs a rebuild.

Nothing is installed on the host: builds run in containers. To vet and build the Go service by hand:

```bash
docker run --rm -v "$PWD/dbreader/app:/src:ro" golang:1.23 \
  sh -c 'cp -r /src /b && cd /b && go mod tidy && go vet ./... && go build ./...'
```

### Code style

All source files (Go, Python, shell) follow one commenting style: a header banner per file, a banner per function/type with a real "Used by" list, `STEP` narration inside phase-heavy functions, and wide blank-line gaps between sections. The banners are the documentation — there are no docstrings or Go doc comments, and known quirks are written down next to the code they describe.


</br>

## 🙏 Acknowledgments

- [Lightning Network Daemon (LND)](https://github.com/lightningnetwork/lnd) - Lightning Network implementation
- [Lightning Network Community](https://lightning.network/) - For the amazing Lightning Network ecosystem
