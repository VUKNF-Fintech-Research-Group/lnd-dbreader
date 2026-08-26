############################################################
#  [*] exporter — the BLNSTATS feed: MySQL → one .json.gz
#
#  Reads the three tables dbreader keeps in MySQL and
#  writes them as a single gzip'd JSON to
#  /DATA/lnd-dbreader.json.gz (compose mounts
#  ./_DATA/exporter there), 3 minutes after start and then
#  every 12 hours. The file is written to a _tmp name and
#  renamed into place, so a reader never sees a half-written
#  file. ~30 MB gzip'd today: 480k channel rows, 120k
#  addresses, 28k node aliases. The endpoint Caddy serves
#  _DATA/exporter read-only at http://<host>/rawdata/, and
#  that is where the BLNSTATS server fetches it from.
#
#  Environment (none set by compose — defaults apply):
#    DB_HOST / DB_NAME / DB_USER / DB_PASSWORD
#
#  Every table is dbreader's append-only history, exported
#  WHOLE: closed channels, old aliases and dropped addresses
#  are all in the file. Addresses and aliases carry
#  FirstSeen/LastSeen so the consumer can tell; channels do
#  NOT — see get_data_channel_announcements.
#
#  The MySQL connection lives only for the export itself:
#  it is closed before the 12-hour sleep, because MySQL
#  drops idle connections after wait_timeout = 8 h and
#  closing a dropped one raises OperationalError — which
#  used to escape the loop and crash the process once per
#  cycle, leaving docker's restart policy to revive it.
############################################################


import os
import requests
from time import sleep
import mysql.connector
from mysql.connector.cursor import MySQLCursorDict
import json
import gzip
from datetime import datetime








# Stamped into meta.lnd_dbreader_version of every export —
# the consumer's compatibility marker, not the Go app's
LND_DBREADER_VERSION = "1.0"

# The compose stack sets none of the DB_* variables, so
# these are what actually connects
DEFAULT_DB_HOST = 'lnd-dbreader-mysql'
DEFAULT_DB_NAME = 'lnd_data'
DEFAULT_DB_USER = 'lnd_data'
DEFAULT_DB_PASSWORD = 'lnd_data'








############################################################
# get_db_connection
############################################################
#
# One mysql-connector connection from the DB_* environment.
# The `cursor_class` line is a NO-OP: MySQLConnection has no
# such attribute (the real API is cursor(dictionary=True)),
# so it just creates an unused attribute and every cursor
# keeps returning TUPLES — verified inside the image with
# mysql-connector 8.4.0. The collectors below rely on that:
# their dict(zip(columns, row)) conversion would silently
# produce garbage on dict rows. Do not "fix" one without
# the other.
#
# Used by:
#   - __main__ (below) — once per loop iteration
############################################################

def get_db_connection():
    conn = mysql.connector.connect(
        host=os.getenv('DB_HOST', DEFAULT_DB_HOST),
        database=os.getenv('DB_NAME', DEFAULT_DB_NAME),
        user=os.getenv('DB_USER', DEFAULT_DB_USER),
        password=os.getenv('DB_PASSWORD', DEFAULT_DB_PASSWORD)
    )
    conn.cursor_class = MySQLCursorDict
    return conn








############################################################
# log_with_timestamp
############################################################
#
# print() with a local-time stamp (compose mounts
# /etc/localtime). The Dockerfile runs python3 -u, so the
# line reaches docker logs immediately, not at buffer flush.
#
# Used by:
#   - every function in this file
############################################################

def log_with_timestamp(message):
    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    print(f"[{timestamp}] {message}")








############################################################
# get_data_channel_announcements
############################################################
#
# Every row of channel_announcements as
# {ShortChannelID, NodeID1, NodeID2} — the only collector
# WITHOUT FirstSeen/LastSeen, so the consumer cannot tell a
# closed channel from an open one; all 480k historical rows
# look alive. modifiedAfter is interpolated raw into the
# SQL (no quoting — it would have to be a MySQL expression)
# and no caller passes it: the export is always full.
#
# Used by:
#   - export_data_to_file (below)
############################################################

def get_data_channel_announcements(cursor, modifiedAfter=None):
    log_with_timestamp("Collecting channel announcements data...")

    cursor.execute(f"""
        SELECT 
            short_channel_id AS ShortChannelID,
            node_id_1 AS NodeID1,
            node_id_2 AS NodeID2
        FROM channel_announcements
        {f"WHERE last_seen > {modifiedAfter}" if modifiedAfter is not None else ""}
    """)
    rows = cursor.fetchall()

    # Tuple rows → dicts keyed by the SELECT aliases (see
    # get_db_connection for why the rows are tuples)
    columns = [col[0] for col in cursor.description]
    data_list = []
    for row in rows:
        row_dict = dict(zip(columns, row))
        data_list.append(row_dict)

    log_with_timestamp(f"Collected {len(data_list)} channel announcements")
    return data_list








############################################################
# get_data_node_addresses
############################################################
#
# Every row of node_addresses as {NodeID, Address, Port,
# FirstSeen, LastSeen}, timestamps as unix seconds. Port is
# 0 for addresses dbreader could not split a port off.
# Same raw, never-used modifiedAfter as the channel
# collector.
#
# Used by:
#   - export_data_to_file (below)
############################################################

def get_data_node_addresses(cursor, modifiedAfter=None):
    log_with_timestamp("Collecting node addresses data...")

    cursor.execute(f"""
        SELECT 
            node_id AS NodeID,
            address AS Address,
            port AS Port,
            UNIX_TIMESTAMP(first_seen) AS FirstSeen,
            UNIX_TIMESTAMP(last_seen) AS LastSeen
        FROM node_addresses
        {f"WHERE last_seen > {modifiedAfter}" if modifiedAfter is not None else ""}
    """)
    rows = cursor.fetchall()


    # Tuple rows → dicts keyed by the SELECT aliases
    columns = [col[0] for col in cursor.description]
    data_list = [dict(zip(columns, row)) for row in rows]

    log_with_timestamp(f"Collected {len(data_list)} node addresses")
    return data_list








############################################################
# get_data_node_announcements
############################################################
#
# node_announcements as {NodeID, Alias, FirstSeen,
# LastSeen}, minus rows with an empty alias — nodes that
# never set one are absent from the file. A node that
# renamed itself is present once per alias (dbreader keys
# the table on node_id + alias + colour), with its own
# FirstSeen/LastSeen each. Colour and the JSON blob are not
# exported. Same raw, never-used modifiedAfter as the
# others.
#
# Used by:
#   - export_data_to_file (below)
############################################################

def get_data_node_announcements(cursor, modifiedAfter=None):
    log_with_timestamp("Collecting node announcements data...")

    cursor.execute(f"""
        SELECT 
            node_id AS NodeID,
            alias AS Alias,
            UNIX_TIMESTAMP(first_seen) AS FirstSeen,
            UNIX_TIMESTAMP(last_seen) AS LastSeen
        FROM node_announcements
        WHERE alias <> ''
        {f"AND last_seen > {modifiedAfter}" if modifiedAfter is not None else ""}
    """)
    rows = cursor.fetchall()


    # Tuple rows → dicts keyed by the SELECT aliases
    columns = [col[0] for col in cursor.description]
    data_list = [dict(zip(columns, row)) for row in rows]

    log_with_timestamp(f"Collected {len(data_list)} node announcements")
    return data_list








############################################################
# export_data_to_file
############################################################
#
# One export: the three collectors on one cursor, wrapped
# in {meta, data}, gzip'd to /DATA/lnd-dbreader.json.gz via
# a _tmp file and os.rename — atomic on the same
# filesystem, so BLNSTATS never reads a torn file. meta
# carries the unix timestamp, a local-time string, the
# version marker and the three row counts. indent=2 makes
# the raw JSON several times larger; gzip absorbs most of
# it. Returns True/False, and the caller ignores both:
# every failure is logged here and swallowed, so a broken
# export never stops the loop.
#
# Used by:
#   - __main__ (below)
############################################################

def export_data_to_file(db_cursor):
    try:
        # STEP 1: collect — three full-table reads, ~5 s today
        # ====================================================
        channel_announcements = get_data_channel_announcements(db_cursor)
        node_addresses = get_data_node_addresses(db_cursor)
        node_announcements = get_data_node_announcements(db_cursor)


        # STEP 2: the envelope — meta first so a consumer can
        # read the header without parsing 30 MB of data
        # ===================================================
        fullJson = {
            "meta": {
                "timestamp": int(datetime.now().timestamp()),
                "exported_at": datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
                "lnd_dbreader_version": LND_DBREADER_VERSION,
                "summary": {
                    "total_channel_announcements": len(channel_announcements),
                    "total_node_addresses": len(node_addresses),
                    "total_node_announcements": len(node_announcements)
                }
            },
            "data": {
                "channel_announcements": channel_announcements,
                "node_addresses": node_addresses,
                "node_announcements": node_announcements,
            }
        }


        # STEP 3: write to _tmp, then rename over the live file
        # — the ~20 s of gzip'ing never shows a partial file
        # =====================================================
        output_file = "/DATA/lnd-dbreader.json.gz"
        with gzip.open(output_file+"_tmp", 'wt', encoding='utf-8') as f:
            json.dump(fullJson, f, indent=2)
        os.rename(output_file+"_tmp", output_file)


        log_with_timestamp(f"Data successfully exported to {output_file}")
        log_with_timestamp(f"Summary: {len(channel_announcements)} channels, {len(node_addresses)} addresses, {len(node_announcements)} nodes")
        
        return True
    except Exception as e:
        log_with_timestamp(f"Error exporting data: {e}")
        return False








############################################################
# __main__
############################################################
#
# Wait, then export every 12 hours forever. The outer
# except is the only way out of the loop: an error opening
# or closing the connection (export errors are swallowed
# inside export_data_to_file) ends the process, and docker's
# restart policy brings it back.
#
# Used by:
#   - Dockerfile — CMD ["python3", "-u", "main.py"], the
#     lnd-dbreader-exporter compose service
############################################################

if __name__ == "__main__":
    # STEP 1: give dbreader's first sync a head start, so the
    # first file is not cut from tables still being filled
    # =======================================================
    log_with_timestamp("Starting LND data exporter...")
    log_with_timestamp("Waiting 3 minutes before starting data export to file...")
    sleep(180)
    

    # STEP 2: the loop. A fresh connection per iteration,
    # closed again before the sleep — an idle connection
    # would outlive MySQL's 8 h wait_timeout (see the header)
    # =======================================================
    try:
        while True:
            with get_db_connection() as db_conn:
                with db_conn.cursor() as db_cursor:

                    log_with_timestamp("Exporting data...")
                    export_data_to_file(db_cursor)

            log_with_timestamp("Sleeping for 12 hours...")
            sleep(12*3600)

    except Exception as e:
        log_with_timestamp(f"Error: {e}")
