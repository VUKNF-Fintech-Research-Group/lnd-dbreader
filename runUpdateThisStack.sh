#!/bin/bash

mkdir -p lnd
mkdir -p ./lnd/lnd
mkdir -p mysql/data

sudo docker-compose down
sudo docker-compose up -d --build
