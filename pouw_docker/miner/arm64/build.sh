#!/bin/bash

docker build -t hfzhang6/pouw-geth-client:miner-mac-m1 . --no-cache
docker push hfzhang6/pouw-geth-client:miner-mac-m1