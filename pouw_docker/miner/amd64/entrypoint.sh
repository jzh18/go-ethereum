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
  --bootnodes="enode://377701aa8eff07f05978661f9a9c95e0a0f166cd65821b34e20fb8ef2c7fe1bf300addb5dadf747cc20a7c3352e71b1f2da9c348d04a9e747b5795e0187a55cb@13.50.117.77:30303" \
  --mine \
  --miner.threads=1 \
  --miner.etherbase=${MINER_ADDRESS}