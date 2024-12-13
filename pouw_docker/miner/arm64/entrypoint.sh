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
  --bootnodes="enode://9d2d0d7b8636b391ebd8a4b0f1450149fd088dd2d9f87ee3f3103e3ee159eb7981c82d1f680f1d6e5b6a9c92e3c317f48c40088a92634a866f599027b40d262d@13.50.117.77:30303" \
  --mine \
  --miner.threads=1 \
  --miner.etherbase=${MINER_ADDRESS}