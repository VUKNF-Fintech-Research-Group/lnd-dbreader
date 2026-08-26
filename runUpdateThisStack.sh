#!/bin/bash

# STEP 1: Create necessary files and directories
# ==============================================
mkdir -p ./_DATA/lnd
mkdir -p ./_DATA/mysql
mkdir -p ./_DATA/exporter
sudo chown -R 1000:1000 ./_DATA



# STEP 2: Run the stack
# =====================
sudo docker-compose down
sudo docker-compose up -d --build
