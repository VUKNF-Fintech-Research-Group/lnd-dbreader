import mysql.connector
from pyzabbix import ZabbixMetric, ZabbixSender
import sys
from time import sleep
import os

# MySQL connection details
mysql_config = {
    'host': os.getenv('MYSQL_HOST'),
    'user': os.getenv('MYSQL_USER'),
    'password': os.getenv('MYSQL_PASSWORD'),
    'database': os.getenv('MYSQL_DATABASE')
}

# Zabbix server details
zabbix_server = os.getenv('ZABBIX_SERVER')
zabbix_port = int(os.getenv('ZABBIX_PORT', 10051))


# Zabbix Host
zabbix_monitoring_host = os.getenv('ZABBIX_MONITORING_HOST')


# Tables to check
tables_to_check = os.getenv('TABLES_TO_CHECK').split(',')



def get_row_count(cursor, table):
    cursor.execute(f"SELECT COUNT(*) FROM {table}")
    return cursor.fetchone()[0]



def main():
    try:
        # Connect to MySQL
        conn = mysql.connector.connect(**mysql_config)
        cursor = conn.cursor()

        # Initialize Zabbix sender
        zabbix_sender = ZabbixSender(zabbix_server, zabbix_port)

        # Prepare Zabbix packet
        zabbix_packet = []

        for table in tables_to_check:
            row_count = get_row_count(cursor, table)
            print(f"Table {table}: {row_count} rows")
            
            # Prepare Zabbix metric
            zabbix_packet.append(ZabbixMetric(zabbix_monitoring_host, table, row_count))

        # Send data to Zabbix
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



if __name__ == "__main__":
    sleep(15)

    while True:
        main()
        sleep(1800)