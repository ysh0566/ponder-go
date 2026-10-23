// Package checkpoint implements Ponder's lexicographically sortable 75-digit cursor.
package checkpoint

import (
	"fmt"
	"strconv"
	"strings"
)

const Length = 75
const (
	Transactions = 2
	Blocks       = 5
	Logs         = 5
	Traces       = 7
)

type Checkpoint struct {
	BlockTimestamp   uint64 `json:"blockTimestamp"`
	ChainID          uint64 `json:"chainId"`
	BlockNumber      uint64 `json:"blockNumber"`
	TransactionIndex uint64 `json:"transactionIndex"`
	EventType        int    `json:"eventType"`
	EventIndex       uint64 `json:"eventIndex"`
}

var Zero = Checkpoint{}
var Max = Checkpoint{9_999_999_999, 9_999_999_999_999_999, 9_999_999_999_999_999, 9_999_999_999_999_999, 9, 9_999_999_999_999_999}
var ZeroString = strings.Repeat("0", Length)
var MaxString = strings.Repeat("9", Length)

func Encode(c Checkpoint) (string, error) {
	if c.EventType < 0 || c.EventType > 9 {
		return "", fmt.Errorf("Got invalid event type %d, expected a number from 0 to 9", c.EventType)
	}
	s := fmt.Sprintf("%010d%016d%016d%016d%d%016d", c.BlockTimestamp, c.ChainID, c.BlockNumber, c.TransactionIndex, c.EventType, c.EventIndex)
	if len(s) != Length {
		return "", fmt.Errorf("Invalid stringified checkpoint: %s", s)
	}
	return s, nil
}
func MustEncode(c Checkpoint) string {
	s, e := Encode(c)
	if e != nil {
		panic(e)
	}
	return s
}
func Decode(s string) (Checkpoint, error) {
	var c Checkpoint
	if len(s) != Length || strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return c, fmt.Errorf("invalid checkpoint: expected %d decimal digits", Length)
	}
	c.BlockTimestamp, _ = strconv.ParseUint(s[:10], 10, 64)
	c.ChainID, _ = strconv.ParseUint(s[10:26], 10, 64)
	c.BlockNumber, _ = strconv.ParseUint(s[26:42], 10, 64)
	c.TransactionIndex, _ = strconv.ParseUint(s[42:58], 10, 64)
	c.EventType = int(s[58] - '0')
	c.EventIndex, _ = strconv.ParseUint(s[59:], 10, 64)
	return c, nil
}
func Equal(a, b Checkpoint) bool          { return a == b }
func Greater(a, b Checkpoint) bool        { return MustEncode(a) > MustEncode(b) }
func GreaterOrEqual(a, b Checkpoint) bool { return MustEncode(a) >= MustEncode(b) }
func Maximum(cs ...Checkpoint) (Checkpoint, error) {
	if len(cs) == 0 {
		return Zero, fmt.Errorf("empty checkpoint list")
	}
	r := cs[0]
	for _, c := range cs[1:] {
		if Greater(c, r) {
			r = c
		}
	}
	return r, nil
}
func Minimum(cs ...Checkpoint) (Checkpoint, error) {
	if len(cs) == 0 {
		return Zero, fmt.Errorf("empty checkpoint list")
	}
	r := cs[0]
	for _, c := range cs[1:] {
		if Greater(r, c) {
			r = c
		}
	}
	return r, nil
}
func Block(timestamp, chain, number uint64, roundUp bool) Checkpoint {
	c := Zero
	if roundUp {
		c = Max
	}
	c.BlockTimestamp = timestamp
	c.ChainID = chain
	c.BlockNumber = number
	return c
}
