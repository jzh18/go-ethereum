#!/bin/bash

docker build -t hfzhang6/pouw-geth-client:bootnode-amd64 . --no-cache
docker push hfzhang6/pouw-geth-client:bootnode-amd64