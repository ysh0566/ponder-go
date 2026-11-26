// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract ExamplePool {
    address public immutable token0;
    address public immutable token1;
    event Flash(address indexed sender, address indexed recipient, uint256 amount0, uint256 amount1, uint256 paid0, uint256 paid1);

    constructor(address a, address b) { token0 = a; token1 = b; }
    function record(uint256 a, uint256 b, uint256 paidA, uint256 paidB) external {
        emit Flash(msg.sender, msg.sender, a, b, paidA, paidB);
    }
}

contract ProtocolExamples {
    event Trade(address trader, address subject, bool isBuy, uint256 shareAmount, uint256 ethAmount,
                uint256 protocolEthAmount, uint256 subjectEthAmount, uint256 supply);
    event PoolCreated(address indexed token0, address indexed token1, uint24 indexed fee, int24 tickSpacing, address pool);

    address private constant A = address(0xaa);
    address private constant B = address(0xbb);
    address private constant C = address(0xcc);
    ExamplePool private second;

    function firstTrades() external {
        emit Trade(C, C, true, 0, 43, 2, 1, 0);
        emit Trade(A, B, true, 3, 100, 7, 4, 3);
        emit Trade(A, B, true, 4, 20, 2, 3, 7);
    }
    function nextTrades() external {
        emit Trade(A, B, false, 2, 41, 3, 5, 5);
        emit Trade(B, B, true, 1, 2, 0, 1, 6);
        emit Trade(A, C, true, 2**100, 7, 0, 0, 2**100);
    }
    function firstFlashes() external {
        ExamplePool outsider = new ExamplePool(C, A);
        outsider.record(99, 98, 97, 96);
        ExamplePool first = new ExamplePool(A, B);
        second = new ExamplePool(B, C);
        emit PoolCreated(A, B, 500, 10, address(first));
        emit PoolCreated(B, C, 3000, 60, address(second));
        first.record(3, 5, 1, 2);
        first.record(4, 7, 11, 13);
        second.record(6, 8, 17, 19);
    }
    function nextFlash() external { second.record(2, 3, 101, 103); }
}
