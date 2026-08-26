#!/bin/bash

# Prepare data directories
mkdir -p ./_DATA/lnd
mkdir -p ./_DATA/mysql
mkdir -p ./_DATA/exporter
sudo chown -R 1000:1000 ./_DATA



# Run the stack
sudo docker-compose down
sudo docker-compose up -d --build
