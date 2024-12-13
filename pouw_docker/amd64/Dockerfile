FROM ubuntu:20.04
# install build-essential
RUN apt-get update && apt-get install -y build-essential

# Install golang
RUN apt-get update && apt-get install -y wget
RUN wget https://go.dev/dl/go1.19.4.linux-amd64.tar.gz
RUN tar -C /usr/local -xzf go1.19.4.linux-amd64.tar.gz
ENV PATH=$PATH:/usr/local/go/bin

# Build go-ethereum pouw
RUN apt-get update && apt-get install -y git
RUN git clone https://github.com/jzh18/go-ethereum.git
WORKDIR go-ethereum
RUN git checkout pouw-dev
RUN git pull
RUN make geth
RUN mv build/bin/geth /usr/local/bin/geth
RUN geth init /go-ethereum/pouw_docker/genesis.json
