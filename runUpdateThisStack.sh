#!/bin/bash

# Prepare LND data directory
mkdir -p ./lnd/lnd

# Prepare MySQL data directory
mkdir -p mysql/data

# Prepare DATA directory
mkdir -p ./DATA



# Run the stack
sudo docker-compose down
sudo docker-compose up -d --build
