package custom

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ysh0566/ponder-go/orm"
)

type Units int64

var ErrUnits = errors.New("invalid units")

var UnitsCodec = orm.ColumnCodec[Units]{
	Encode: func(v Units) (any, error) {
		if v < 0 {
			return nil, ErrUnits
		}
		return fmt.Sprintf("u/%d", v), nil
	},
	Decode: func(raw any) (Units, error) {
		s, err := orm.DecodeString(raw)
		if err != nil {
			return 0, err
		}
		n, ok := strings.CutPrefix(s, "u/")
		v, err := strconv.ParseInt(n, 10, 64)
		if !ok || err != nil || v < 0 {
			return 0, ErrUnits
		}
		return Units(v), nil
	},
}

type Payload []byte

var PayloadCodec = orm.ColumnCodec[Payload]{
	Encode: func(v Payload) (any, error) { return []byte(v), nil },
	Decode: func(raw any) (Payload, error) { return orm.DecodeBytes(raw) },
}
