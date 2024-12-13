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
  --bootnodes="enode://c8dd7e533969999eb7626b1aaeebddc305bc7ba614b6bce1177e54cc9997432ae2c03ed9b8fe5dcd181966c1eafff612daa88875e8744f0df81f5c2a1c54b849@13.50.117.77:30303" \
  --mine \
  --miner.threads=1 \
  --miner.etherbase=${MINER_ADDRESS}