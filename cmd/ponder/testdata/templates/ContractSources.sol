// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract FileSource {
    event FileCreated(string indexed indexedFilename, bytes32 indexed checksum, string filename, uint256 size, bytes metadata);
    event FileDeleted(string indexed indexedFilename, bytes32 indexed checksum, string filename);

    mapping(string => string) public contents;

    function create(string calldata name, string calldata body, bytes calldata metadata) external {
        contents[name] = body;
        emit FileCreated(name, keccak256(bytes(body)), name, bytes(body).length, metadata);
    }

    function remove(string calldata name) external {
        bytes32 checksum = keccak256(bytes(contents[name]));
        delete contents[name];
        emit FileDeleted(name, checksum, name);
    }

    function createVia(address source, string calldata name, string calldata body, bytes calldata metadata) external {
        contents[name] = string.concat("via route: ", body);
        FileSource(source).create(name, body, metadata);
    }

    function forward(address target, bytes calldata data) external {
        (bool ok,) = target.call(data);
        require(ok);
    }
}

contract FileFrontend {
    function readFile(address source, string calldata name) external view returns (string memory) {
        return FileSource(source).contents(name);
    }
}

contract BatchCalls {
    struct Call { address target; bool allowFailure; bytes callData; }
    struct Result { bool success; bytes returnData; }

    function aggregate3(Call[] calldata calls) external payable returns (Result[] memory results) {
        results = new Result[](calls.length);
        for (uint256 i = 0; i < calls.length; i++) {
            (bool ok, bytes memory data) = calls[i].target.call(calls[i].callData);
            require(ok || calls[i].allowFailure);
            results[i] = Result(ok, data);
        }
    }
}
