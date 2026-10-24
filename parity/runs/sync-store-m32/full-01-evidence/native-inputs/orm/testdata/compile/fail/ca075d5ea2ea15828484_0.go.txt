package compilefixture
import ("ponder.local/ponder-go/schema";"ponder.local/ponder-go/orm/testdata/strict")
func check(){ table := schema.Table{Name:"account",Columns:[]schema.Column{{Name:"address",Type:"text",Primary:true},{Name:"balance",Type:"integer",NotNull:true}}}; _ = table; _ = strict.Account; _ = table.Bind(nil) }
