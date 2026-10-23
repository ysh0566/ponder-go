package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"ponder.local/ponder-go/checkpoint"
	"ponder.local/ponder-go/interval"
	"strconv"
)

type request struct {
	Op         string            `json:"op"`
	Checkpoint map[string]string `json:"checkpoint"`
	Encoded    string            `json:"encoded"`
	A, B       []interval.Interval
	Interval   interval.Interval
	Size       int64
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	for scanner.Scan() {
		var r request
		var value any
		err := json.Unmarshal(scanner.Bytes(), &r)
		if err == nil {
			switch r.Op {
			case "encode":
				get := func(k string) uint64 { v, _ := strconv.ParseUint(r.Checkpoint[k], 10, 64); return v }
				et, _ := strconv.Atoi(r.Checkpoint["eventType"])
				value, err = checkpoint.Encode(checkpoint.Checkpoint{BlockTimestamp: get("blockTimestamp"), ChainID: get("chainId"), BlockNumber: get("blockNumber"), TransactionIndex: get("transactionIndex"), EventType: et, EventIndex: get("eventIndex")})
			case "decode":
				var c checkpoint.Checkpoint
				c, err = checkpoint.Decode(r.Encoded)
				value = map[string]any{"blockTimestamp": fmt.Sprint(c.BlockTimestamp), "chainId": fmt.Sprint(c.ChainID), "blockNumber": fmt.Sprint(c.BlockNumber), "transactionIndex": fmt.Sprint(c.TransactionIndex), "eventType": c.EventType, "eventIndex": fmt.Sprint(c.EventIndex)}
			case "union":
				value = interval.Union(r.A)
			case "intersection":
				value = interval.Intersection(r.A, r.B)
			case "difference":
				value = interval.Difference(r.A, r.B)
			case "chunks":
				value, err = interval.Chunks(r.Interval, r.Size)
			default:
				err = fmt.Errorf("unknown probe operation %s", r.Op)
			}
		}
		var response map[string]any
		if err != nil {
			response = map[string]any{"error": err.Error()}
		} else {
			response = map[string]any{"value": value}
		}
		if e := json.NewEncoder(os.Stdout).Encode(response); e != nil {
			panic(e)
		}
	}
	if e := scanner.Err(); e != nil {
		panic(e)
	}
}
