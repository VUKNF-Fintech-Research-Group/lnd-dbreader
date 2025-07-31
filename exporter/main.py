import os
import requests
from time import sleep
import mysql.connector
from mysql.connector.cursor import MySQLCursorDict
import json
import gzip
from datetime import datetime



LND_DBREADER_VERSION = "1.0"

DEFAULT_DB_HOST = 'lnd-dbreader-mysql'
DEFAULT_DB_NAME = 'lnd_data'
DEFAULT_DB_USER = 'lnd_data'
DEFAULT_DB_PASSWORD = 'lnd_data'




def get_db_connection():
    conn = mysql.connector.connect(
        host=os.getenv('DB_HOST', DEFAULT_DB_HOST),
        database=os.getenv('DB_NAME', DEFAULT_DB_NAME),
        user=os.getenv('DB_USER', DEFAULT_DB_USER),
        password=os.getenv('DB_PASSWORD', DEFAULT_DB_PASSWORD)
    )
    conn.cursor_class = MySQLCursorDict
    return conn




def log_with_timestamp(message):
    """Print message with timestamp"""
    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    print(f"[{timestamp}] {message}")



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

    # Convert query results (tuple rows) into a list of dictionaries using column names
    columns = [col[0] for col in cursor.description]
    data_list = []
    for row in rows:
        row_dict = dict(zip(columns, row))
        data_list.append(row_dict)

    log_with_timestamp(f"Collected {len(data_list)} channel announcements")
    return data_list



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


    # Convert query results to list of dictionaries
    columns = [col[0] for col in cursor.description]
    data_list = [dict(zip(columns, row)) for row in rows]

    log_with_timestamp(f"Collected {len(data_list)} node addresses")
    return data_list



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


    # Convert query results to list of dictionaries
    columns = [col[0] for col in cursor.description]
    data_list = [dict(zip(columns, row)) for row in rows]

    log_with_timestamp(f"Collected {len(data_list)} node announcements")
    return data_list



def export_data_to_file(db_cursor):
    """Export all data and save as compressed JSON file"""
    try:
        # Collect all data
        channel_announcements = get_data_channel_announcements(db_cursor)
        node_addresses = get_data_node_addresses(db_cursor)
        node_announcements = get_data_node_announcements(db_cursor)

        # Create combined data structure
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

        # Save as compressed JSON file
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




if __name__ == "__main__":
    log_with_timestamp("Starting LND data exporter...")
    log_with_timestamp("Waiting 3 minutes before starting data export to file...")
    sleep(180)
    
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
