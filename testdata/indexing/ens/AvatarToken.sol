// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract AvatarToken {
    mapping(bytes4 => mapping(uint256 => string)) private uris;

    function set(bytes4 method, uint256 id, string calldata value) external {
        uris[method][id] = value;
    }

    function tokenURI(uint256 id) external view returns (string memory) {
        return uris[msg.sig][id];
    }

    function uri(uint256 id) external view returns (string memory) {
        return uris[msg.sig][id];
    }
}
