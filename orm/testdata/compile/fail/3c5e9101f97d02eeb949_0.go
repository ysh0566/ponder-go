package compilefixture
import ("context";"math/big";"ponder.local/ponder-go/orm/testdata/strict")
func check(){ _ = big.NewInt(10); _,_ = strict.Account.Bind(nil).SQL().Insert(context.Background(),strict.NewAccountInsert("0x0000000000000000000000000000000000000001",nil)) }
