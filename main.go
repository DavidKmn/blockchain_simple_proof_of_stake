package main

import (
	"os"
	"github.com/davecgh/go-spew/spew"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

type Block struct {
	Index     int
	Timestamp string
	BPM       int
	Hash      string
	PrevHash  string
	Validator string
}

var Blockchain []Block
var tempBlocks []Block

// candidateBlocks handles incoming blocks for validation
var candidateBlocks = make(chan Block)

// announcements broadcasts winning validator to all nodes
var announcements = make(chan string)

var mutex = &sync.Mutex{}

// validators keeps track of open validators and balances
var validators = make(map[string]int)

func main() {
	err := godotenv.Load()

	if err != nil {
		log.Fatal(err)
	}

	// create genesis block
	t := time.Now()
	genesisBlock := Block{}
	genesisBlock = Block{0, t.String(), 0, calculateBlockHash(genesisBlock), "", ""}
	spew.Dump(genesisBlock)
	Blockchain = append(Blockchain, genesisBlock)

	// start TCP and serve TCP server
	server, err := net.Listen("tcp", ":"+os.Getenv("ADDR"))
	if err != nil {
		log.Fatal(err)
	}
	defer server.Close()

	go func() {
		for candidate := range candidateBlocks {
			mutex.Lock()
			tempBlocks = append(tempBlocks, candidate)
			mutex.Unlock()
		}
	}()

	go func() {
		for {
			pickWinner()
		}
	}()

	for {
		conn, err := server.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go handleConnection(conn)
	}
}

func calculateHash(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	hashed := h.Sum(nil)
	return hex.EncodeToString(hashed)
}

func calculateBlockHash(block Block) string {
	record := string(block.Index) + block.Timestamp + string(block.BPM) + block.PrevHash
	return calculateHash(record)
}

func generateBlock(oldBlock Block, BPM int, address string) (Block, error) {
	var newBlock Block

	t := time.Now()

	newBlock.Index = oldBlock.Index + 1
	newBlock.BPM = BPM
	newBlock.PrevHash = oldBlock.Hash
	newBlock.Timestamp = t.String()
	newBlock.Hash = calculateBlockHash(newBlock)
	newBlock.Validator = address

	return newBlock, nil
}

func isBlockValid(newBlock Block, oldBlock Block) bool {
	if newBlock.Index-1 != oldBlock.Index {
		return false
	}

	if newBlock.PrevHash != oldBlock.Hash {
		return false
	}

	if calculateBlockHash(newBlock) != newBlock.Hash {
		return false
	}

	return true
}

func handleConnection(conn net.Conn) {
	defer conn.Close()

	go func() {
		for {
			msg := <-announcements
			io.WriteString(conn, msg)
		}
	}()

	var address string

	// allow user to allocate number of tokens to stake
	// the greater the number of tokens, the greater chance to forging a new block
	io.WriteString(conn, "Enter token Balance: ")
	scanBalance := bufio.NewScanner(conn)
	for scanBalance.Scan() {
		balance, err := strconv.Atoi(scanBalance.Text())
		if err != nil {
			log.Printf("%v not a number: %v", scanBalance.Text(), err)
			return
		}

		t := time.Now()
		address = calculateHash(t.String())
		validators[address] = balance
		fmt.Println(validators)
		break
	}

	io.WriteString(conn, "\nEnter a new BPM: ")

	scanBPM := bufio.NewScanner(conn)

	go func() {
		for {
			// take in BPM from stdin and add it to blockchain after conducting necessary validation
			for scanBPM.Scan() {
				bpm, err := strconv.Atoi(scanBPM.Text())
				// if malicious party tries to mutate the chain with a bad input, delete them as a validator and they lose their staked tokens
				if err != nil {
					// This potential to lose your token balance is a major reason why Proof of Stake is generally secure.
					// If you try to alter the blockchain for your benefit and you get caught, you lose your entire staked token balance.
					// It’s a major deterrent for bad actors.
					log.Printf("%v not a number: %v", scanBPM.Text(), err)
					delete(validators, address)
					conn.Close()
				}

				mutex.Lock()
				oldLastIndex := Blockchain[len(Blockchain)-1]
				mutex.Unlock()

				// create a newBlock for consideration to be forged
				newBlock, err := generateBlock(oldLastIndex, bpm, address)
				if err != nil {
					log.Println(err)
					continue
				}

				if isBlockValid(newBlock, oldLastIndex) {
					candidateBlocks <- newBlock
				}
				io.WriteString(conn, "\nEnter a new BPM: ")
			}
		}
	}()

	// Simulate receiveing broadcast
	for {
		time.Sleep(time.Minute)
		mutex.Lock()
		output, err := json.Marshal(Blockchain)
		mutex.Unlock()
		if err != nil {
			log.Fatal(err)
		}
		io.WriteString(conn, string(output)+"\n")
	}
}

// pickWinner creates a lettery pool of validators and chooses the validator who gets to forge a block to the blockchain
// by random selecting from the pool, weighted by amount of tokens staked

func pickWinner() {
	time.Sleep(30 * time.Second)
	mutex.Lock()
	temp := tempBlocks
	mutex.Unlock()

	lotteryPool := []string{}
	if len(temp) > 0 {
		// slighly modified traditional proof of stake algo
		// from all validators who submitted a block, weight them by the # of staked tokens
		// in traditional proof of stake, validators can participate without submitting a block to be forged
	OUTER:
		for _, block := range temp {
			// if already in lottery pool skip back to the loop OUTER loop
			for _, node := range lotteryPool {
				if block.Validator == node {
					continue OUTER
				}
			}

			// lock list of validators to prevent data race
			mutex.Lock()
			setValidators := validators
			mutex.Unlock()

			// we fill out lotteryPool with copies of the validators address
			// they get a copy for each token they have staked
			// so a validator who put 100 tokens will get 100 entries in the lotteryPool
			// a validator who only put 1 token gets 1 entry
			// this leads to a weighting system based on the stakes of the validators
			k, ok := setValidators[block.Validator]
			if ok {
				for i := 0; i < k; i++ {
					lotteryPool = append(lotteryPool, block.Validator)
				}
			}
		}

		// randomly pick winner from lottery pool
		s := rand.NewSource(time.Now().Unix())
		r := rand.New(s)
		lotteryWinner := lotteryPool[r.Intn(len(lotteryPool))]

		// add block of winner to blockchain and let all other nodes know
		for _, block := range temp {
			if block.Validator == lotteryWinner {
				mutex.Lock()
				Blockchain = append(Blockchain, block)
				mutex.Unlock()

				for range validators {
					announcements <- "\nwinning validator: " + lotteryWinner + "\n"
				}
				break
			}
		}
	}

	mutex.Lock()
	tempBlocks = []Block{}
	mutex.Unlock()
}
