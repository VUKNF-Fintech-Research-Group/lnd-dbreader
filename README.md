# LND Database Reader v0.19.3

A production-ready service that continuously reads Lightning Network Daemon (LND) channel graph data and synchronizes it to a MySQL database. This application is specifically designed for LND v0.19.3-beta and handles the new graph database architecture introduced in that version.

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.23+-blue.svg)
![LND Version](https://img.shields.io/badge/LND-v0.19.3--beta-blue.svg)

<img width="1082" height="746" alt="Screenshot 2025-08-05 at 14 53 43" src="https://github.com/user-attachments/assets/e60587dd-070d-4353-9d60-53003090d541" />

</br>

## 🚀 Features

- **Continuous Synchronization**: Automatically syncs LND graph data at configurable intervals
- **LND v0.19.3 Compatible**: Fully supports the new graph database architecture
- **Production Ready**: Includes graceful shutdown, error recovery, and robust logging
- **Database Lock Avoidance**: Uses file copying to avoid conflicts with running LND
- **Batch Processing**: Efficient bulk inserts for high-performance data processing
- **Docker Support**: Complete containerized setup with Docker Compose
- **MySQL Integration**: Stores data in structured MySQL tables for analysis
- **Comprehensive Logging**: Detailed logs for monitoring and debugging

</br>

## 📋 Table of Contents
- [Quick Start](#-quick-start)
- [Configuration](#️-configuration)
- [Database Schema](#-database-schema)

</br>

## 🔧 Requirements

### System Requirements
- **Ubuntu OS**: Tested using - Ubuntu 22.04.5 LTS
- **Docker & Docker Compose**: For containerized deployment

### Hardware Requirements
- **RAM**: 4GB+
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

</br>

### 5. OPTIONAL: Check the state
Open in web browser:
```
http://<server-ip>/dbgate
```
Run command to check the logs:
```bash
sudo docker-compose logs -f
```


</br>
</br>


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

</br>

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
Access the database browser at http://<server-ip>/dbgate


</br>

## 🙏 Acknowledgments

- [Lightning Network Daemon (LND)](https://github.com/lightningnetwork/lnd) - Lightning Network implementation
- [Lightning Network Community](https://lightning.network/) - For the amazing Lightning Network ecosystem

