package compilefixture
import ("github.com/ysh0566/ponder-go/schema";"github.com/ysh0566/ponder-go/orm/testdata/strict")
func check(){ table := schema.Table{Name:"account",Columns:[]schema.Column{{Name:"address",Type:"text",Primary:true},{Name:"balance",Type:"integer",NotNull:true}}}; _ = table; _ = strict.Account; _ = strict.Account.Bind(nil) }
