// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

contract RouterImplementation {
    event Liquidation(uint256 lienId, address liquidator);
    event OwnershipTransferred(address indexed user, address indexed newOwner);

    function fire(address liquidator, address owner) external {
        emit Liquidation(123, liquidator);
        emit OwnershipTransferred(msg.sender, owner);
    }
}

contract RouterProxy {
    constructor() {
        RouterImplementation impl = new RouterImplementation();
        impl.fire(address(1), address(2));
        (bool ok,) = address(impl).delegatecall(abi.encodeCall(impl.fire, (address(42), address(43))));
        require(ok);
    }
}
