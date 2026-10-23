package compilefixture
import ("context";"ponder.local/ponder-go/orm/testdata/strict";"ponder.local/ponder-go/orm/testdata/nullable")
func check(){ _ = strict.Account; _ = nullable.Account; _,_ = nullable.Account.Bind(nil).Insert(context.Background(),nullable.NewAccountInsert("0x0000000000000000000000000000000000000000")) }
