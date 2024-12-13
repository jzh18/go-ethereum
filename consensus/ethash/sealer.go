// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package ethash

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/types"
)

const (
	// staleThreshold is the maximum depth of the acceptable stale but valid ethash solution.
	staleThreshold = 7
)

var (
	errNoMiningWork      = errors.New("no mining work available yet")
	errInvalidSealResult = errors.New("invalid or stale proof-of-work solution")
)

// data: "1,2,3A4,5,6A7,8,9"
func stringToMatrix(data string) [][]int {
	var matrix [][]int
	rows := strings.Split(data, "A")
	for i := 0; i < len(rows); i++ {
		cols := strings.Split(rows[i], ",")
		var row []int
		for j := 0; j < len(cols); j++ {
			num, err := strconv.Atoi(cols[j])
			if err != nil {
				panic("Invalid matrix data")
			}
			row = append(row, num)
		}
		matrix = append(matrix, row)
	}
	return matrix
}

func matrixToString(matrix [][]int) string {
	var data string
	for i := 0; i < len(matrix); i++ {
		for j := 0; j < len(matrix[0]); j++ {
			data += string(strconv.Itoa(matrix[i][j]))
			if j != len(matrix[0])-1 {
				data += ","
			}
		}
		if i != len(matrix)-1 {
			data += "\n"
		}
	}
	return data
}

func matrixMultiple(matrix_a [][]int, matrix_b [][]int) [][]int {
	fmt.Println("matrix_a:", matrix_a)
	fmt.Println("matrix_b:", matrix_b)
	if len(matrix_a[0]) != len(matrix_b) {
		panic("Matrix A's column number is not equal to Matrix B's row number")
	}
	var matrix [][]int
	for i := 0; i < len(matrix_a); i++ {
		var row []int
		for j := 0; j < len(matrix_b[0]); j++ {
			var sum int
			for k := 0; k < len(matrix_a[0]); k++ {
				sum += matrix_a[i][k] * matrix_b[k][j]
			}
			row = append(row, sum)
		}
		matrix = append(matrix, row)
	}
	return matrix
}

// Seal implements consensus.Engine, attempting to find a nonce that satisfies
// the block's difficulty requirements.
func (ethash *Ethash) Seal(chain consensus.ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	// If we're running a fake PoW, simply return a 0 nonce immediately
	if ethash.config.PowMode == ModeFake || ethash.config.PowMode == ModeFullFake || len(block.Transactions()) == 0 || block.Transactions() == nil {
		header := block.Header()
		header.Nonce, header.MixDigest = types.BlockNonce{}, common.Hash{}
		select {
		case results <- block.WithSeal(header):
		default:
			ethash.config.Log.Warn("Sealing result is not read by miner", "mode", "fake", "sealhash", ethash.SealHash(block.Header()))
		}
		return nil
	}
	// If we're running a shared PoW, delegate sealing to it
	if ethash.shared != nil {
		return ethash.shared.Seal(chain, block, results, stop)
	}
	// Create a runner and the multiple search threads it directs
	abort := make(chan struct{})

	ethash.lock.Lock()
	threads := ethash.threads
	if ethash.rand == nil {
		seed, err := crand.Int(crand.Reader, big.NewInt(math.MaxInt64))
		if err != nil {
			ethash.lock.Unlock()
			return err
		}
		ethash.rand = rand.New(rand.NewSource(seed.Int64()))
	}
	ethash.lock.Unlock()
	if threads == 0 {
		threads = runtime.NumCPU()
	}
	if threads < 0 {
		threads = 0 // Allows disabling local mining without extra logic around local/remote
	}
	// Push new work to remote sealer
	if ethash.remote != nil {
		ethash.remote.workCh <- &sealTask{block: block, results: results}
	}
	var (
		pend   sync.WaitGroup
		locals = make(chan *types.Block)
	)
	for i := 0; i < threads; i++ {
		pend.Add(1)
		go func(id int, nonce uint64) {
			defer pend.Done()
			ethash.mine(block, id, nonce, abort, locals)
		}(i, uint64(ethash.rand.Int63()))
	}
	// Wait until sealing is terminated or a nonce is found
	go func() {
		var result *types.Block
		select {
		case <-stop:
			// Outside abort, stop all miner threads
			close(abort)
		case result = <-locals:
			// One of the threads found a block, abort all others
			select {
			case results <- result:
			default:
				ethash.config.Log.Warn("Sealing result is not read by miner", "mode", "local", "sealhash", ethash.SealHash(block.Header()))
			}
			close(abort)
		case <-ethash.update:
			// Thread count was changed on user request, restart
			close(abort)
			if err := ethash.Seal(chain, block, results, stop); err != nil {
				ethash.config.Log.Error("Failed to restart sealing after update", "err", err)
			}
		}
		// Wait for all miners to terminate and return the block
		pend.Wait()
	}()
	return nil
}

// mine is the actual proof-of-work miner that searches for a nonce starting from
// seed that results in correct final block difficulty.
func (ethash *Ethash) mine(block *types.Block, id int, seed uint64, abort chan struct{}, found chan *types.Block) {
	//for _, tx := range block.Transactions() {
	//	ethash.config.Log.Warn("tx data:", string(tx.Data()))
	//	ethash.config.Log.Warn("tx hash:", tx.Hash().String())
	//}

	// Extract some data from the header
	var (
		header  = block.Header()
		hash    = ethash.SealHash(header).Bytes()
		target  = new(big.Int).Div(two256, header.Difficulty)
		number  = header.Number.Uint64()
		dataset = ethash.dataset(number, false)
	)
	// Start generating random nonces until we abort or find a good one
	var (
		attempts  = int64(0)
		nonce     = seed
		powBuffer = new(big.Int)
	)
	logger := ethash.config.Log.New("miner", id)
	logger.Trace("Started ethash search for new nonces", "seed", seed)

	// fixme: do not support multiple transactions in a block yet
	if len(block.Transactions()) > 1 {
		panic("Do not support multiple transactions in a block yet")
	}
	fmt.Println("len(block.Transactions()):", len(block.Transactions()))
	if len(block.Transactions()) == 1 && (block.Transactions()[0].Data() != nil || len(block.Transactions()[0].Data()) == 0) {
		logger.Info("Performing proof of useful work", "mode", "pouw")
		// Seal and return a block (if still needed)
	search_useful:
		for {
			select {
			case <-abort:
				// Mining terminated, update stats and abort
				logger.Trace("Ethash nonce search aborted", "attempts", nonce-seed)
				ethash.hashrate.Mark(attempts)
				break search_useful

			default:
				tx := block.Transactions()[0]
				ethash.config.Log.Warn("Performing proof of useful work", "mode", "pouw", "run", tx.Hash().String())
				matrix_strs := strings.Split(string(tx.Data()), "*")
				// split by '*'
				matrix_a_str := matrix_strs[0]
				matrix_b_str := matrix_strs[1]
				matrix_a := stringToMatrix(matrix_a_str)
				matrix_b := stringToMatrix(matrix_b_str)
				matrix_c := matrixMultiple(matrix_a, matrix_b) // shape should be m*n == 8
				fmt.Println("matrix_c:", matrix_c)
				var nonce [8]byte
				count := 0
				for count < 8 {
					for j := 0; j < len(matrix_c); j++ {
						for k := 0; k < len(matrix_c[0]); k++ {
							nonce[count] = byte(matrix_c[j][k])
							count++
						}
					}
				}

				//hash = ethash.SealHash(header).Bytes()

				//digest, _ := hashimotoFull(dataset.dataset, hash, nonce)

				// Correct nonce found, create a new header with it
				//header.Extra = []byte(matrixToString(matrix_c))
				header = types.CopyHeader(header)
				header.Nonce = nonce
				//header.MixDigest = common.BytesToHash(digest)

				select {
				case found <- block.WithSeal(header):
					logger.Info("Ethash nonce found and reported", "nonce", nonce)
				case <-abort:
					logger.Info("Ethash nonce found but discarded", "nonce", nonce)
				}
				break search_useful
			}
		}
	} else {

		logger.Info("Performing proof of work", "mode", "pow")
	search:
		for {
			select {
			case <-abort:
				// Mining terminated, update stats and abort
				logger.Trace("Ethash nonce search aborted", "attempts", nonce-seed)
				ethash.hashrate.Mark(attempts)
				break search

			default:
				// We don't have to update hash rate on every nonce, so update after after 2^X nonces
				attempts++
				if (attempts % (1 << 15)) == 0 {
					ethash.hashrate.Mark(attempts)
					attempts = 0
				}
				// Compute the PoW value of this nonce
				digest, result := hashimotoFull(dataset.dataset, hash, nonce)
				if powBuffer.SetBytes(result).Cmp(target) <= 0 {
					// Correct nonce found, create a new header with it
					header = types.CopyHeader(header)
					header.Nonce = types.EncodeNonce(nonce)
					header.MixDigest = common.BytesToHash(digest)

					// Seal and return a block (if still needed)
					select {
					case found <- block.WithSeal(header):
						logger.Trace("Ethash nonce found and reported", "attempts", nonce-seed, "nonce", nonce)
					case <-abort:
						logger.Trace("Ethash nonce found but discarded", "attempts", nonce-seed, "nonce", nonce)
					}
					break search
				}
				nonce++
			}
		}
	}
	// Datasets are unmapped in a finalizer. Ensure that the dataset stays live
	// during sealing so it's not unmapped while being read.
	runtime.KeepAlive(dataset)
}

// This is the timeout for HTTP requests to notify external miners.
const remoteSealerTimeout = 1 * time.Second

type remoteSealer struct {
	works        map[common.Hash]*types.Block
	rates        map[common.Hash]hashrate
	currentBlock *types.Block
	currentWork  [4]string
	notifyCtx    context.Context
	cancelNotify context.CancelFunc // cancels all notification requests
	reqWG        sync.WaitGroup     // tracks notification request goroutines

	ethash       *Ethash
	noverify     bool
	notifyURLs   []string
	results      chan<- *types.Block
	workCh       chan *sealTask   // Notification channel to push new work and relative result channel to remote sealer
	fetchWorkCh  chan *sealWork   // Channel used for remote sealer to fetch mining work
	submitWorkCh chan *mineResult // Channel used for remote sealer to submit their mining result
	fetchRateCh  chan chan uint64 // Channel used to gather submitted hash rate for local or remote sealer.
	submitRateCh chan *hashrate   // Channel used for remote sealer to submit their mining hashrate
	requestExit  chan struct{}
	exitCh       chan struct{}
}

// sealTask wraps a seal block with relative result channel for remote sealer thread.
type sealTask struct {
	block   *types.Block
	results chan<- *types.Block
}

// mineResult wraps the pow solution parameters for the specified block.
type mineResult struct {
	nonce     types.BlockNonce
	mixDigest common.Hash
	hash      common.Hash

	errc chan error
}

// hashrate wraps the hash rate submitted by the remote sealer.
type hashrate struct {
	id   common.Hash
	ping time.Time
	rate uint64

	done chan struct{}
}

// sealWork wraps a seal work package for remote sealer.
type sealWork struct {
	errc chan error
	res  chan [4]string
}

func startRemoteSealer(ethash *Ethash, urls []string, noverify bool) *remoteSealer {
	ctx, cancel := context.WithCancel(context.Background())
	s := &remoteSealer{
		ethash:       ethash,
		noverify:     noverify,
		notifyURLs:   urls,
		notifyCtx:    ctx,
		cancelNotify: cancel,
		works:        make(map[common.Hash]*types.Block),
		rates:        make(map[common.Hash]hashrate),
		workCh:       make(chan *sealTask),
		fetchWorkCh:  make(chan *sealWork),
		submitWorkCh: make(chan *mineResult),
		fetchRateCh:  make(chan chan uint64),
		submitRateCh: make(chan *hashrate),
		requestExit:  make(chan struct{}),
		exitCh:       make(chan struct{}),
	}
	go s.loop()
	return s
}

func (s *remoteSealer) loop() {
	defer func() {
		s.ethash.config.Log.Trace("Ethash remote sealer is exiting")
		s.cancelNotify()
		s.reqWG.Wait()
		close(s.exitCh)
	}()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case work := <-s.workCh:
			// Update current work with new received block.
			// Note same work can be past twice, happens when changing CPU threads.
			s.results = work.results
			s.makeWork(work.block)
			s.notifyWork()

		case work := <-s.fetchWorkCh:
			// Return current mining work to remote miner.
			if s.currentBlock == nil {
				work.errc <- errNoMiningWork
			} else {
				work.res <- s.currentWork
			}

		case result := <-s.submitWorkCh:
			// Verify submitted PoW solution based on maintained mining blocks.
			if s.submitWork(result.nonce, result.mixDigest, result.hash) {
				result.errc <- nil
			} else {
				result.errc <- errInvalidSealResult
			}

		case result := <-s.submitRateCh:
			// Trace remote sealer's hash rate by submitted value.
			s.rates[result.id] = hashrate{rate: result.rate, ping: time.Now()}
			close(result.done)

		case req := <-s.fetchRateCh:
			// Gather all hash rate submitted by remote sealer.
			var total uint64
			for _, rate := range s.rates {
				// this could overflow
				total += rate.rate
			}
			req <- total

		case <-ticker.C:
			// Clear stale submitted hash rate.
			for id, rate := range s.rates {
				if time.Since(rate.ping) > 10*time.Second {
					delete(s.rates, id)
				}
			}
			// Clear stale pending blocks
			if s.currentBlock != nil {
				for hash, block := range s.works {
					if block.NumberU64()+staleThreshold <= s.currentBlock.NumberU64() {
						delete(s.works, hash)
					}
				}
			}

		case <-s.requestExit:
			return
		}
	}
}

// makeWork creates a work package for external miner.
//
// The work package consists of 3 strings:
//
//	result[0], 32 bytes hex encoded current block header pow-hash
//	result[1], 32 bytes hex encoded seed hash used for DAG
//	result[2], 32 bytes hex encoded boundary condition ("target"), 2^256/difficulty
//	result[3], hex encoded block number
func (s *remoteSealer) makeWork(block *types.Block) {
	hash := s.ethash.SealHash(block.Header())
	s.currentWork[0] = hash.Hex()
	s.currentWork[1] = common.BytesToHash(SeedHash(block.NumberU64())).Hex()
	s.currentWork[2] = common.BytesToHash(new(big.Int).Div(two256, block.Difficulty()).Bytes()).Hex()
	s.currentWork[3] = hexutil.EncodeBig(block.Number())

	// Trace the seal work fetched by remote sealer.
	s.currentBlock = block
	s.works[hash] = block
}

// notifyWork notifies all the specified mining endpoints of the availability of
// new work to be processed.
func (s *remoteSealer) notifyWork() {
	work := s.currentWork

	// Encode the JSON payload of the notification. When NotifyFull is set,
	// this is the complete block header, otherwise it is a JSON array.
	var blob []byte
	if s.ethash.config.NotifyFull {
		blob, _ = json.Marshal(s.currentBlock.Header())
	} else {
		blob, _ = json.Marshal(work)
	}

	s.reqWG.Add(len(s.notifyURLs))
	for _, url := range s.notifyURLs {
		go s.sendNotification(s.notifyCtx, url, blob, work)
	}
}

func (s *remoteSealer) sendNotification(ctx context.Context, url string, json []byte, work [4]string) {
	defer s.reqWG.Done()

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(json))
	if err != nil {
		s.ethash.config.Log.Warn("Can't create remote miner notification", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, remoteSealerTimeout)
	defer cancel()
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.ethash.config.Log.Warn("Failed to notify remote miner", "err", err)
	} else {
		s.ethash.config.Log.Trace("Notified remote miner", "miner", url, "hash", work[0], "target", work[2])
		resp.Body.Close()
	}
}

// submitWork verifies the submitted pow solution, returning
// whether the solution was accepted or not (not can be both a bad pow as well as
// any other error, like no pending work or stale mining result).
func (s *remoteSealer) submitWork(nonce types.BlockNonce, mixDigest common.Hash, sealhash common.Hash) bool {
	if s.currentBlock == nil {
		s.ethash.config.Log.Error("Pending work without block", "sealhash", sealhash)
		return false
	}
	// Make sure the work submitted is present
	block := s.works[sealhash]
	if block == nil {
		s.ethash.config.Log.Warn("Work submitted but none pending", "sealhash", sealhash, "curnumber", s.currentBlock.NumberU64())
		return false
	}
	// Verify the correctness of submitted result.
	header := block.Header()
	header.Nonce = nonce
	header.MixDigest = mixDigest

	start := time.Now()
	if !s.noverify {
		if err := s.ethash.verifySeal(nil, header, true); err != nil {
			s.ethash.config.Log.Warn("Invalid proof-of-work submitted", "sealhash", sealhash, "elapsed", common.PrettyDuration(time.Since(start)), "err", err)
			return false
		}
	}
	// Make sure the result channel is assigned.
	if s.results == nil {
		s.ethash.config.Log.Warn("Ethash result channel is empty, submitted mining result is rejected")
		return false
	}
	s.ethash.config.Log.Trace("Verified correct proof-of-work", "sealhash", sealhash, "elapsed", common.PrettyDuration(time.Since(start)))

	// Solutions seems to be valid, return to the miner and notify acceptance.
	solution := block.WithSeal(header)

	// The submitted solution is within the scope of acceptance.
	if solution.NumberU64()+staleThreshold > s.currentBlock.NumberU64() {
		select {
		case s.results <- solution:
			s.ethash.config.Log.Debug("Work submitted is acceptable", "number", solution.NumberU64(), "sealhash", sealhash, "hash", solution.Hash())
			return true
		default:
			s.ethash.config.Log.Warn("Sealing result is not read by miner", "mode", "remote", "sealhash", sealhash)
			return false
		}
	}
	// The submitted block is too old to accept, drop it.
	s.ethash.config.Log.Warn("Work submitted is too old", "number", solution.NumberU64(), "sealhash", sealhash, "hash", solution.Hash())
	return false
}
