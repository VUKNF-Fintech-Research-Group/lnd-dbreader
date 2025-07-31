# LND Database Reader v0.19.1

A production-ready service that continuously reads Lightning Network Daemon (LND) channel graph data and synchronizes it to a MySQL database. This application is specifically designed for LND v0.19.1-beta and handles the new graph database architecture introduced in that version.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.23+-blue.svg)
![LND Version](https://img.shields.io/badge/LND-v0.19.1--beta-blue.svg)

## 🚀 Features

- **Continuous Synchronization**: Automatically syncs LND graph data at configurable intervals
- **LND v0.19.1 Compatible**: Fully supports the new graph database architecture
- **Production Ready**: Includes graceful shutdown, error recovery, and robust logging
- **Database Lock Avoidance**: Uses file copying to avoid conflicts with running LND
- **Batch Processing**: Efficient bulk inserts for high-performance data processing
- **Docker Support**: Complete containerized setup with Docker Compose
- **MySQL Integration**: Stores data in structured MySQL tables for analysis
- **Comprehensive Logging**: Detailed logs for monitoring and debugging

## 📋 Table of Contents

- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Database Schema](#database-schema)

## 🔧 Requirements

### System Requirements
- **Docker & Docker Compose**: For containerized deployment
- **LND v0.19.1-beta**: Running Lightning Network node
- **MySQL 8.0+**: Database server
- **Go 1.23+**: For development/building from source

### Hardware Requirements
- **RAM**: 4GB+
- **Storage**: 20GB+
- **CPU**: 4+ cores

## 🚀 Quick Start

### 1. Clone the Repository

```bash
git clone https://github.com/VUKNF-Fintech-Research-Group/lnd-dbreader.git
cd lnd-dbreader
```

### 2. Configure Environment

Create `docker-compose.yml` from sample:

```bash
cp docker-compose.yml.sample docker-compose.yml
```

### 3. Start the Services

```bash
# Start all services
./runUpdateThisStack.sh

# View logs
sudo docker-compose logs -f

# View database
# Access DbGate at http://<server-ip>:8092 (admin/admin)
```

## ⚙️ Configuration

Below environment variables for lnd-dbreader-dbreader service configuration:

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `MYSQL_HOST` | `lnd-dbreader-mysql` | MySQL server hostname |
| `MYSQL_PORT` | `3306` | MySQL server port |
| `MYSQL_USER` | `lnd_data` | MySQL username |
| `MYSQL_PASSWORD` | `lnd_data` | MySQL password |
| `MYSQL_DATABASE` | `lnd_data` | MySQL database name |
| `LND_DB_PATH` | `/data/channel.db` | Path to LND channel database |
| `SYNC_INTERVAL_MINUTES` | `30` | Sync interval in minutes |

### Docker Compose Services

- **lnd-dbreader-dbreader**: Main application service
- **lnd-dbreader-mysql**: MySQL database server
- **lnd-dbreader-dbgate**: Web-based database browser
- **lnd-dbreader-lnd**: LND Lightning Network node

## 📊 Database Schema

The application creates and maintains three main tables:

### `channel_announcements`
Stores Lightning Network channel announcements.

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT UNSIGNED | Primary key |
| `short_channel_id` | BIGINT UNSIGNED | Channel identifier |
| `node_id_1` | VARCHAR(66) | First node public key |
| `node_id_2` | VARCHAR(66) | Second node public key |
| `bitcoin_key_1` | VARCHAR(66) | First node Bitcoin key |
| `bitcoin_key_2` | VARCHAR(66) | Second node Bitcoin key |
| `extra_opaque_data` | TEXT | Additional channel data |
| `json_data` | JSON | Complete announcement JSON |
| `first_seen` | TIMESTAMP | First time seen |
| `last_seen` | TIMESTAMP | Last update time |

### `node_announcements`
Stores Lightning Network node announcements.

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT UNSIGNED | Primary key |
| `node_id` | VARCHAR(66) | Node public key |
| `alias` | VARCHAR(255) | Node alias/name |
| `rgb_color` | VARCHAR(7) | Node color (hex) |
| `json_data` | JSON | Complete announcement JSON |
| `first_seen` | TIMESTAMP | First time seen |
| `last_seen` | TIMESTAMP | Last update time |

### `node_addresses`
Stores network addresses for Lightning Network nodes.

| Column | Type | Description |
|--------|------|-------------|
| `id` | BIGINT UNSIGNED | Primary key |
| `node_id` | VARCHAR(66) | Node public key |
| `address` | VARCHAR(255) | IP address or hostname |
| `port` | INT UNSIGNED | Port number |
| `first_seen` | TIMESTAMP | First time seen |
| `last_seen` | TIMESTAMP | Last update time |


### Database Monitoring

Access the database browser at http://<server-ip>:8092 with credentials:
- **Username**: `admin`
- **Password**: `admin`


## 🙏 Acknowledgments

- [Lightning Network Daemon (LND)](https://github.com/lightningnetwork/lnd) - Lightning Network implementation
- [Lightning Network Community](https://lightning.network/) - For the amazing Lightning Network ecosystem

