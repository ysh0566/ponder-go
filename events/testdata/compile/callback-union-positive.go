package main
import("encoding/json";"ponder.local/ponder-go/abi";"ponder.local/ponder-go/events")
func main(){
 var original abi.Function
 if err:=json.Unmarshal([]byte("{\"inputs\":[{\"internalType\":\"address\",\"name\":\"to\",\"type\":\"address\"},{\"internalType\":\"uint256\",\"name\":\"amount\",\"type\":\"uint256\"}],\"name\":\"transfer\",\"outputs\":[{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}"),&original);err!=nil{panic(err)}
 callbacks:=[]events.EventCallback{events.ContractCallback[abi.Function]{CallbackBase:events.CallbackBase{Name:"Erc20.transfer()",Filter:events.Object{"type":"trace"}},ABIItem:original}}
 narrowed:=callbacks[0].(events.ContractCallback[abi.Function])
 copied:=narrowed.ABIItem
 copied.Outputs=[]abi.Parameter{}
 narrowed.ABIItem=copied
 callbacks[0]=narrowed
 if len(original.Outputs)!=1||len(callbacks[0].(events.ContractCallback[abi.Function]).ABIItem.Outputs)!=0{panic("copy modified shared original ABI")}
 projected:=events.CallbackObjects(callbacks)
 if len(projected)!=1||projected[0]["abiItem"].(abi.Function).Name!="transfer"||len(projected[0]["abiItem"].(abi.Function).Outputs)!=0{panic("typed callback projection changed ABI")}
}
