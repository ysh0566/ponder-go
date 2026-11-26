// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract WrappedDeposits {
    event Deposit(address indexed dst, uint256 wad);

    function deposit() external payable {
        emit Deposit(msg.sender, msg.value);
    }
}
