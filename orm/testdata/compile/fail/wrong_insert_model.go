package compilefixture
import ("context";"github.com/ysh0566/ponder-go/orm/testdata/strict";"github.com/ysh0566/ponder-go/orm/testdata/nullable")
func check(){ _ = strict.Account; _ = nullable.Account; _,_ = strict.Account.Bind(nil).Insert(context.Background(),nullable.NewAccountInsert("0x0000000000000000000000000000000000000000")) }
