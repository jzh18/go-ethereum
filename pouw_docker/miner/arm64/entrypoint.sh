#!/bin/bash

FILE=nodekey
if [ -f "$FILE" ]; then
    echo "$FILE exists."
else
    echo "$FILE does not exist."
    bootnode -genkey nodekey
fi

geth --allow-insecure-unlock --networkid=6666 --http --http.port=8545 \
  --http.addr=0.0.0.0 --http.api=eth,web3,net,admin,personal --http.corsdomain=* \
  --syncmode=full --nodekey nodekey \
  --bootnodes="enode://5285ae5747310893d3d111ab19c46f8031ab584757bdf970cfa0957e98c118e37cc982aa6219b878ae2d07432924f1ed9276307422cc8768d43aef1fdc033445@13.50.117.77:30303" \
  --mine \
  --miner.threads=1 \
  --miner.etherbase=${MINER_ADDRESS}