// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract GatewayResolver {
    error OffchainLookup(address sender, string[] urls, bytes callData, bytes4 callbackSelector, bytes extraData);
    error ResolverNotFound(bytes name);
    struct Query {address sender; string[] urls; bytes data;}
    string private base;
    uint256 private offset;

    function setup(string calldata gateway) external { base = gateway; }
    function setOffset(uint256 n) external {offset = n;}
    function urls(string memory path) private view returns (string[] memory a) {
        a = new string[](1);
        a[0] = string.concat(base,path);
    }
    function local() private pure returns (string[] memory a) {
        a = new string[](1);
        a[0] = "x-batch-gateway:true";
    }
    function batchData(Query[] memory queries) private pure returns(bytes memory) {
        return abi.encodeWithSignature("query((address,string[],bytes)[])",queries);
    }
    function value(uint8 mode) external view returns(uint256) {
        string[] memory a = urls("/post");
        address sender=address(this);
        if (mode==0) {
            a=new string[](3);
            a[0]=string.concat(base,"/bad");
            a[1]=string.concat(base,"/malformed");
            a[2]=string.concat(base,"/get/{sender}/{data}");
        }
        if (mode==2) sender=address(1);
        if (mode==3) a=urls("/malformed");
        if (mode==4) a=urls("/wait");
        if (mode==5) revert ResolverNotFound(hex"ff");
        if (mode==6) a=urls("/bad");
        revert OffchainLookup(sender,a,hex"12",this.verify.selector,abi.encode(mode));
    }
    function verify(bytes calldata response, bytes calldata extra) external view returns(uint256) {
        uint8 mode=abi.decode(extra,(uint8));
        if (mode==7) revert OffchainLookup(address(this),urls("/post"),hex"12",this.verify.selector,abi.encode(uint8(1)));
        if (mode==8) revert OffchainLookup(address(this),urls("/post"),hex"12",this.verify.selector,extra);
        return abi.decode(response,(uint256))+offset;
    }
    function reverseWithGateways(bytes calldata,uint256,string[] calldata) external view returns(string memory,address,address) {
        revert OffchainLookup(address(this),urls("/post"),hex"12",this.reverseResult.selector,"");
    }
    function reverseResult(bytes calldata result,bytes calldata) external view returns(string memory,address,address) {
        require(abi.decode(result,(uint256))==42);
        return ("alice.eth",address(this),address(this));
    }
    function findResolver(bytes calldata) external view returns(address,bytes32,uint256) {
        revert OffchainLookup(address(this),urls("/post"),hex"12",this.findResult.selector,"");
    }
    function findResult(bytes calldata result,bytes calldata) external view returns(address,bytes32,uint256) {
        require(abi.decode(result,(uint256))==42);
        return (address(this),bytes32(0),0);
    }
    struct Call3 {address target; bool allowFailure; bytes callData;}
    struct Result {bool success; bytes returnData;}
    function aggregate3(Call3[] calldata calls) external view returns(Result[] memory) {
        revert OffchainLookup(address(this),urls("/post"),hex"12",this.aggregateResult.selector,abi.encode(calls.length));
    }
    function aggregateResult(bytes calldata result,bytes calldata extra) external pure returns(Result[] memory rows) {
        rows=new Result[](abi.decode(extra,(uint256)));
        for(uint256 i=0;i<rows.length;i++) rows[i]=Result(true,result);
    }
    function resolveWithGateways(bytes calldata,bytes calldata record,string[] calldata gateways) external view returns(bytes memory,address) {
        // Include a nested local batch and an HTTP error alongside a success.
        Query[] memory inner=new Query[](1);
        inner[0]=Query(address(this),urls("/post"),hex"12");
        Query[] memory entries=new Query[](3);
        entries[0]=Query(address(this),local(),batchData(inner));
        entries[1]=Query(address(this),urls("/bad"),hex"ab");
        entries[2]=Query(address(this),urls("/malformed"),hex"cd");
        revert OffchainLookup(address(this),gateways,batchData(entries),this.recordResult.selector,record);
    }
    function recordResult(bytes calldata result,bytes calldata record) external view returns(bytes memory,address) {
        (bool[] memory failed,bytes[] memory responses)=abi.decode(result,(bool[],bytes[]));
        require(failed.length==3 && !failed[0] && failed[1] && failed[2]);
        (bool[] memory nestedFailed,bytes[] memory nested)=abi.decode(responses[0],(bool[],bytes[]));
        require(!nestedFailed[0] && abi.decode(nested[0],(uint256))==42);
        require(keccak256(responses[1])==keccak256(abi.encodeWithSignature("HttpError(uint16,string)",uint16(503),"HTTP request failed.")));
        require(keccak256(responses[2])==keccak256(abi.encodeWithSignature("Error(string)","Offchain gateway response is malformed. Response data must be a hex value.")));
        if (bytes4(record[:4])==bytes4(keccak256("addr(bytes32)"))) return (abi.encode(address(0xabc)),address(this));
        return (abi.encode("data:image/png;base64,aWNvbg=="),address(this));
    }
}
