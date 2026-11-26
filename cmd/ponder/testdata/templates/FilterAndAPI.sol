// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract FilterAndAPI {
    event Swap(address indexed payer, address recipient, address indexed engine, bytes32 indexed poolId,
               bool riskyForStable, uint256 deltaIn, uint256 deltaOut, bool fromMargin, bool toMargin);
    event URI(string value, uint256 indexed id);
    event Transfer(address indexed from, address indexed to, uint256 amount);
    event Approval(address indexed owner, address indexed spender, uint256 amount);

    function swaps(bool includeSwap) external {
        emit URI("metadata", 7);
        if (includeSwap) {
            emit Swap(address(0xaa), address(0xbb), address(this), bytes32(uint256(42)), true, 3, 7, false, true);
        }
    }

    function tokenEvents() external {
        address a = address(0xaa);
        address b = address(0xbb);
        address c = address(0xcc);
        emit Transfer(a, b, 999 ether);
        emit Transfer(c, a, 4 ether + 1);
        emit Transfer(a, a, 1);
        for (uint256 i = 1; i <= 9; i++) emit Transfer(b, c, i);
        emit Approval(a, c, 13);
        emit Approval(a, c, 17);
    }
}
