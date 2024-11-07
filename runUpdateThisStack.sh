#!/bin/bash

mkdir -p lnd
mkdir -p ./lnd/lnd
mkdir -p mysql

sudo docker-compose down
sudo docker-compose up -d --build
