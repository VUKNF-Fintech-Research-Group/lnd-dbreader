FROM python:3.12.1

WORKDIR /app

# Base
RUN pip install --no-cache-dir              \
    "requests==2.32.3"                      \
    "mysql-connector-python==8.4.0"

# Copy the main python script
COPY main.py /app/

CMD ["python3", "-u", "main.py"]