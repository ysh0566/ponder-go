// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// Indexes replies by the hash of the complete Universal Resolver request.
// The fixture distinguishes a stored empty response from a missing record.
contract RecordResolver {
    struct Reply { bool exists; bytes data; }
    mapping(bytes32 => Reply) private replies;
    error ResolverNotFound(bytes name);

    function set(bytes32 key, bytes calldata data) external {
        replies[key] = Reply(true, data);
    }

    function resolveWithGateways(bytes calldata name, bytes calldata, string[] calldata)
        external view returns (bytes memory, address)
    {
        Reply storage reply = replies[keccak256(msg.data)];
        if (!reply.exists) revert ResolverNotFound(name);
        return (reply.data, address(this));
    }
}
