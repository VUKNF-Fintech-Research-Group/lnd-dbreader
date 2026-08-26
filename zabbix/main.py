############################################################
#  [*] zabbix — table row counts → Zabbix, every 30 min
#
#  Optional monitoring sidecar (the sample compose ships it
#  commented out). Every half hour it counts the rows of
#  each table in TABLES_TO_CHECK and pushes the numbers to
#  the Zabbix server as TRAPPER items on host
#  ZABBIX_MONITORING_HOST — the item KEY is the table name
#  itself, so a table added to TABLES_TO_CHECK needs a
#  matching trapper item on the Zabbix side or the server
#  counts it as "failed" in the response. A count that
#  stops growing is the signal that dbreader has stalled.
#
#  Environment (all from compose, no defaults except the
#  port):
#    MYSQL_HOST / MYSQL_USER / MYSQL_PASSWORD /
#    MYSQL_DATABASE         — the dbreader database
#    ZABBIX_SERVER          — Zabbix server / proxy address
#    ZABBIX_PORT            — trapper port (10051)
#    ZABBIX_MONITORING_HOST — the Zabbix host the items
#                             live on (LND-DBREADER)
#    TABLES_TO_CHECK        — comma list of table names;
#                             compose lists the channel and
#                             node announcement tables,
#                             not node_addresses
#
#  Nothing is retried faster than the 30-minute cadence:
#  a MySQL or Zabbix failure is printed and the next attempt
#  is the next tick. Runs unprivileged (user 1000) on a
#  read-only filesystem; the image also carries pymodbus,
#  which nothing here imports.
############################################################


import mysql.connector
from pyzabbix import ZabbixMetric, ZabbixSender
import sys
from time import sleep
import os








# No defaults: an unset variable becomes None, connect()
# raises, and main() logs it every 30 minutes until compose
# is fixed
mysql_config = {
    'host': os.getenv('MYSQL_HOST'),
    'user': os.getenv('MYSQL_USER'),
    'password': os.getenv('MYSQL_PASSWORD'),
    'database': os.getenv('MYSQL_DATABASE')
}

# Where the metrics go; 10051 is Zabbix's trapper port
zabbix_server = os.getenv('ZABBIX_SERVER')
zabbix_port = int(os.getenv('ZABBIX_PORT', 10051))


# The Zabbix host that owns the trapper items
zabbix_monitoring_host = os.getenv('ZABBIX_MONITORING_HOST')


# Split at import time — with TABLES_TO_CHECK unset this
# line raises AttributeError before main() ever runs, and
# the container sits in a restart loop
tables_to_check = os.getenv('TABLES_TO_CHECK').split(',')








############################################################
# get_row_count
############################################################
#
# SELECT COUNT(*) on one table. The name is interpolated
# straight into the SQL — it comes from compose, not from
# users. On InnoDB this is a full index scan, sub-second at
# today's ~500k rows.
#
# Used by:
#   - main (below) — once per table per tick
############################################################

def get_row_count(cursor, table):
    cursor.execute(f"SELECT COUNT(*) FROM {table}")
    return cursor.fetchone()[0]








############################################################
# main
############################################################
#
# One tick: count every table, send one packet. Every error
# is printed and swallowed so the loop in __main__ keeps
# ticking. The finally block closes MySQL only when connect
# succeeded (`conn` exists) and the server still answers a
# ping — is_connected() sends one.
#
# Used by:
#   - __main__ (below)
############################################################

def main():
    try:
        # STEP 1: MySQL — fails here when compose left a
        # MYSQL_* variable unset
        # ==============================================
        conn = mysql.connector.connect(**mysql_config)
        cursor = conn.cursor()


        # STEP 2: one metric per table, item key = table name
        # ===================================================
        zabbix_sender = ZabbixSender(zabbix_server, zabbix_port)
        zabbix_packet = []
        for table in tables_to_check:
            row_count = get_row_count(cursor, table)
            print(f"Table {table}: {row_count} rows")
            
            zabbix_packet.append(ZabbixMetric(zabbix_monitoring_host, table, row_count))


        # STEP 3: one trapper packet for all tables; the
        # response says how many the server processed vs
        # failed (unknown item key → failed, not an error)
        # ================================================
        result = zabbix_sender.send(zabbix_packet)
        print(f"Sent to Zabbix: {result}")

    except mysql.connector.Error as err:
        print(f"MySQL Error: {err}")
    except Exception as ex:
        print(f"Error: {ex}")
    finally:
        if 'conn' in locals() and conn.is_connected():
            cursor.close()
            conn.close()








############################################################
# __main__
############################################################
#
# 15 seconds for MySQL to come up, then a tick every 30
# minutes forever — main() never raises, so nothing ends
# this loop but docker.
#
# Used by:
#   - Dockerfile — CMD ["python3", "-u", "main.py"], the
#     lnd-dbreader-zabbix compose service
############################################################

if __name__ == "__main__":
    sleep(15)

    while True:
        main()
        sleep(1800)
