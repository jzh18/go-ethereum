First, you need to create a bootnode.
If you're on Mac M1, go to ./arm64 and
```shell
docker build -t pouw-geth-client .
docker run -it --name bootnode pouw-geth-client bash 
```
Inside it, run
```shell
echo "genesis_account" > /tmp/passwd
./build/bin/geth account new -password /tmp/passwd
```
Copy the address of the account and replace the first address it in the genesis.json file.
Then, run
```shell
echo "miner_account" > /tmp/passwd
./build/bin/geth account new -password /tmp/passwd
```
Copy the address of the account and replace the second address it in the genesis.json file.

Copy the genesis.json file to the container
```shell
docker cp genesis.json bootnode:/tmp/genesis.json
```


Then, run
```shell
```

Then initialize the genesis block
```shell
./build/bin/geth init /tmp/genesis.json
```
Exit the container and commit the changes
```shell
docker commit bootnode pouw-geth-client
```
