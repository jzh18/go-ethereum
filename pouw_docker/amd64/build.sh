#!/bin/bash

docker build -t hfzhang6/pouw-geth-client:ubuntu-20.04-amd64 . --no-cache
docker push hfzhang6/pouw-geth-client:ubuntu-20.04-amd64