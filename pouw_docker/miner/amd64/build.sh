#!/bin/bash

docker build -t hfzhang6/pouw-geth-client:miner . --no-cache
docker push hfzhang6/pouw-geth-client:miner-amd64