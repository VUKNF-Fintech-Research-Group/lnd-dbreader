FROM python:3.12-alpine

WORKDIR /app

RUN pip install --no-cache-dir py-zabbix==1.1.7  pymodbus==3.6.2 mysql-connector-python==9.0.0

COPY ./main.py /app

USER 1000
CMD ["python3", "-u", "main.py"]
